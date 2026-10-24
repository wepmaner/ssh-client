package sshclient

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

// Target описывает, куда и под каким пользователем подключаться.
type Target struct {
	Address string
	Port    int
	User    string
	Key     *model.Key // nil — без ключа, только keyboard-interactive и пароль
}

const (
	dialTimeout       = 10 * time.Second
	passphraseRetries = 3
	promptRetries     = 3

	msgTimeout    = "Сервер не ответил за 10 секунд"
	msgAuthDenied = "Доступ запрещён: сервер отклонил ключ и пароль"
)

// handshakeTimeout ограничивает ожидание ответа сервера во время рукопожатия SSH.
// Время, пока пользователь отвечает на вопросы в терминале, не считается. Переменная — для тестов.
var handshakeTimeout = 10 * time.Second

// userError — готовое сообщение для пользователя; причина доступна через errors.Is/As.
type userError struct {
	msg   string
	cause error
}

func (e *userError) Error() string { return e.msg }
func (e *userError) Unwrap() error { return e.cause }

// Dial подключается к серверу и проходит аутентификацию: ключ из t.Key, затем keyboard-interactive,
// затем пароль. Все вопросы (доверие ключу сервера, парольная фраза, пароль, вопросы сервера) задаются
// текстом через p. Изменившийся ключ сервера отклоняется (ErrHostKeyChanged), Ctrl+C на любом вопросе
// прерывает подключение (ErrCancelled), молчание сервера дольше 10 секунд даёт ErrTimeout.
func Dial(ctx context.Context, t Target, p *Prompter, kh *KnownHosts) (*ssh.Client, error) {
	addr := net.JoinHostPort(t.Address, strconv.Itoa(t.Port))
	d := &dialState{p: p}
	auth, err := d.authMethods(t)
	if err != nil {
		return nil, err
	}

	nd := net.Dialer{Timeout: dialTimeout}
	conn, err := nd.DialContext(ctx, "tcp", addr)
	if err != nil {
		if isTimeout(err) {
			return nil, timeoutError(err)
		}
		return nil, fmt.Errorf("подключение к %s: %w", addr, err)
	}
	d.conn = conn

	cfg := &ssh.ClientConfig{
		User:            t.User,
		HostKeyCallback: d.hostKeyCallback(kh.Callback(p)),
		Auth:            auth,
		AuthCallback:    d.abortIfCancelled,
		Timeout:         dialTimeout,
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) //nolint:gosec // G104: closing aborts the handshake; its error is irrelevant
	d.armDeadline()
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if !stop() { // ctx завершился во время рукопожатия и закрыл соединение
		if err == nil {
			_ = c.Close() //nolint:gosec // G104: best-effort cleanup, ctx error is reported
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, timeoutError(ctx.Err())
		}
		return nil, fmt.Errorf("подключение к %s: %w", addr, ctx.Err())
	}
	if err != nil {
		_ = conn.Close() //nolint:gosec // G104: best-effort cleanup after the primary error
		return nil, d.classify(addr, err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = c.Close() //nolint:gosec // G104: best-effort cleanup after the primary error
		return nil, fmt.Errorf("подключение к %s: %w", addr, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func timeoutError(cause error) error {
	return &userError{msg: msgTimeout, cause: errors.Join(ErrTimeout, cause)}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(err, &ne) && ne.Timeout())
}

// dialState — состояние одного подключения, общее для обратных вызовов ssh.
type dialState struct {
	p    *Prompter
	conn net.Conn

	mu         sync.Mutex
	deadline   time.Time // текущий срок ожидания сервера; ноль — идёт вопрос пользователю
	cancelled  bool      // пользователь отменил ввод: остальные методы не пробовать
	hostKeyErr error     // отказ проверки ключа сервера, вернуть как есть
}

// armDeadline заново отсчитывает handshakeTimeout ожидания сервера.
func (d *dialState) armDeadline() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deadline = time.Now().Add(handshakeTimeout)
	_ = d.conn.SetDeadline(d.deadline) //nolint:gosec // G104: a failed deadline surfaces as the next read error
}

// prompting выполняет f, пока срок ожидания сервера приостановлен: пользователь может думать сколько угодно.
func (d *dialState) prompting(f func() error) error {
	d.mu.Lock()
	if d.cancelled {
		d.mu.Unlock()
		return ErrCancelled
	}
	d.deadline = time.Time{}
	_ = d.conn.SetDeadline(time.Time{}) //nolint:gosec // G104: a failed deadline surfaces as the next read error
	d.mu.Unlock()

	err := f()

	d.mu.Lock()
	if errors.Is(err, ErrCancelled) {
		d.cancelled = true
	}
	d.mu.Unlock()
	d.armDeadline()
	return err
}

func (d *dialState) abortIfCancelled(*ssh.ClientAuthContext) (ssh.AuthMethod, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancelled {
		return nil, ErrCancelled
	}
	return nil, nil
}

func (d *dialState) hostKeyCallback(check ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := d.prompting(func() error { return check(hostname, remote, key) })
		if err != nil {
			d.mu.Lock()
			d.hostKeyErr = err
			d.mu.Unlock()
		}
		return err
	}
}

// classify переводит ошибку рукопожатия в понятную пользователю.
func (d *dialState) classify(addr string, err error) error {
	d.mu.Lock()
	cancelled, hostKeyErr, deadline := d.cancelled, d.hostKeyErr, d.deadline
	d.mu.Unlock()
	switch {
	case cancelled:
		return ErrCancelled
	case hostKeyErr != nil:
		return hostKeyErr
	case isTimeout(err) || (!deadline.IsZero() && !time.Now().Before(deadline)):
		return timeoutError(err)
	case strings.Contains(err.Error(), "unable to authenticate") ||
		strings.Contains(err.Error(), "too many authentication attempts"):
		// x/crypto/ssh не даёт типизированной ошибки отказа в доступе, только текст.
		return &userError{msg: msgAuthDenied, cause: err}
	}
	return fmt.Errorf("подключение к %s: %w", addr, err)
}

// authMethods строит методы в порядке: ключ, keyboard-interactive, пароль.
// Ключ разбирается до подключения, чтобы испорченный ключ не тратил попытку соединения.
func (d *dialState) authMethods(t Target) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if t.Key != nil {
		signer, err := ssh.ParsePrivateKey(t.Key.PrivatePEM)
		var missing *ssh.PassphraseMissingError
		switch {
		case err == nil:
			methods = append(methods, ssh.PublicKeys(signer))
		case errors.As(err, &missing):
			methods = append(methods, ssh.PublicKeysCallback(d.passphraseSigners(t.Key)))
		default:
			return nil, fmt.Errorf("ключ %s: %w", t.Key.Name, err)
		}
	}
	return append(methods,
		ssh.RetryableAuthMethod(ssh.KeyboardInteractive(d.keyboardInteractive), promptRetries),
		ssh.RetryableAuthMethod(ssh.PasswordCallback(d.password(t)), promptRetries),
	), nil
}

