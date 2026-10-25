package sshtest

import (
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Пробросы тестового сервера (RFC 4254 §7): канал direct-tcpip для -L/-D и запросы
// tcpip-forward / cancel-tcpip-forward для -R. Сервер слушает и подключается на той же машине.

// tcpipMsg — полезная нагрузка direct-tcpip и forwarded-tcpip.
type tcpipMsg struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

// forwardMsg — полезная нагрузка tcpip-forward и cancel-tcpip-forward.
type forwardMsg struct {
	Addr string
	Port uint32
}

// handleDirectTCPIP подключается к запрошенной цели и копирует данные в обе стороны.
func (s *Server) handleDirectTCPIP(nc ssh.NewChannel) {
	var m tcpipMsg
	if err := ssh.Unmarshal(nc.ExtraData(), &m); err != nil {
		ignoreErr(nc.Reject(ssh.ConnectionFailed, "неверный запрос direct-tcpip"))
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(m.Addr, strconv.FormatUint(uint64(m.Port), 10)))
	if err != nil {
		ignoreErr(nc.Reject(ssh.ConnectionFailed, err.Error()))
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		ignoreErr(target.Close())
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ssh.DiscardRequests(reqs)
	}()
	s.pipe(ch, target)
}

// pipe копирует данные между каналом и TCP-соединением, пока одна из сторон не закроется.
func (s *Server) pipe(ch ssh.Channel, c net.Conn) {
	done := make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_, err := io.Copy(ch, c)
		ignoreErr(err)
		ignoreErr(ch.CloseWrite())
		close(done)
	}()
	_, err := io.Copy(c, ch)
	ignoreErr(err)
	ignoreErr(c.Close())
	<-done
	ignoreErr(ch.Close())
}

// remoteForwards — слушатели tcpip-forward одного клиентского соединения.
type remoteForwards struct {
	s    *Server
	conn *ssh.ServerConn

	mu     sync.Mutex
	lns    map[string]net.Listener // ключ — запрошенный адрес host:port (порт уже фактический)
	closed bool
}

// handle обслуживает tcpip-forward и cancel-tcpip-forward; false — запрос другого типа.
func (f *remoteForwards) handle(req *ssh.Request) bool {
	switch req.Type {
	case "tcpip-forward":
		f.listen(req)
	case "cancel-tcpip-forward":
		f.cancel(req)
	default:
		return false
	}
	return true
}

func (f *remoteForwards) listen(req *ssh.Request) {
	var m forwardMsg
	if err := ssh.Unmarshal(req.Payload, &m); err != nil {
		ignoreErr(req.Reply(false, nil))
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(m.Addr, strconv.FormatUint(uint64(m.Port), 10)))
	if err != nil {
		ignoreErr(req.Reply(false, nil))
		return
	}
	port := uint32(ln.Addr().(*net.TCPAddr).Port) //nolint:gosec // G115: TCP port fits in uint32
	key := net.JoinHostPort(m.Addr, strconv.FormatUint(uint64(port), 10))
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		ignoreErr(ln.Close())
		ignoreErr(req.Reply(false, nil))
		return
	}
	f.lns[key] = ln
	f.mu.Unlock()

	var reply []byte
	if m.Port == 0 { // RFC 4254 §7.1: сервер сообщает выделенный порт
		reply = ssh.Marshal(struct{ Port uint32 }{port})
	}
	ignoreErr(req.Reply(true, reply))

	f.s.wg.Add(1)
	go func() {
		defer f.s.wg.Done()
		f.accept(ln, m.Addr, port)
	}()
}

// accept открывает клиенту канал forwarded-tcpip на каждое принятое соединение.
func (f *remoteForwards) accept(ln net.Listener, addr string, port uint32) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		f.s.wg.Add(1)
		go func() {
			defer f.s.wg.Done()
			origin := c.RemoteAddr().(*net.TCPAddr)
			ch, reqs, err := f.conn.OpenChannel("forwarded-tcpip", ssh.Marshal(tcpipMsg{
				Addr: addr, Port: port,
				OriginAddr: origin.IP.String(), OriginPort: uint32(origin.Port), //nolint:gosec // G115: TCP port fits in uint32
			}))
			if err != nil {
				ignoreErr(c.Close())
				return
			}
			f.s.wg.Add(1)
			go func() {
				defer f.s.wg.Done()
				ssh.DiscardRequests(reqs)
			}()
			f.s.pipe(ch, c)
		}()
	}
}

func (f *remoteForwards) cancel(req *ssh.Request) {
	var m forwardMsg
	if err := ssh.Unmarshal(req.Payload, &m); err != nil {
		ignoreErr(req.Reply(false, nil))
		return
	}
	key := net.JoinHostPort(m.Addr, strconv.FormatUint(uint64(m.Port), 10))
	f.mu.Lock()
	ln, ok := f.lns[key]
	delete(f.lns, key)
	f.mu.Unlock()
	if ok {
		ignoreErr(ln.Close())
	}
	ignoreErr(req.Reply(ok, nil))
}

// closeAll закрывает все слушатели соединения; вызывается, когда соединение закончилось.
func (f *remoteForwards) closeAll() {
	f.mu.Lock()
	lns := f.lns
	f.lns = nil
	f.closed = true
	f.mu.Unlock()
	for _, ln := range lns {
		ignoreErr(ln.Close())
	}
}
