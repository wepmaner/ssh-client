// Package sshtest — SSH-сервер в процессе для тестов клиента и слоя приложения.
package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Opts настраивает тестовый SSH-сервер.
type Opts struct {
	AcceptKey    ssh.PublicKey // единственный принимаемый публичный ключ; nil — все ключи отклоняются
	Password     string        // принимаемый пароль; "" — вход по паролю не предлагается
	KBIAnswer    string        // ожидаемый ответ keyboard-interactive; "" — метод не предлагается
	MaxAuthTries int           // ServerConfig.MaxAuthTries; 0 — значение x/crypto по умолчанию (6)
	ExitCode     int           // != 0: shell печатает "ok\r\n", сразу завершается с этим кодом и закрывает канал
	NoExitStatus bool          // shell печатает "ok\r\n" и закрывает канал без exit-status; соединение остаётся

	// Exec обслуживает запрос exec: пишет вывод в stdout и stderr и возвращает код выхода.
	// В stop приходят имена сигналов из запросов signal ("TERM"). nil — запрос отклоняется.
	Exec func(cmd string, stdout, stderr io.Writer, stop <-chan string) int
	// SFTPRoot — рабочая папка подсистемы sftp; "" — подсистема отклоняется.
	SFTPRoot string

	OnPty          func(term string, cols, rows uint32) // вызывается на pty-req
	OnWindowChange func(cols, rows uint32)              // вызывается на window-change
}

// Server — SSH-сервер в процессе для тестов клиента; создаётся StartServer.
type Server struct {
	opts    Opts
	addr    string
	hostKey ssh.PublicKey
	wg      sync.WaitGroup
	frozen  atomic.Bool
	ln      net.Listener
	accepts atomic.Int64 // принятые TCP-подключения за жизнь сервера

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

// ChannelHandler обслуживает один входящий канал. Обработчик должен вернуться, когда канал закрыт.
type ChannelHandler func(nc ssh.NewChannel)

// GlobalRequestHandler отвечает на глобальный запрос соединения conn.
type GlobalRequestHandler func(conn *ssh.ServerConn, req *ssh.Request)

// Встроены каналы session и direct-tcpip и запросы tcpip-forward / cancel-tcpip-forward (forward.go).
// Остальное добавляется через RegisterChannelHandler и RegisterGlobalRequestHandler.
// Неизвестные каналы и запросы отклоняются.
var (
	handlersMu      sync.RWMutex
	channelHandlers = map[string]func(s *Server, nc ssh.NewChannel){
		"session":      (*Server).handleSession,
		"direct-tcpip": (*Server).handleDirectTCPIP,
	}
	globalRequestHandlers = map[string]GlobalRequestHandler{}
)

// RegisterChannelHandler добавляет обработчик каналов типа name для всех тестовых серверов.
// Обычно вызывается из init тестового пакета.
func RegisterChannelHandler(name string, h ChannelHandler) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	channelHandlers[name] = func(_ *Server, nc ssh.NewChannel) { h(nc) }
}

// RegisterGlobalRequestHandler добавляет обработчик глобальных запросов типа name.
func RegisterGlobalRequestHandler(name string, h GlobalRequestHandler) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	globalRequestHandlers[name] = h
}

func channelHandler(name string) (func(s *Server, nc ssh.NewChannel), bool) {
	handlersMu.RLock()
	defer handlersMu.RUnlock()
	h, ok := channelHandlers[name]
	return h, ok
}

func globalRequestHandler(name string) (GlobalRequestHandler, bool) {
	handlersMu.RLock()
	defer handlersMu.RUnlock()
	h, ok := globalRequestHandlers[name]
	return h, ok
}

// Start запускает сервер на 127.0.0.1:0 и останавливает его в t.Cleanup.
func Start(t testing.TB, opts Opts) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	s := StartServer(t, opts)
	return s.Addr(), s.HostKey()
}

// StartServer запускает сервер на 127.0.0.1:0, останавливает его в t.Cleanup и возвращает
// управляющий объект (адрес, ключ сервера, обрыв соединений).
func StartServer(t testing.TB, opts Opts) *Server {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{opts: opts, hostKey: signer.PublicKey()}
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
	s.addr = ln.Addr().String()
	s.ln = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepts.Add(1)
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
	return s
}

// Addr — адрес сервера host:port.
func (s *Server) Addr() string { return s.addr }

// HostKey — публичный ключ сервера.
func (s *Server) HostKey() ssh.PublicKey { return s.hostKey }

// Accepted — число TCP-подключений, принятых сервером за всю его жизнь.
func (s *Server) Accepted() int { return int(s.accepts.Load()) }

// StopListening закрывает порт сервера: новые подключения получают отказ, живые остаются.
func (s *Server) StopListening() { ignoreErr(s.ln.Close()) }

// FreezeConnections имитирует зависшее соединение без FIN/RST: сервер перестаёт отвечать на
// глобальные запросы (tcpip-forward, cancel-tcpip-forward и прочие), соединения остаются открытыми.
func (s *Server) FreezeConnections() { s.frozen.Store(true) }

// DropConnections рвёт все живые клиентские соединения, как при обрыве сети.
// Сервер продолжает принимать новые подключения.
func (s *Server) DropConnections() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		ignoreErr(c.Close())
	}
}

// track запоминает соединение для закрытия в Cleanup; false — сервер уже остановлен.
func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns = append(s.conns, c)
	return true
}

func (s *Server) serveConn(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		ignoreErr(c.Close())
		return
	}
	fw := &remoteForwards{s: s, conn: conn, lns: map[string]net.Listener{}}
	defer fw.closeAll()
	defer func() { ignoreErr(conn.Close()) }()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for req := range reqs {
			if s.frozen.Load() {
				continue // зависший сервер: запрос остаётся без ответа, соединение открыто
			}
			if fw.handle(req) {
				continue
			}
			if h, ok := globalRequestHandler(req.Type); ok {
				h(conn, req)
				continue
			}
			if req.WantReply {
				ignoreErr(req.Reply(false, nil))
			}
		}
	}()
	for nc := range chans {
		h, ok := channelHandler(nc.ChannelType())
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
func (s *Server) handleSession(nc ssh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer func() { ignoreErr(ch.Close()) }()
	stop := make(chan string, 4) // имена сигналов для Opts.Exec
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
			if s.opts.NoExitStatus {
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
		case "exec":
			var e struct{ Command string }
			if s.opts.Exec == nil || ssh.Unmarshal(req.Payload, &e) != nil {
				ignoreErr(req.Reply(false, nil))
				continue
			}
			ignoreErr(req.Reply(true, nil))
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				code := s.opts.Exec(e.Command, ch, ch.Stderr(), stop)
				sendExitStatus(ch, code)
				ignoreErr(ch.Close())
			}()
		case "signal":
			var sg struct{ Signal string }
			if ssh.Unmarshal(req.Payload, &sg) == nil {
				select {
				case stop <- sg.Signal:
				default:
				}
			}
			if req.WantReply {
				ignoreErr(req.Reply(true, nil))
			}
		case "subsystem":
			var sub struct{ Name string }
			if s.opts.SFTPRoot == "" || ssh.Unmarshal(req.Payload, &sub) != nil || sub.Name != "sftp" {
				ignoreErr(req.Reply(false, nil))
				continue
			}
			srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.opts.SFTPRoot))
			if err != nil {
				ignoreErr(req.Reply(false, nil))
				continue
			}
			ignoreErr(req.Reply(true, nil))
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				ignoreErr(srv.Serve())
				ignoreErr(srv.Close())
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
