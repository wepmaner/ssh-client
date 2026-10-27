package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/keys"
	"sshclient/internal/model"
)

// Target описывает, куда и под каким пользователем подключаться.
type Target struct {
	Address string
	Port    int
	User    string
	Key     *model.Key // nil — без ключа, только keyboard-interactive и пароль
	Trust   TrustAsker // кто подтверждает неизвестный ключ сервера; nil — вопрос в консоли через Prompter

	// Passphrase — уже известная фраза зашифрованного ключа (необязательно). Dial пробует её
	// первой, без вопроса; неверная или пустая фраза ведёт к обычным вопросам в консоли.
	// Dial затирает этот буфер перед возвратом при любом исходе.
	Passphrase []byte
	// OnPassphrase (необязательно) вызывается не больше одного раза с фразой ключа, если
	// аутентификация прошла и сервер принял этот ключ. Получатель обязан скопировать фразу:
	// после вызова Dial затирает свой буфер.
	OnPassphrase func(p []byte)

	// Password — уже известный пароль (необязательно). Dial пробует его один раз в первом
	// подходящем месте (запрос пароля или первый раунд keyboard-interactive с одним вопросом
	// без эха) и не спрашивает; неверный пароль ведёт к обычным вопросам в консоли.
	// Dial затирает этот буфер перед возвратом при любом исходе.
	Password []byte
	// OnPassword (необязательно) вызывается не больше одного раза, только после успешной
	// аутентификации, с паролем, которым прошёл вход. Получатель обязан скопировать пароль:
	// после вызова Dial затирает свой буфер.
	OnPassword func(p []byte)
}

const (
	dialTimeout       = 10 * time.Second
	passphraseRetries = 3
	promptRetries     = 3

	msgTimeout      = "Сервер не ответил за 10 секунд"
	msgHandshakeCap = "Подключение не завершилось за 10 минут" // общий предел maxHandshakeTime
	msgAuthDenied   = "Доступ запрещён: сервер отклонил ключ и пароль"
)

// handshakeTimeout ограничивает ожидание ответа сервера во время рукопожатия SSH.
// Время, пока пользователь отвечает на вопросы в терминале, не считается. Переменная — для тестов.
var handshakeTimeout = 10 * time.Second

// maxHandshakeTime — общий предел рукопожатия вместе с ответами пользователя, чтобы сервер
// не мог бесконечно держать соединение вопросами. Переменная — для тестов.
var maxHandshakeTime = 10 * time.Minute

// userError — готовое сообщение для пользователя; причина доступна через errors.Is/As.
type userError struct {
	msg   string
	cause error
}

func (e *userError) Error() string { return e.msg }
func (e *userError) Unwrap() error { return e.cause }

