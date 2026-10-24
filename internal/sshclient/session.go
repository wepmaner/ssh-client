package sshclient

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

const (
	defaultTerm = "xterm-256color"
	chunkSize   = 32 * 1024
)

// Session — интерактивный shell на сервере с PTY.
type Session struct {
	sess  *ssh.Session
	stdin io.WriteCloser
	out   chan []byte

	done      chan struct{} // закрывается в Close: насос перестаёт ждать читателя Output
	closeOnce sync.Once
}

// NewSession открывает канал, запрашивает PTY размером cols×rows (term "" — xterm-256color) и запускает shell.
func NewSession(client *ssh.Client, cols, rows int, term string) (*Session, error) {
	if term == "" {
		term = defaultTerm
	}
	sess, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("открытие сессии: %w", err)
	}
	s, err := start(sess, cols, rows, term)
	if err != nil {
		_ = sess.Close() //nolint:gosec // G104: best-effort cleanup after the primary error
		return nil, err
	}
	return s, nil
}

func start(sess *ssh.Session, cols, rows int, term string) (*Session, error) {
	stdin, err := sess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("открытие сессии: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("открытие сессии: %w", err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("открытие сессии: %w", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty(term, rows, cols, modes); err != nil {
		return nil, fmt.Errorf("запрос терминала: %w", err)
	}
	if err := sess.Shell(); err != nil {
		return nil, fmt.Errorf("запуск shell: %w", err)
	}

	s := &Session{sess: sess, stdin: stdin, out: make(chan []byte, 16), done: make(chan struct{})}
	var wg sync.WaitGroup
	wg.Add(2)
	go s.pump(stdout, &wg)
	go s.pump(stderr, &wg)
	go func() {
		wg.Wait()
		close(s.out) // единственное место закрытия: после выхода обоих насосов
	}()
	return s, nil
}

// pump копирует r в Output кусками до 32 КиБ до EOF или Close.
func (s *Session) pump(r io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		buf := make([]byte, chunkSize)
		n, err := r.Read(buf)
		if n > 0 {
			select {
			case s.out <- buf[:n]:
			case <-s.done:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// Output — вывод shell (stdout и stderr). Канал закрывается, когда сессия завершилась или закрыта.
func (s *Session) Output() <-chan []byte { return s.out }

// Write отправляет ввод пользователя в shell.
func (s *Session) Write(b []byte) error {
	if _, err := s.stdin.Write(b); err != nil {
		return fmt.Errorf("отправка ввода: %w", err)
	}
	return nil
}

// Resize сообщает серверу новый размер терминала.
func (s *Session) Resize(cols, rows int) error {
	if err := s.sess.WindowChange(rows, cols); err != nil {
		return fmt.Errorf("изменение размера терминала: %w", err)
	}
	return nil
}

// Wait ждёт завершения shell; ненулевой код выхода возвращается как *ssh.ExitError.
func (s *Session) Wait() error { return s.sess.Wait() }

// Close закрывает сессию; повторные вызовы ничего не делают и возвращают nil.
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		if e := s.sess.Close(); e != nil && !errors.Is(e, io.EOF) {
			err = fmt.Errorf("закрытие сессии: %w", e)
		}
	})
	return err
}
