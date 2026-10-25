package sshclient

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"

	"sshclient/internal/model"
	"sshclient/internal/sshclient/sshtest"
)

const forwardWait = 5 * time.Second

// forwardServer запускает тестовый сервер и возвращает его вместе с функцией подключения для раннера.
func forwardServer(t *testing.T) (*sshtest.Server, func(ctx context.Context) (*ssh.Client, error)) {
	t.Helper()
	key, pub := testClientKey(t, "work", "")
	srv := sshtest.StartServer(t, sshtest.Opts{AcceptKey: pub})
	kh := trustedKnownHosts(t, srv.Addr(), srv.HostKey())
	target := targetFor(t, srv.Addr(), key)
	return srv, func(ctx context.Context) (*ssh.Client, error) {
		return Dial(ctx, target, NewPrompter(io.Discard, strings.NewReader("")), kh)
	}
}

// echoServer — локальный TCP-сервер, возвращающий всё полученное.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := io.Copy(c, c)
				ignoreErr(err)
				ignoreErr(c.Close())
			}()
		}
	}()
	t.Cleanup(func() {
		ignoreErr(ln.Close())
		mu.Lock()
		for _, c := range conns {
			ignoreErr(c.Close())
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String()
}

// freeAddr возвращает адрес 127.0.0.1 со свободным в данный момент портом.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// startRunner создаёт раннер с быстрым backoff, запускает его и останавливает в Cleanup.
func startRunner(t *testing.T, f model.Forward, dial func(ctx context.Context) (*ssh.Client, error)) *ForwardRunner {
	t.Helper()
	r := NewForwardRunner(f, dial)
	r.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.State(); st.Status != "running" {
		t.Fatalf("state after Start = %+v", st)
	}
	return r
}

// roundTrip отправляет msg через conn и ждёт эхо.
func roundTrip(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(forwardWait)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != msg {
		t.Fatalf("echo = %q, want %q", buf, msg)
	}
}

func dialEcho(t *testing.T, addr, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, forwardWait)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignoreErr(c.Close()) }()
	roundTrip(t, c, msg)
}

// waitState опрашивает State, пока cond не выполнится.
func waitState(t *testing.T, r *ForwardRunner, what string, cond func(ForwardState) bool) {
	t.Helper()
	deadline := time.Now().Add(forwardWait)
	for {
		st := r.State()
		if cond(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s, state %+v", what, st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitEvent читает Events, пока не придёт состояние со статусом want.
func waitEvent(t *testing.T, r *ForwardRunner, want string) {
	t.Helper()
	timeout := time.After(forwardWait)
	for {
		select {
		case st, ok := <-r.Events():
			if !ok {
				t.Fatalf("events closed before %q", want)
			}
			if st.Status == want {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for event %q, state %+v", want, r.State())
		}
	}
}

// drainEvents выбирает уже накопленные события, не дожидаясь новых.
func drainEvents(r *ForwardRunner) {
	for {
		select {
		case <-r.Events():
		default:
			return
		}
	}
}

func TestLocalForwardProxiesBytes(t *testing.T) {
	_, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echo}, dial)

	c, err := net.DialTimeout("tcp", listen, forwardWait)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c, "hello через L")
	waitState(t, r, "one conn", func(s ForwardState) bool { return s.Conns == 1 })
	roundTrip(t, c, "second")
	ignoreErr(c.Close())
	waitState(t, r, "no conns", func(s ForwardState) bool { return s.Conns == 0 && s.Status == "running" })
}

func TestLocalForwardPortBusy(t *testing.T) {
	_, dial := forwardServer(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignoreErr(busy.Close()) })
	_, port, err := net.SplitHostPort(busy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	r := NewForwardRunner(model.Forward{Kind: "L", Listen: busy.Addr().String(), Target: echoServer(t)}, dial)
	t.Cleanup(r.Stop)
	err = r.Start(context.Background())
	if err == nil {
		t.Fatal("Start on a busy port succeeded")
	}
	if !strings.Contains(err.Error(), port) {
		t.Fatalf("error %q does not mention port %s", err, port)
	}
	if st := r.State(); st.Status != "error" || st.Error == "" {
		t.Fatalf("state = %+v", st)
	}
}

func TestDynamicForwardSocks5(t *testing.T) {
	_, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "D", Listen: listen}, dial)

	d, err := proxy.SOCKS5("tcp", listen, nil, &net.Dialer{Timeout: forwardWait})
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Dial("tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c, "hello через SOCKS5")
	waitState(t, r, "one conn", func(s ForwardState) bool { return s.Conns == 1 })
	ignoreErr(c.Close())
	waitState(t, r, "no conns", func(s ForwardState) bool { return s.Conns == 0 })

	// Недоступная цель: SOCKS-сервер отвечает ошибкой, раннер продолжает работать.
	if c, err := d.Dial("tcp", freeAddr(t)); err == nil {
		ignoreErr(c.Close())
		t.Fatal("dial to a closed port through SOCKS5 succeeded")
	}
	if st := r.State(); st.Status != "running" {
		t.Fatalf("state = %+v", st)
	}
}