// Dial подключается к серверу и проходит аутентификацию: ключ из t.Key, затем keyboard-interactive,
// затем пароль. Доверие неизвестному ключу сервера подтверждает t.Trust (без него — p), остальные
// вопросы (парольная фраза, пароль, вопросы сервера) задаются текстом через p; пока идёт любой вопрос,
// срок ожидания сервера приостановлен. Изменившийся ключ сервера отклоняется (ErrHostKeyChanged),
// отказ доверять — ErrHostNotTrusted, Ctrl+C на любом вопросе прерывает подключение (ErrCancelled),
// молчание сервера дольше 10 секунд даёт ErrTimeout.
func Dial(ctx context.Context, t Target, p *Prompter, kh *KnownHosts) (*ssh.Client, error) {
	defer clear(t.Passphrase)
	defer clear(t.Password)
	addr := net.JoinHostPort(t.Address, strconv.Itoa(t.Port))
	d := &dialState{p: p, remembered: t.Password}
	defer d.forgetSecrets()
	auth, err := d.authMethods(t)
	if err != nil {
		return nil, err
	}

	nd := net.Dialer{Timeout: dialTimeout}
	conn, err := nd.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, dialError(addr, err)
	}
	d.conn = conn

	// Общий предел рукопожатия вместе с вопросами: сервер не может держать соединение бесконечно.
	hctx, cancel := context.WithTimeout(ctx, maxHandshakeTime)
	defer cancel()
	var trust TrustAsker = p
	if t.Trust != nil {
		trust = t.Trust
	}
	cfg := &ssh.ClientConfig{
		User: t.User,
		// Вопрос о доверии снимается и по общему пределу, и по отмене ctx.
		HostKeyCallback: d.hostKeyCallback(kh.Callback(hctx, trust)),
		Auth:            auth,
		AuthCallback:    d.authStep,
	}
	stop := context.AfterFunc(hctx, func() { _ = conn.Close() }) //nolint:gosec // G104: closing aborts the handshake; its error is irrelevant
	d.armDeadline()
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if !stop() { // ctx или общий предел истёк во время рукопожатия и закрыл соединение
		if err == nil {
			_ = c.Close() //nolint:gosec // G104: best-effort cleanup, ctx error is reported
		}
		if ctx.Err() == nil && errors.Is(hctx.Err(), context.DeadlineExceeded) {
			// Сработал общий предел, а не молчание сервера.
			return nil, &userError{msg: msgHandshakeCap, cause: errors.Join(ErrTimeout, ErrHandshakeCap, hctx.Err())}
		}
		if errors.Is(hctx.Err(), context.DeadlineExceeded) {
			return nil, timeoutError(hctx.Err())
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
	if phrase := d.acceptedPassphrase(); phrase != nil && t.OnPassphrase != nil {
		t.OnPassphrase(phrase)
	}
	if pw := d.acceptedPassword(); pw != nil && t.OnPassword != nil {
		t.OnPassword(pw)
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
	phrase     []byte    // фраза, которой открылся ключ; своя копия, затирается в forgetSecrets
	keyFailed  bool      // сервер не принял ключ: publickey среди неудачных методов

	remembered     []byte // пароль из Target (буфер Dial, его затирает Dial); nil после первой попытки
	passKept       []byte // пароль, которым, вероятно, прошёл вход; своя копия, затирается в forgetSecrets
	passwordUntold bool   // password взят из памяти и ещё не подтверждён: первый введённый пароль его заменит
}

// acceptedPassphrase возвращает фразу ключа, если ключ открылся ею и сервер его не отверг.
// Вызывается после успешной аутентификации: тогда ключ принят или дал частичный успех.
func (d *dialState) acceptedPassphrase() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.keyFailed {
		return nil
	}
	return d.phrase
}

// keepPassphrase запоминает фразу, которой открылся ключ; pb переходит во владение d.
func (d *dialState) keepPassphrase(pb []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	clear(d.phrase)
	d.phrase = pb
}

// forgetSecrets затирает копии фразы ключа и пароля.
func (d *dialState) forgetSecrets() {
	d.mu.Lock()
	defer d.mu.Unlock()
	clear(d.phrase)
	d.phrase = nil
	clear(d.passKept)
	d.passKept = nil
}

// acceptedPassword возвращает пароль, которым прошёл вход, или nil. Вызывается после успешной
// аутентификации; буфер принадлежит d и затирается в forgetSecrets.
func (d *dialState) acceptedPassword() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.passKept) == 0 {
		return nil
	}
	return d.passKept
}

// takeRemembered отдаёт запомненный пароль ровно один раз и держит его копию как кандидата.
// Пусто, если пароля нет или его уже пробовали.
func (d *dialState) takeRemembered() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.remembered) == 0 {
		return ""
	}
	pw := string(d.remembered)
	d.remembered = nil
	clear(d.passKept)
	d.passKept = bytes.Clone([]byte(pw))
	d.passwordUntold = true
	return pw
}

// keepPassword запоминает введённый пароль. Первый ответ запоминается; запомненный из памяти
// заменяется один раз, если сервер его не принял и пользователь ввёл другой (replace).
// Позднейшие ответы (например, одноразовый код во втором раунде) не запоминаются.
func (d *dialState) keepPassword(pw string, replace bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.passKept) != 0 && !(replace && d.passwordUntold) {
		return
	}
	clear(d.passKept)
	d.passKept = []byte(pw)
	d.passwordUntold = false
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

func (d *dialState) isCancelled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancelled
}

// authStep вызывается перед каждой попыткой после первой: прерывает аутентификацию после
// отмены и запоминает, что сервер отверг ключ (метод publickey среди неудачных).
func (d *dialState) authStep(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if slices.Contains(ctx.TriedMethods, "publickey") {
		d.keyFailed = true
	}
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
	case isAuthDenied(err):
		return &userError{msg: msgAuthDenied, cause: errors.Join(ErrAuthDenied, err)}
	}
	return fmt.Errorf("подключение к %s: %w", addr, err)
}

// isAuthDenied распознаёт отказ в доступе. x/crypto/ssh не даёт типизированной ошибки, только текст:
// клиент сам исчерпал методы ("unable to authenticate", "too many authentication attempts") или сервер
// разорвал соединение после MaxAuthTries ("Too many authentication failures" у OpenSSH и x/crypto).
func isAuthDenied(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "too many authentication")
}

