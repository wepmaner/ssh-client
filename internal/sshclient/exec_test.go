package sshclient

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/sshclient/sshtest"
)

// execClient подключается к тестовому серверу с заданными опциями; клиент закрывается в Cleanup.
func execClient(t *testing.T, opts sshtest.Opts) (*sshtest.Server, *ssh.Client) {
	t.Helper()
	key, pub := testClientKey(t, "work", "")
	opts.AcceptKey = pub
	srv := sshtest.StartServer(t, opts)
	kh := trustedKnownHosts(t, srv.Addr(), srv.HostKey())
	target := targetFor(t, srv.Addr(), key)
	c, err := Dial(context.Background(), target, NewPrompter(io.Discard, strings.NewReader("")), kh)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return srv, c
}

func TestExecStreamsAndExitCode(t *testing.T) {
	_, c := execClient(t, sshtest.Opts{Exec: func(cmd string, stdout, stderr io.Writer, _ <-chan string) int {
		_, _ = io.WriteString(stdout, "out:"+cmd+"\n")
		_, _ = io.WriteString(stderr, "err\n")
		return 0
	}})
	res, err := Exec(context.Background(), c, "hello", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out:hello\n" || res.Stderr != "err\n" || res.ExitCode != 0 || res.Truncated || res.TimedOut {
		t.Fatalf("неожиданный результат: %+v", res)
	}
}

func TestExecNonZeroExitIsNotError(t *testing.T) {
	_, c := execClient(t, sshtest.Opts{Exec: func(string, io.Writer, io.Writer, <-chan string) int { return 7 }})
	res, err := Exec(context.Background(), c, "false", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("код выхода %d, ожидался 7", res.ExitCode)
	}
}

func TestExecKeepsTail(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteByte(byte('a' + i%26))
	}
	all := sb.String()
	_, c := execClient(t, sshtest.Opts{Exec: func(_ string, stdout, _ io.Writer, _ <-chan string) int {
		// несколькими записями, чтобы буфер обрезался не за один раз
		for i := 0; i < 100; i += 7 {
			_, _ = io.WriteString(stdout, all[i:min(i+7, 100)])
		}
		return 0
	}})
	res, err := Exec(context.Background(), c, "x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Stdout != all[90:] {
		t.Fatalf("Truncated=%v Stdout=%q, ожидалось %q", res.Truncated, res.Stdout, all[90:])
	}
}

func TestExecInvalidUTF8(t *testing.T) {
	_, c := execClient(t, sshtest.Opts{Exec: func(cmd string, stdout, stderr io.Writer, _ <-chan string) int {
		if cmd == "bad" {
			_, _ = stdout.Write([]byte{'a', 0xff, 'b'})
		} else {
			_, _ = io.WriteString(stdout, "ééééé") // 10 байт; хвост в 5 байт начинается посреди руны
		}
		return 0
	}})
	res, err := Exec(context.Background(), c, "bad", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(res.Stdout) || !strings.Contains(res.Stdout, "�") {
		t.Fatalf("stdout %q не заменяет неверный байт", res.Stdout)
	}
	res, err = Exec(context.Background(), c, "cut", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || !utf8.ValidString(res.Stdout) {
		t.Fatalf("обрез посреди руны: %q, Truncated=%v", res.Stdout, res.Truncated)
	}
}

func TestExecTimeoutSignalsThenCloses(t *testing.T) {
	got := make(chan string, 1)
	_, c := execClient(t, sshtest.Opts{Exec: func(_ string, stdout, _ io.Writer, stop <-chan string) int {
		_, _ = io.WriteString(stdout, "partial")
		got <- <-stop
		return 143
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Exec(ctx, c, "sleep", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("вызов занял %v", time.Since(start))
	}
	if !res.TimedOut || res.Stdout != "partial" {
		t.Fatalf("неожиданный результат: %+v", res)
	}
	if sig := <-got; sig != "TERM" {
		t.Fatalf("сервер получил сигнал %q, ожидался TERM", sig)
	}
	// общий клиент остаётся рабочим
	s, err := c.NewSession()
	if err != nil {
		t.Fatalf("клиент закрыт после Exec: %v", err)
	}
	_ = s.Close()
}

func TestExecTimeoutClosesWhenSignalIgnored(t *testing.T) {
	release := make(chan struct{})
	_, c := execClient(t, sshtest.Opts{Exec: func(string, io.Writer, io.Writer, <-chan string) int {
		<-release // сигнал игнорируется
		return 0
	}})
	t.Cleanup(func() { close(release) }) // после Cleanup сервера (порядок LIFO): иначе сервер ждёт обработчик
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Exec(ctx, c, "stubborn", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || time.Since(start) > 3*time.Second {
		t.Fatalf("TimedOut=%v за %v", res.TimedOut, time.Since(start))
	}
}

func TestExecCancelReturnsCanceledWithPartialOutput(t *testing.T) {
	started := make(chan struct{})
	_, c := execClient(t, sshtest.Opts{Exec: func(_ string, stdout, _ io.Writer, stop <-chan string) int {
		_, _ = io.WriteString(stdout, "part")
		close(started)
		<-stop
		return 143
	}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	res, err := Exec(ctx, c, "x", 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка %v, ожидалась context.Canceled", err)
	}
	if res.Stdout != "part" || res.TimedOut {
		t.Fatalf("неожиданный результат: %+v", res)
	}
}