func TestRemoteForward(t *testing.T) {
	_, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t) // слушает тестовый сервер, он в том же процессе
	r := startRunner(t, model.Forward{Kind: "R", Listen: listen, Target: echo}, dial)

	c, err := net.DialTimeout("tcp", listen, forwardWait)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c, "hello через R")
	waitState(t, r, "one conn", func(s ForwardState) bool { return s.Conns == 1 })
	ignoreErr(c.Close())
	waitState(t, r, "no conns", func(s ForwardState) bool { return s.Conns == 0 })
}

func TestStopClosesListener(t *testing.T) {
	_, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echo}, dial)

	// Открытое соединение тоже должно закрыться при Stop.
	c, err := net.DialTimeout("tcp", listen, forwardWait)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignoreErr(c.Close()) }()
	roundTrip(t, c, "before stop")

	r.Stop()
	r.Stop() // идемпотентно
	if st := r.State(); st.Status != "stopped" || st.Conns != 0 {
		t.Fatalf("state after Stop = %+v", st)
	}
	if err := c.SetReadDeadline(time.Now().Add(forwardWait)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("proxied connection still open after Stop")
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatalf("port not freed after Stop: %v", err)
	}
	ignoreErr(ln.Close())

	timeout := time.After(forwardWait)
	for {
		select {
		case _, ok := <-r.Events():
			if !ok {
				if err := r.Start(context.Background()); err == nil {
					t.Fatal("Start after Stop succeeded")
				}
				return
			}
		case <-timeout:
			t.Fatal("events channel not closed after Stop")
		}
	}
}

func TestReconnectAfterDrop(t *testing.T) {
	for _, kind := range []string{"L", "R", "D"} {
		t.Run(kind, func(t *testing.T) {
			srv, dial := forwardServer(t)
			echo := echoServer(t)
			listen := freeAddr(t)
			f := model.Forward{Kind: kind, Listen: listen, Target: echo}
			if kind == "D" {
				f.Target = ""
			}
			r := startRunner(t, f, dial)
			check := func(msg string) {
				t.Helper()
				if kind != "D" {
					dialEcho(t, listen, msg)
					return
				}
				d, err := proxy.SOCKS5("tcp", listen, nil, &net.Dialer{Timeout: forwardWait})
				if err != nil {
					t.Fatal(err)
				}
				c, err := d.Dial("tcp", echo)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { ignoreErr(c.Close()) }()
				roundTrip(t, c, msg)
			}
			check("before drop")
			waitState(t, r, "no conns", func(s ForwardState) bool { return s.Conns == 0 })
			drainEvents(r) // события запуска не должны засчитаться за переподключение

			srv.DropConnections()
			waitEvent(t, r, "starting")
			waitEvent(t, r, "running")
			check("after reconnect")
		})
	}
}

func TestForwardEventsDoNotBlock(t *testing.T) {
	_, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echo}, dial)
	// Никто не читает Events: десятки смен Conns не должны остановить раннер.
	for i := range 40 {
		dialEcho(t, listen, "ping "+strconv.Itoa(i))
	}
	waitState(t, r, "no conns", func(s ForwardState) bool { return s.Conns == 0 })
	r.Stop()
}

func TestForwardBackoffSchedule(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 32, 60, 60}
	for i, w := range want {
		if got := defaultBackoff(i + 1); got != w*time.Second {
			t.Fatalf("backoff(%d) = %v, want %v", i+1, got, w*time.Second)
		}
	}
	if got := defaultBackoff(1000); got != 60*time.Second {
		t.Fatalf("backoff(1000) = %v", got)
	}
}

func TestReconnectRetriesFailedDial(t *testing.T) {
	srv, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	var (
		mu       sync.Mutex
		calls    int
		attempts []int
	)
	flaky := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 2 || n == 3 { // первые две попытки после обрыва неудачны
			return nil, errors.New("сервер недоступен")
		}
		return dial(ctx)
	}
	r := NewForwardRunner(model.Forward{Kind: "L", Listen: listen, Target: echo}, flaky)
	r.backoff = func(attempt int) time.Duration {
		mu.Lock()
		attempts = append(attempts, attempt)
		mu.Unlock()
		return 10 * time.Millisecond
	}
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drainEvents(r)

	srv.DropConnections()
	timeout := time.After(forwardWait)
	sawError := false
	for done := false; !done; {
		select {
		case st, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed")
			}
			switch {
			case st.Status == "starting" && st.Error == "сервер недоступен":
				sawError = true
			case st.Status == "running":
				done = true
			}
		case <-timeout:
			t.Fatalf("timeout, state %+v", r.State())
		}
	}
	if !sawError {
		t.Fatal("failed attempt not reported in Error while starting")
	}
	if st := r.State(); st.Error != "" {
		t.Fatalf("Error not cleared after reconnect: %+v", st)
	}
	mu.Lock()
	got := append([]int(nil), attempts...)
	mu.Unlock()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("backoff attempts = %v, want [1 2 3]", got)
	}
	dialEcho(t, listen, "after retries")
}
