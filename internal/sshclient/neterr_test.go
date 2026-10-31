package sshclient

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

// opError is the shape net.Dialer returns: dial tcp → connectex (or connect) → errno.
func opError(errno error) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connectex", Err: errno}}
}

func TestDialErrorTexts(t *testing.T) {
	const addr = "example.test:2222"
	cases := []struct {
		name  string
		err   error
		want  string
		isErr error
	}{
		{"refused", opError(errRefused), "Сервер example.test отклонил подключение к порту 2222: SSH на нём не запущен или порт закрыт", nil},
		{"timeout", opError(errTimedOut), msgTimeout, ErrTimeout},
		{"host unreachable", opError(errHostUnreachable), "Нет маршрута до example.test: сеть недоступна или адрес вне вашей сети", nil},
		{"network unreachable", opError(errNetUnreachable), "Нет маршрута до example.test: сеть недоступна или адрес вне вашей сети", nil},
		{"dns not found", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "example.test", IsNotFound: true}},
			"Адрес example.test не найден: проверьте имя хоста или подключение к сети", nil},
		{"dns failure", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "server misbehaving", Name: "example.test", IsTemporary: true}},
			"Не удалось узнать адрес example.test: нет связи с DNS-сервером", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := dialError(addr, c.err)
			if err.Error() != c.want {
				t.Fatalf("message %q, want %q", err.Error(), c.want)
			}
			if !errors.Is(err, c.err) {
				t.Fatal("the original error is not kept for the details")
			}
			if c.isErr != nil && !errors.Is(err, c.isErr) {
				t.Fatalf("not %v", c.isErr)
			}
		})
	}
}

func TestDialErrorOtherKeepsCause(t *testing.T) {
	cause := errors.New("something odd")
	err := dialError("h:22", cause)
	if err.Error() != "подключение к h:22: something odd" {
		t.Fatalf("message %q", err.Error())
	}
}

func TestDialRefusedPortInRussian(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Dial(context.Background(), targetFor(t, addr, nil), NewPrompter(io.Discard, strings.NewReader("")), trustedKnownHosts(t, addr, testKey(t)))
	if err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if !strings.Contains(err.Error(), "отклонил подключение") {
		t.Fatalf("message %q", err.Error())
	}
	if !strings.Contains(errorChain(err), "refused") && !strings.Contains(errorChain(err), "connectex") {
		t.Fatalf("the system text is lost: %q", errorChain(err))
	}
}

// errorChain joins the messages of err and its causes, as the details on the error card do.
func errorChain(err error) string {
	var b strings.Builder
	for e := err; e != nil; e = errors.Unwrap(e) {
		b.WriteString(e.Error() + "\n")
	}
	return b.String()
}

func TestForwardStateKeepsSystemText(t *testing.T) {
	sys := opError(errRefused)
	refused := func(context.Context) (*ssh.Client, error) { return nil, dialError("example.test:22", sys) }
	r := NewForwardRunner(model.Forward{Kind: "L", Listen: freeAddr(t), Target: "127.0.0.1:1"}, refused)
	t.Cleanup(r.Stop)
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded with a refused dial")
	}
	st := r.State()
	if !strings.Contains(st.Error, "отклонил подключение") {
		t.Fatalf("error %q", st.Error)
	}
	if st.Detail != sys.Error() {
		t.Fatalf("detail %q, want the system text %q", st.Detail, sys.Error())
	}
}

func TestListenErrorTexts(t *testing.T) {
	bind := func(errno error) error {
		return &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: errno}}
	}
	cases := []struct {
		name string
		addr string
		err  error
		want string
	}{
		{"in use", "127.0.0.1:13001", bind(errAddrInUse), "Порт 13001 уже занят другой программой: выберите другой порт или закройте программу, которая его держит"},
		{"in use, other address", "0.0.0.0:13001", bind(errAddrInUse), "Порт 0.0.0.0:13001 уже занят другой программой: выберите другой порт или закройте программу, которая его держит"},
		{"reserved", "127.0.0.1:13001", bind(errAccess), "Windows не даёт занять порт 13001: он зарезервирован системой (часто Hyper-V или WSL), выберите другой порт"},
		{"foreign address", "10.0.0.5:13001", bind(errAddrNotAvail), "Адрес 10.0.0.5 не принадлежит этому компьютеру: укажите 127.0.0.1 или адрес своей сети"},
		{"other", "127.0.0.1:13001", bind(io.ErrUnexpectedEOF), "Не удалось занять локальный порт 13001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ListenError(c.addr, c.err)
			if got.Error() != c.want {
				t.Fatalf("text = %q, want %q", got, c.want)
			}
			if !errors.Is(got, c.err) {
				t.Fatalf("cause lost: %v", got)
			}
		})
	}
}
