package sshclient

import (
	"context"
	"errors"
	"fmt"
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

const forwardWait = 15 * time.Second

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

func TestListenErrorAddrInUse(t *testing.T) {
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
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("Start on a busy port succeeded")
	}
	st := r.State()
	want := "Порт " + port + " уже занят другой программой: выберите другой порт или закройте программу, которая его держит"
	if st.Error != want {
		t.Fatalf("Error = %q, want %q", st.Error, want)
	}
	if !strings.Contains(st.Detail, "listen tcp") {
		t.Fatalf("Detail %q has no system text", st.Detail)
	}
}

func TestRemoteListenRefusedText(t *testing.T) {
	_, dial := forwardServer(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0") // тестовый сервер в том же процессе: порт занят и для него
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignoreErr(busy.Close()) })
	_, port, err := net.SplitHostPort(busy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := NewForwardRunner(model.Forward{Kind: "R", Listen: busy.Addr().String(), Target: echoServer(t)}, dial)
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("Start on a port the server cannot open succeeded")
	}
	st := r.State()
	want := "Сервер не открыл порт " + port + ": он занят на сервере или проброс запрещён его настройкой (AllowTcpForwarding, GatewayPorts)"
	if st.Error != want {
		t.Fatalf("Error = %q, want %q", st.Error, want)
	}
	if st.Detail == "" {
		t.Fatal("Detail is empty")
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
		if got := DefaultBackoff(i + 1); got != w*time.Second {
			t.Fatalf("backoff(%d) = %v, want %v", i+1, got, w*time.Second)
		}
	}
	if got := DefaultBackoff(1000); got != 60*time.Second {
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

func TestStopRemoteForwardOnFrozenServer(t *testing.T) {
	srv, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "R", Listen: listen, Target: echo}, dial)

	srv.FreezeConnections() // cancel-tcpip-forward больше не получит ответа
	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(forwardWait):
		srv.DropConnections() // отпускаем зависший Stop, чтобы Cleanup завершился
		<-done
		t.Fatal("Stop hangs on a frozen server connection")
	}
	if st := r.State(); st.Status != "stopped" {
		t.Fatalf("state after Stop = %+v", st)
	}
}

func TestStopDuringRemoteReconnectOnFrozenServer(t *testing.T) {
	srv, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "R", Listen: listen, Target: echo}, dial)

	srv.FreezeConnections()
	srv.DropConnections() // переподключение пройдёт рукопожатие и зависнет на tcpip-forward
	waitState(t, r, "starting", func(s ForwardState) bool { return s.Status == "starting" })
	time.Sleep(100 * time.Millisecond) // даём попытке дойти до запроса порта (backoff 10 мс)

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(forwardWait):
		srv.DropConnections()
		<-done
		t.Fatal("Stop hangs while reconnect waits for tcpip-forward")
	}
}

func TestLocalListenerFailureIsSticky(t *testing.T) {
	srv, dial := forwardServer(t)
	echo := echoServer(t)
	listen := freeAddr(t)
	r := startRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echo}, dial)
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		t.Fatal(err)
	}

	r.mu.Lock()
	ln := r.ln
	r.mu.Unlock()
	ignoreErr(ln.Close()) // Accept падает не из-за Stop
	waitState(t, r, "error", func(s ForwardState) bool { return s.Status == "error" })
	if st := r.State(); !strings.Contains(st.Error, port) {
		t.Fatalf("error %q does not mention port %s", st.Error, port)
	}

	drainEvents(r)
	srv.DropConnections()
	time.Sleep(200 * time.Millisecond) // переподключение (backoff 10 мс) успело бы произойти
	for {
		select {
		case st := <-r.Events():
			if st.Status != "error" {
				t.Fatalf("status changed after listener failure: %+v", st)
			}
			continue
		default:
		}
		break
	}
	if st := r.State(); st.Status != "error" {
		t.Fatalf("state = %+v", st)
	}
	r.Stop()
	if st := r.State(); st.Status != "stopped" {
		t.Fatalf("state after Stop = %+v", st)
	}
}

func TestReconnectStopsWhenServerNotTrusted(t *testing.T) {
	// Пользователь отказался доверять ключу при переподключении: проброс завершается ошибкой
	// окончательно, без новых попыток и новых вопросов.
	testReconnectStopsOn(t, &userError{msg: msgHostNotTrusted, cause: ErrHostNotTrusted})
}

func TestReconnectStopsWhenHostKeyChanged(t *testing.T) {
	// Изменившийся ключ сервера отклоняется всегда: повторы дали бы тот же отказ.
	testReconnectStopsOn(t, fmt.Errorf("%w: host example", ErrHostKeyChanged))
}