// authMethods строит методы в порядке: ключ, keyboard-interactive, пароль.
// Ключ разбирается до подключения, чтобы испорченный ключ не тратил попытку соединения.
func (d *dialState) authMethods(t Target) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if t.Key != nil {
		signer, err := keys.Signer(*t.Key, nil)
		switch {
		case err == nil:
			methods = append(methods, ssh.PublicKeys(signer))
		case errors.Is(err, keys.ErrPassphraseRequired):
			methods = append(methods, ssh.PublicKeysCallback(d.passphraseSigners(t.Key, t.Passphrase)))
		default:
			return nil, fmt.Errorf("ключ %s: %w", t.Key.Name, err)
		}
	}
	return append(methods,
		ssh.RetryableAuthMethod(ssh.KeyboardInteractive(d.keyboardInteractive), promptRetries),
		ssh.RetryableAuthMethod(ssh.PasswordCallback(d.password(t)), promptRetries),
	), nil
}

// passphraseSigners открывает ключ фразой cached без вопроса, а если она пуста или неверна,
// спрашивает фразу до трёх раз. cached затирается после первой попытки; фраза, которой ключ
// открылся, хранится только в d до конца Dial.
func (d *dialState) passphraseSigners(k *model.Key, cached []byte) func() ([]ssh.Signer, error) {
	return func() ([]ssh.Signer, error) {
		if len(cached) > 0 {
			signer, err := keys.Signer(*k, cached)
			if err == nil {
				d.keepPassphrase(bytes.Clone(cached))
			}
			clear(cached)
			cached = nil // повторный вызов (после частичного успеха publickey) сразу спрашивает
			if err == nil {
				return []ssh.Signer{signer}, nil
			}
			// Устаревшая фраза стоит одной тихой попытки, дальше обычные вопросы.
			if !errors.Is(err, keys.ErrWrongPassphrase) {
				return nil, fmt.Errorf("ключ %s: %w", k.Name, err)
			}
		}
		for range passphraseRetries {
			var pass string
			err := d.prompting(func() (err error) {
				pass, err = d.p.AskSecret("Парольная фраза ключа " + k.Name + ": ")
				return err
			})
			if err != nil {
				return nil, err
			}
			// Копию фразы затираем сразу; строку pass Go затереть не даёт, её заберёт сборщик мусора.
			pb := []byte(pass)
			signer, err := keys.Signer(*k, pb)
			if err == nil {
				d.keepPassphrase(pb) // затрёт forgetSecrets в конце Dial
				return []ssh.Signer{signer}, nil
			}
			clear(pb)
			// Пустой ответ считается неверной фразой, как и у OpenSSH.
			if !errors.Is(err, keys.ErrWrongPassphrase) && !errors.Is(err, keys.ErrPassphraseRequired) {
				return nil, fmt.Errorf("ключ %s: %w", k.Name, err)
			}
		}
		return nil, fmt.Errorf("ключ %s: неверная парольная фраза", k.Name)
	}
}

func (d *dialState) keyboardInteractive(_, instruction string, questions []string, echos []bool) ([]string, error) {
	printInstruction := func() error {
		if instruction = strings.TrimRight(instruction, "\r\n"); instruction != "" {
			if _, err := io.WriteString(d.p.out, terminalText(instruction)+"\r\n"); err != nil {
				return err
			}
		}
		return nil
	}
	if len(questions) == 0 {
		// Раунд без вопросов: ждать пользователя не нужно, срок ожидания сервера не приостанавливаем.
		if d.isCancelled() {
			return nil, ErrCancelled
		}
		if err := printInstruction(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	answers := make([]string, len(questions))
	if len(questions) == 1 && len(echos) == 1 && !echos[0] {
		if pw := d.takeRemembered(); pw != "" {
			if err := printInstruction(); err != nil {
				return nil, err
			}
			return []string{pw}, nil
		}
	}
	err := d.prompting(func() error {
		if err := printInstruction(); err != nil {
			return err
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
	if len(questions) == 1 && len(echos) == 1 && !echos[0] {
		d.keepPassword(answers[0], false)
	}
	return answers, nil
}

func (d *dialState) password(t Target) func() (string, error) {
	return func() (string, error) {
		if pw := d.takeRemembered(); pw != "" {
			return pw, nil
		}
		var pw string
		err := d.prompting(func() (err error) {
			pw, err = d.p.AskSecret("Пароль " + t.User + "@" + t.Address + ": ")
			return err
		})
		if err == nil {
			d.keepPassword(pw, true)
		}
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