// passphraseSigners спрашивает парольную фразу ключа до трёх раз; фраза нигде не сохраняется.
func (d *dialState) passphraseSigners(k *model.Key) func() ([]ssh.Signer, error) {
	return func() ([]ssh.Signer, error) {
		for range passphraseRetries {
			var pass string
			err := d.prompting(func() (err error) {
				pass, err = d.p.AskSecret("Парольная фраза ключа " + k.Name + ": ")
				return err
			})
			if err != nil {
				return nil, err
			}
			signer, err := ssh.ParsePrivateKeyWithPassphrase(k.PrivatePEM, []byte(pass))
			if err == nil {
				return []ssh.Signer{signer}, nil
			}
			if !errors.Is(err, x509.IncorrectPasswordError) {
				return nil, fmt.Errorf("ключ %s: %w", k.Name, err)
			}
		}
		return nil, fmt.Errorf("ключ %s: неверная парольная фраза", k.Name)
	}
}

func (d *dialState) keyboardInteractive(_, instruction string, questions []string, echos []bool) ([]string, error) {
	answers := make([]string, len(questions))
	err := d.prompting(func() error {
		if instruction = strings.TrimRight(instruction, "\r\n"); instruction != "" {
			if _, err := io.WriteString(d.p.out, terminalText(instruction)+"\r\n"); err != nil {
				return err
			}
		}
		for i, q := range questions {
			ask := d.p.AskSecret
			if i < len(echos) && echos[i] {
				ask = d.p.Ask
			}
			a, err := ask(terminalText(q))
			if err != nil {
				return err
			}
			answers[i] = a
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return answers, nil
}

func (d *dialState) password(t Target) func() (string, error) {
	return func() (string, error) {
		var pw string
		err := d.prompting(func() (err error) {
			pw, err = d.p.AskSecret("Пароль " + t.User + "@" + t.Address + ": ")
			return err
		})
		return pw, err
	}
}

// terminalText готовит текст сервера к выводу в «сырой» терминал: убирает управляющие символы
// (сервер не должен управлять терминалом через вопросы) и переводит \n в \r\n.
func terminalText(s string) string {
	var b strings.Builder
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		switch {
		case r == '\n':
			b.WriteString("\r\n")
		case r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