func testReconnectStopsOn(t *testing.T, refused error) {
	t.Helper()
	srv, dial := forwardServer(t)
	listen := freeAddr(t)
	var (
		mu    sync.Mutex
		calls int
	)
	dialer := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n > 1 {
			return nil, refused
		}
		return dial(ctx)
	}
	r := NewForwardRunner(model.Forward{Kind: "L", Listen: listen, Target: echoServer(t)}, dialer)
	r.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drainEvents(r)

	srv.DropConnections()
	timeout := time.After(forwardWait)
	for done := false; !done; {
		select {
		case st := <-r.Events():
			if st.Status == "error" {
				if st.Error != refused.Error() {
					t.Fatalf("state %+v", st)
				}
				done = true
			}
		case <-timeout:
			t.Fatalf("timeout, state %+v", r.State())
		}
	}
	time.Sleep(100 * time.Millisecond) // десять пауз переподключения
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 2 {
		t.Fatalf("dial called %d times, want 2", n)
	}
	if st := r.State(); st.Status != "error" {
		t.Fatalf("state %+v", st)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatalf("local port still busy: %v", err)
	}
	ignoreErr(ln.Close())
}

// sharedClient подключает клиента, которым владеет тест (как подключение хоста), и закрывает его в Cleanup.
func sharedClient(t *testing.T, dial func(ctx context.Context) (*ssh.Client, error)) *ssh.Client {
	t.Helper()
	c, err := dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignoreErr(c.Close()) })
	return c
}

// startSharedRunner — как startRunner, но поверх acquire и NewSharedForwardRunner.
func startSharedRunner(t *testing.T, f model.Forward, acquire func(ctx context.Context) (*ssh.Client, error)) *ForwardRunner {
	t.Helper()
	r := NewSharedForwardRunner(f, acquire)
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.State(); st.Status != "running" {
		t.Fatalf("state after Start = %+v", st)
	}
	return r
}

func TestSharedRunnerDoesNotCloseClient(t *testing.T) {
	_, dial := forwardServer(t)
	client := sharedClient(t, dial)
	acquire := func(context.Context) (*ssh.Client, error) { return client, nil }
	echo := echoServer(t)
	listen1, listen2 := freeAddr(t), freeAddr(t)
	r1 := startSharedRunner(t, model.Forward{Kind: "L", Listen: listen1, Target: echo}, acquire)
	startSharedRunner(t, model.Forward{Kind: "L", Listen: listen2, Target: echo}, acquire)
	dialEcho(t, listen1, "first")
	dialEcho(t, listen2, "second")

	r1.Stop()

	dialEcho(t, listen2, "after stop of the first")
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("shared client was closed: %v", err)
	}
	ln, err := net.Listen("tcp", listen1)
	if err != nil {
		t.Fatalf("port of the stopped runner still busy: %v", err)
	}
	ignoreErr(ln.Close())
}

func TestSharedRunnerReacquiresAfterDrop(t *testing.T) {
	srv, dial := forwardServer(t)
	var (
		mu    sync.Mutex
		calls int
	)
	acquire := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return dial(ctx)
	}
	listen := freeAddr(t)
	r := startSharedRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echoServer(t)}, acquire)
	dialEcho(t, listen, "before drop")
	waitState(t, r, "no conns", func(s ForwardState) bool { return s.Conns == 0 })
	drainEvents(r)

	srv.DropConnections()
	waitEvent(t, r, "starting")
	waitEvent(t, r, "running")
	dialEcho(t, listen, "after reacquire")

	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 2 {
		t.Fatalf("acquire called %d times, want 2", n)
	}
}

func TestSharedRunnerStopRemoteOnFrozenServer(t *testing.T) {
	srv, dial := forwardServer(t)
	client := sharedClient(t, dial)
	waited := make(chan struct{})
	go func() {
		ignoreErr(client.Wait())
		close(waited)
	}()
	acquire := func(context.Context) (*ssh.Client, error) { return client, nil }
	r := startSharedRunner(t, model.Forward{Kind: "R", Listen: freeAddr(t), Target: echoServer(t)}, acquire)

	srv.FreezeConnections() // cancel-tcpip-forward больше не получит ответа
	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(forwardDialTimeout + time.Second):
		srv.DropConnections() // отпускаем зависший Stop, чтобы Cleanup завершился
		<-done
		t.Fatal("Stop takes longer than forwardDialTimeout on a frozen server")
	}
	if st := r.State(); st.Status != "stopped" {
		t.Fatalf("state after Stop = %+v", st)
	}
	select {
	case <-waited:
		t.Fatal("shared client was closed by Stop")
	default:
	}
}

func TestSharedRunnerFinalErrorFromAcquire(t *testing.T) {
	srv, dial := forwardServer(t)
	var (
		mu    sync.Mutex
		calls int
	)
	acquire := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n > 1 {
			return nil, fmt.Errorf("%w: host example", ErrHostKeyChanged)
		}
		return dial(ctx)
	}
	listen := freeAddr(t)
	r := startSharedRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echoServer(t)}, acquire)
	drainEvents(r)

	srv.DropConnections()
	waitEvent(t, r, "error")
	time.Sleep(100 * time.Millisecond) // acquire с нулевой паузой успел бы вызваться ещё много раз
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 2 {
		t.Fatalf("acquire called %d times, want 2", n)
	}
	if st := r.State(); st.Status != "error" {
		t.Fatalf("state %+v", st)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatalf("local port still busy: %v", err)
	}
	ignoreErr(ln.Close())
}

