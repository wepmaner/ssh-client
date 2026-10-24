package sshclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testServerOpts настраивает тестовый SSH-сервер.
type testServerOpts struct {
	AcceptKey    ssh.PublicKey // единственный принимаемый публичный ключ; nil — все ключи отклоняются
	Password     string        // принимаемый пароль; "" — вход по паролю не предлагается
	KBIAnswer    string        // ожидаемый ответ keyboard-interactive; "" — метод не предлагается
	MaxAuthTries int           // ServerConfig.MaxAuthTries; 0 — значение x/crypto по умолчанию (6)
	ExitCode     int           // != 0: shell печатает "ok\r\n", сразу завершается с этим кодом и закрывает канал

	OnPty          func(term string, cols, rows uint32) // вызывается на pty-req
	OnWindowChange func(cols, rows uint32)              // вызывается на window-change
}

// testServer — SSH-сервер в процессе для тестов клиента.
type testServer struct {
	opts testServerOpts
	wg   sync.WaitGroup

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

// channelHandler обслуживает один входящий канал.
type channelHandler func(s *testServer, nc ssh.NewChannel)

// channelHandlers — точка расширения по типам каналов: пробросы (direct-tcpip и т. п.) добавляются сюда.
var channelHandlers = map[string]channelHandler{
	"session": (*testServer).handleSession,
}

// globalRequestHandlers — точка расширения для глобальных запросов (tcpip-forward и т. п.).
// Неизвестные запросы отклоняются.
var globalRequestHandlers = map[string]func(s *testServer, conn *ssh.ServerConn, req *ssh.Request){}

// startTestServer запускает сервер на 127.0.0.1:0 и останавливает его в t.Cleanup.
func startTestServer(t *testing.T, opts testServerOpts) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	s := &testServer{opts: opts}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if opts.AcceptKey != nil && bytes.Equal(key.Marshal(), opts.AcceptKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("ключ не принят")
		},
	}
	if opts.Password != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == opts.Password {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("неверный пароль")
		}
	}
	if opts.KBIAnswer != "" {
		cfg.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, ask ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := ask("", "Введите одноразовый код", []string{"Код: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(answers) == 1 && answers[0] == opts.KBIAnswer {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("неверный код")
		}
	}
	cfg.MaxAuthTries = opts.MaxAuthTries
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if !s.track(c) {
				ignoreErr(c.Close())
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serveConn(c, cfg)
			}()
		}
	}()
	t.Cleanup(func() {
		ignoreErr(ln.Close())
		s.mu.Lock()
		for _, c := range s.conns {
			ignoreErr(c.Close())
		}
		s.conns = nil
		s.closed = true
		s.mu.Unlock()
		s.wg.Wait()
	})
	return ln.Addr().String(), signer.PublicKey()
}

// track запоминает соединение для закрытия в Cleanup; false — сервер уже остановлен.
func (s *testServer) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns = append(s.conns, c)
	return true
}

func (s *testServer) serveConn(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		ignoreErr(c.Close())
		return
	}
	defer func() { ignoreErr(conn.Close()) }()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for req := range reqs {
			if h, ok := globalRequestHandlers[req.Type]; ok {
				h(s, conn, req)
				continue
			}
			if req.WantReply {
				ignoreErr(req.Reply(false, nil))
			}
		}
	}()
	for nc := range chans {
		h, ok := channelHandlers[nc.ChannelType()]
		if !ok {
			ignoreErr(nc.Reject(ssh.UnknownChannelType, "неизвестный тип канала"))
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			h(s, nc)
		}()
	}
}

// handleSession — канал session с эхо-shell.
func (s *testServer) handleSession(nc ssh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer func() { ignoreErr(ch.Close()) }()
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p struct {
				Term                 string
				Cols, Rows, PxW, PxH uint32
				Modes                string
			}
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				ignoreErr(req.Reply(false, nil))
				continue
			}
			if s.opts.OnPty != nil {
				s.opts.OnPty(p.Term, p.Cols, p.Rows)
			}
			ignoreErr(req.Reply(true, nil))
		case "window-change":
			var w struct{ Cols, Rows, PxW, PxH uint32 }
			if err := ssh.Unmarshal(req.Payload, &w); err == nil && s.opts.OnWindowChange != nil {
				s.opts.OnWindowChange(w.Cols, w.Rows)
			}
			if req.WantReply {
				ignoreErr(req.Reply(true, nil))
			}
		case "shell":
			ignoreErr(req.Reply(true, nil))
			if _, err := io.WriteString(ch, "ok\r\n"); err != nil {
				return
			}
			if s.opts.ExitCode != 0 {
				sendExitStatus(ch, s.opts.ExitCode)
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_, err := io.Copy(ch, ch) // эхо до EOF от клиента
				ignoreErr(err)
				sendExitStatus(ch, 0)
				ignoreErr(ch.Close())
			}()
		default: // env и прочее игнорируем
			if req.WantReply {
				ignoreErr(req.Reply(false, nil))
			}
		}
	}
}

func sendExitStatus(ch ssh.Channel, code int) {
	if code < 0 {
		panic(fmt.Sprintf("отрицательный код выхода %d", code))
	}
	_, err := ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)})) //nolint:gosec // G115: code checked above
	ignoreErr(err)
}

// ignoreErr явно отбрасывает ошибку закрытия или ответа в тестовом сервере: проверять её нечем и незачем.
func ignoreErr(error) {}