func TestSharedRunnerRejectsTheDroppedClient(t *testing.T) {
	srv, dial := forwardServer(t)
	first := sharedClient(t, dial)
	var (
		mu    sync.Mutex
		calls int
	)
	acquire := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n <= 2 {
			return first, nil // the second call hands back the client that has just dropped
		}
		return dial(ctx)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
	listen := freeAddr(t)
	r := NewSharedForwardRunner(model.Forward{Kind: "L", Listen: listen, Target: echoServer(t)}, acquire)
	r.backoff = func(int) time.Duration { return 300 * time.Millisecond }
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	drainEvents(r)

	srv.DropConnections()
	waitState(t, r, "second acquire", func(ForwardState) bool { return count() >= 2 })
	time.Sleep(100 * time.Millisecond) // with the zero backoff a spin would call acquire many times by now
	if n := count(); n != 2 {
		t.Fatalf("acquire called %d times within the pause, want 2", n)
	}
	waitState(t, r, "running", func(s ForwardState) bool { return s.Status == ForwardRunning })
	if n := count(); n != 3 {
		t.Fatalf("acquire called %d times, want 3", n)
	}
	dialEcho(t, listen, "after the fresh client")
}

func TestSharedRunnerRemoteRefusedBacksOff(t *testing.T) {
	srv, dial := forwardServer(t)
	var (
		mu    sync.Mutex
		calls int
	)
	release := make(chan struct{})
	acquire := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 2 {
			select { // the first acquire after the drop waits until the test has taken the port
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return dial(ctx)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
	listen := freeAddr(t)
	r := startSharedRunner(t, model.Forward{Kind: "R", Listen: listen, Target: echoServer(t)}, acquire)
	drainEvents(r)

	srv.DropConnections()
	waitState(t, r, "acquire after the drop", func(ForwardState) bool { return count() >= 2 })
	// Take the port the server listened on, so it refuses the next tcpip-forward on a live client.
	var ln net.Listener
	deadline := time.Now().Add(forwardWait)
	for {
		var err error
		if ln, err = net.Listen("tcp", listen); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server keeps the remote port: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() { ignoreErr(ln.Close()) })
	close(release)

	waitState(t, r, "refused port", func(s ForwardState) bool { return strings.Contains(s.Error, "Сервер не открыл порт") })
	time.Sleep(time.Second)
	if n := count(); n > 3 {
		t.Fatalf("acquire called %d times within a second of the refusal, want at most 3", n)
	}
}

func TestSharedRunnerNoPauseAfterAcquireError(t *testing.T) {
	srv, dial := forwardServer(t)
	var (
		mu    sync.Mutex
		calls int
		back  time.Time // when the host connection is up again; zero before the drop
	)
	acquire := func(ctx context.Context) (*ssh.Client, error) {
		mu.Lock()
		calls++
		n, up := calls, back
		mu.Unlock()
		if n > 1 && time.Now().Before(up) {
			// Like a host connection: blocks until its next attempt, which fails.
			time.Sleep(50 * time.Millisecond)
			return nil, errors.New("connection refused")
		}
		return dial(ctx)
	}
	listen := freeAddr(t)
	r := startSharedRunner(t, model.Forward{Kind: "L", Listen: listen, Target: echoServer(t)}, acquire)
	drainEvents(r)

	mu.Lock()
	back = time.Now().Add(120 * time.Millisecond) // two failed attempts, then success
	mu.Unlock()
	srv.DropConnections()
	waitState(t, r, "running again", func(s ForwardState) bool {
		return s.Status == ForwardRunning && time.Now().After(back)
	})
	if d := time.Since(back); d > 100*time.Millisecond {
		t.Fatalf("running %v after the host connection came back: the runner paused on its own", d)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 3 {
		t.Fatalf("acquire called %d times, want the failed attempts too", n)
	}
	dialEcho(t, listen, "after the host connection came back")
}

func TestSharedRunnerRemoteRefusedKeepsClient(t *testing.T) {
	_, dial := forwardServer(t)
	client := sharedClient(t, dial)
	acquire := func(context.Context) (*ssh.Client, error) { return client, nil }
	busy, err := net.Listen("tcp", "127.0.0.1:0") // сервер в том же процессе: порт занят и для него
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignoreErr(busy.Close()) })
	r := NewSharedForwardRunner(model.Forward{Kind: "R", Listen: busy.Addr().String(), Target: echoServer(t)}, acquire)
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("Start on a port the server cannot open succeeded")
	}
	if st := r.State(); !strings.Contains(st.Error, "Сервер не открыл порт") {
		t.Fatalf("state %+v", st)
	}
	// Отказ сервера в порте — не обрыв подключения: общий клиент нужен другим пробросам и консолям.
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("shared client was closed after the refusal: %v", err)
	}
	r.Stop()
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("shared client was closed by Stop: %v", err)
	}
}
