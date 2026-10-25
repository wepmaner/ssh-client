package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

// Статусы проброса в ForwardState.Status.
const (
	ForwardStopped  = "stopped"
	ForwardStarting = "starting"
	ForwardRunning  = "running"
	ForwardError    = "error"
)

const (
	forwardEventBuffer = 16
	forwardMaxBackoff  = 60 * time.Second
	forwardDialTimeout = 10 * time.Second
)

var (
	// ErrForwardStopped — Start после Stop: раннер одноразовый.
	ErrForwardStopped = errors.New("проброс остановлен")
	// ErrForwardStarted — повторный Start работающего раннера.
	ErrForwardStarted = errors.New("проброс уже запущен")

	errReconnecting = errors.New("нет соединения с сервером, идёт переподключение")
)

// ForwardState — состояние проброса для интерфейса.
type ForwardState struct {
	Status string `json:"status"` // stopped | starting | running | error
	Error  string `json:"error"`  // причина error; во время переподключения — последняя ошибка подключения
	Detail string `json:"detail"` // системный текст под Error (connectex, lookup …); пусто, если нечего добавить
	Conns  int    `json:"conns"`  // живые проксируемые соединения
}

// ForwardRunner держит один проброс (L, R или D) поверх своего SSH-соединения.
//
// Жизненный цикл: NewForwardRunner → Start → … → Stop. Раннер одноразовый: после Stop (или после
// отмены ctx, переданного в Start) Start возвращает ошибку, нужен новый раннер. Повторить Start можно
// только после неудачного Start.
//
// Обрыв SSH-соединения (client.Wait вернулся) переводит раннер в starting, и dial повторяется
// через 2, 4, 8 … 60 с до успеха, Stop, отмены ctx или отказа доверять ключу сервера
// (ErrHostNotTrusted: статус error окончательный, порт освобождается). Локальный порт L/D при этом остаётся занят;
// новые соединения на него во время переподключения сразу закрываются (fail fast), уже открытые
// соединения рвутся вместе со старым клиентом. Для R после переподключения порт на сервере
// запрашивается заново.
type ForwardRunner struct {
	f       model.Forward
	dial    func(ctx context.Context) (*ssh.Client, error)
	backoff func(attempt int) time.Duration // задержка перед попыткой attempt (с 1); подменяется в тестах
	socks   *socks5.Server                  // только для D

	stopOnce sync.Once
	wg       sync.WaitGroup // Start в процессе и все горутины раннера

	mu           sync.Mutex
	state        ForwardState
	events       chan ForwardState // сам канал не меняется; закрывается в Stop
	eventsClosed bool
	started      bool            // Start успешно завершился
	stopped      bool            // вызван Stop
	closing      bool            // ресурсы закрыты (Stop или отмена ctx), новые не устанавливаются
	failed       bool            // локальный слушатель сломался: статус error окончательный, переподключения нет
	ctx          context.Context // контекст работы после успешного Start; для SOCKS-подключений
	cancel       context.CancelFunc
	ln           net.Listener // локальный слушатель L/D
	client       *ssh.Client  // nil во время переподключения
	rln          net.Listener // слушатель на сервере для R
	conns        map[net.Conn]struct{}
}

// NewForwardRunner создаёт остановленный раннер. dial вызывается при Start и при каждом
// переподключении; возвращённый клиент принадлежит раннеру и закрывается им.
//
// Контракт dial: при отмене ctx он должен быстро вернуться (Stop отменяет ctx и ждёт все
// вызовы dial). Интерактивные вопросы внутри dial (парольная фраза, пароль, доверие ключу
// сервера) тоже обязаны следить за ctx, иначе Stop будет ждать ответа пользователя.
// Переподключение вызывает dial без участия пользователя.
func NewForwardRunner(f model.Forward, dial func(ctx context.Context) (*ssh.Client, error)) *ForwardRunner {
	r := &ForwardRunner{
		f:       f,
		dial:    dial,
		backoff: defaultBackoff,
		state:   ForwardState{Status: ForwardStopped},
		events:  make(chan ForwardState, forwardEventBuffer),
		conns:   map[net.Conn]struct{}{},
	}
	if f.Kind == "D" {
		r.socks = socks5.NewServer(
			socks5.WithDial(r.socksDial),
			socks5.WithResolver(remoteResolver{}),
			socks5.WithAssociateHandle(rejectSocksCommand),
			socks5.WithGPool(wgPool{&r.wg}),
			socks5.WithLogger(socks5.NewLogger(log.New(io.Discard, "", 0))),
		)
	}
	return r
}

// defaultBackoff: 2, 4, 8, 16, 32, затем 60 с.
func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		return forwardMaxBackoff
	}
	return min(2*time.Second<<(attempt-1), forwardMaxBackoff)
}

// State возвращает текущее состояние.
func (r *ForwardRunner) State() ForwardState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Events отдаёт каждое изменение состояния. Канал буферизован и никогда не блокирует раннер:
// при переполнении выбрасывается самое старое событие, так что последнее состояние всегда
// доходит. Канал закрывается, когда Stop завершился.
func (r *ForwardRunner) Events() <-chan ForwardState { return r.events }

// Start занимает локальный порт (L, D) или подключается и открывает порт на сервере (R).
// Ошибка возвращается сразу: занятый порт (текст содержит адрес с портом), неудачный dial,
// отказ сервера открыть порт. После успеха проброс работает в фоне до Stop или отмены ctx.
func (r *ForwardRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	switch {
	case r.stopped:
		r.mu.Unlock()
		return ErrForwardStopped
	case r.started || r.state.Status == ForwardStarting:
		r.mu.Unlock()
		return ErrForwardStarted
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	r.setLocked(ForwardState{Status: ForwardStarting})
	r.mu.Unlock()
	defer r.wg.Done()

	ln, client, rln, err := r.open(runCtx)
	if err != nil {
		cancel()
		r.mu.Lock()
		r.setLocked(ForwardState{Status: ForwardError, Error: err.Error(), Detail: errorCauses(err)})
		r.mu.Unlock()
		return err
	}

	r.mu.Lock()
	if r.stopped || runCtx.Err() != nil { // Stop или отмена ctx пришли во время подключения
		r.mu.Unlock()
		cancel()
		closeAll(ln, rln, client)
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrForwardStopped
	}
	r.ln, r.client, r.rln = ln, client, rln
	r.ctx = runCtx
	r.started = true
	r.setLocked(ForwardState{Status: ForwardRunning})
	r.wg.Add(3)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		<-runCtx.Done()
		r.teardown()
	}()
	go func() {
		defer r.wg.Done()
		r.supervise(runCtx, client)
	}()
	go func() {
		defer r.wg.Done()
		if ln != nil {
			r.acceptLocal(runCtx, ln)
		} else {
			r.acceptRemote(runCtx, rln)
		}
	}()
	return nil
}

// open выполняет синхронную часть Start.
func (r *ForwardRunner) open(ctx context.Context) (ln net.Listener, client *ssh.Client, rln net.Listener, err error) {
	switch r.f.Kind {
	case "L", "D":
		var lc net.ListenConfig
		ln, err = lc.Listen(ctx, "tcp", r.f.Listen)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("не удалось занять локальный порт %s: %w", r.f.Listen, err)
		}
		client, err = r.dial(ctx)
		if err != nil {
			closeQuietly(ln)
			return nil, nil, nil, err
		}
		return ln, client, nil, nil
	case "R":
		client, err = r.dial(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		rln, err = r.listenRemote(ctx, client)
		if err != nil {
			closeQuietly(client)
			return nil, nil, nil, err
		}
		return nil, client, rln, nil
	default:
		return nil, nil, nil, fmt.Errorf("неизвестный тип проброса %q", r.f.Kind)
	}
}

// listenRemote запрашивает порт на сервере. Запрос ждёт ответа сервера, поэтому отмена ctx
// закрывает клиент: иначе Stop ждал бы зависший сервер.
func (r *ForwardRunner) listenRemote(ctx context.Context, client *ssh.Client) (net.Listener, error) {
	stop := context.AfterFunc(ctx, func() { closeQuietly(client) })
	rln, err := client.Listen("tcp", r.f.Listen)
	stop()
	if err != nil {
		return nil, fmt.Errorf("сервер не открыл порт %s: %w", r.f.Listen, err)
	}
	return rln, nil
}

// Stop останавливает проброс: закрывает слушатели, SSH-клиент и все соединения, ждёт горутины.
// Идемпотентен; повторные и параллельные вызовы ждут завершения первого.
func (r *ForwardRunner) Stop() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.stopped = true
		cancel := r.cancel
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		r.teardown()
		r.wg.Wait()

		r.mu.Lock()
		r.setLocked(ForwardState{Status: ForwardStopped})
		close(r.events)
		r.eventsClosed = true
		r.mu.Unlock()
	})
}

// teardown закрывает все ресурсы; горутины раннера после этого завершаются сами.
func (r *ForwardRunner) teardown() {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	r.closing = true
	ln, client, rln := r.ln, r.client, r.rln
	r.ln, r.client, r.rln = nil, nil, nil
	conns := make([]net.Conn, 0, len(r.conns))
	for c := range r.conns {
		conns = append(conns, c)
	}
	if r.started {
		r.setLocked(ForwardState{Status: ForwardStopped, Conns: r.state.Conns})
	}
	r.mu.Unlock()

	closeAll(ln, rln, client)
	for _, c := range conns {
		closeQuietly(c)
	}
}

// supervise ждёт обрыва клиента и переподключается.
func (r *ForwardRunner) supervise(ctx context.Context, client *ssh.Client) {
	for {
		_ = client.Wait() //nolint:gosec // G104: причина обрыва не нужна, важен сам факт
		r.mu.Lock()
		if r.closing || r.failed {
			r.mu.Unlock()
			return
		}
		rln := r.rln
		r.client, r.rln = nil, nil
		r.setLocked(ForwardState{Status: ForwardStarting, Conns: r.state.Conns})
		r.mu.Unlock()
		closeAll(nil, rln, client)

		if client = r.reconnect(ctx); client == nil {
			return
		}
	}
}

// reconnect повторяет dial с нарастающей паузой; nil — раннер останавливается.
func (r *ForwardRunner) reconnect(ctx context.Context) *ssh.Client {
	for attempt := 1; ; attempt++ {
		t := time.NewTimer(r.backoff(attempt))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}

		client, err := r.dial(ctx)
		var rln net.Listener
		if err == nil && r.f.Kind == "R" {
			if rln, err = r.listenRemote(ctx, client); err != nil {
				closeQuietly(client)
				client = nil
			}
		}

		r.mu.Lock()
		if r.closing || r.failed {
			r.mu.Unlock()
			closeAll(nil, rln, client)
			return nil
		}
		if errors.Is(err, ErrHostNotTrusted) {
			// Пользователь отказался доверять ключу сервера: повтор задал бы тот же вопрос снова.
			// Статус error окончательный, локальный порт освобождается.
			r.failed = true
			ln := r.ln
			r.ln = nil
			r.setLocked(ForwardState{Status: ForwardError, Error: err.Error(), Detail: errorCauses(err), Conns: r.state.Conns})
			r.mu.Unlock()
			closeAll(ln, nil, nil)
			return nil
		}
		if err != nil {
			r.setLocked(ForwardState{Status: ForwardStarting, Error: err.Error(), Detail: errorCauses(err), Conns: r.state.Conns})
			r.mu.Unlock()
			continue
		}
		r.client, r.rln = client, rln
		r.setLocked(ForwardState{Status: ForwardRunning, Conns: r.state.Conns})
		if rln != nil {
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				r.acceptRemote(ctx, rln)
			}()
		}
		r.mu.Unlock()
		return client
	}
}

// acceptLocal принимает соединения на локальном порту L/D.
func (r *ForwardRunner) acceptLocal(ctx context.Context, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			r.listenerFailed(err)
			return
		}
		client, ok := r.track(c, true)
		if !ok {
			closeQuietly(c) // остановка или переподключение: соединение сразу закрывается
			continue
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer r.untrack(c)
			if r.socks != nil {
				_ = r.socks.ServeConn(c) //nolint:gosec // G104: ошибка клиента SOCKS касается только этого соединения
				return
			}
			remote, err := client.DialContext(ctx, "tcp", r.f.Target)
			if err != nil {
				return
			}
			pipe(c, remote)
		}()
	}
}

// acceptRemote принимает соединения, пришедшие на порт сервера (R), и подключается к Target локально.
func (r *ForwardRunner) acceptRemote(ctx context.Context, rln net.Listener) {
	for {
		c, err := rln.Accept()
		if err != nil {
			return // клиент закрыт или оборвался; переподключение запросит порт заново
		}
		if _, ok := r.track(c, false); !ok {
			closeQuietly(c)
			continue
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer r.untrack(c)
			d := net.Dialer{Timeout: forwardDialTimeout}
			local, err := d.DialContext(ctx, "tcp", r.f.Target)
			if err != nil {
				return
			}
			pipe(c, local)
		}()
	}
}

// listenerFailed обрабатывает поломку локального слушателя не из-за остановки: слушатель и клиент
// закрываются, статус error остаётся до Stop, переподключения не будет.
func (r *ForwardRunner) listenerFailed(err error) {
	r.mu.Lock()
	if r.closing || r.failed {
		r.mu.Unlock()
		return
	}
	r.failed = true
	ln, client, rln := r.ln, r.client, r.rln
	r.ln, r.client, r.rln = nil, nil, nil
	r.setLocked(ForwardState{Status: ForwardError, Error: fmt.Sprintf("локальный порт %s: %v", r.f.Listen, err), Conns: r.state.Conns})
	r.mu.Unlock()
	closeAll(ln, rln, client)
}

// track регистрирует принятое соединение. needClient — нужен живой клиент (L/D); false в ok
// значит, что соединение надо закрыть.
func (r *ForwardRunner) track(c net.Conn, needClient bool) (*ssh.Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.failed || (needClient && r.client == nil) {
		return nil, false
	}
	r.conns[c] = struct{}{}
	r.setLocked(ForwardState{Status: r.state.Status, Error: r.state.Error, Detail: r.state.Detail, Conns: len(r.conns)})
	return r.client, true
}

func (r *ForwardRunner) untrack(c net.Conn) {
	closeQuietly(c)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, c)
	r.setLocked(ForwardState{Status: r.state.Status, Error: r.state.Error, Detail: r.state.Detail, Conns: len(r.conns)})
}

// socksDial открывает соединение к цели SOCKS-запроса через текущий SSH-клиент.
// Контекст библиотеки (context.Background) не отменяется, поэтому используется контекст раннера.
func (r *ForwardRunner) socksDial(_ context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	client, ctx := r.client, r.ctx
	r.mu.Unlock()
	if client == nil || ctx == nil {
		return nil, errReconnecting
	}
	return client.DialContext(ctx, network, addr)
}

// setLocked меняет состояние и отправляет событие; вызывается под r.mu. Отправка не блокирует:
// при полном буфере выбрасывается самое старое событие.
func (r *ForwardRunner) setLocked(st ForwardState) {
	if st == r.state {
		return
	}
	r.state = st
	if r.eventsClosed {
		return
	}
	for {
		select {
		case r.events <- st:
			return
		default:
		}
		select {
		case <-r.events:
		default:
		}
	}
}

// pipe копирует данные в обе стороны. Конец данных с одной стороны передаётся полузакрытием,
// ошибка закрывает обе стороны; функция возвращается, когда оба направления завершились.
func pipe(a, b net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		copyHalf(b, a)
	}()
	copyHalf(a, b)
	<-done
	closeQuietly(a)
	closeQuietly(b)
}

func copyHalf(dst, src net.Conn) {
	_, err := io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok && err == nil {
		if cw.CloseWrite() == nil {
			return
		}
	}
	closeQuietly(dst)
	closeQuietly(src)
}

// remoteResolver оставляет имя как есть: его разрешает SSH-сервер, как у ssh -D.
type remoteResolver struct{}

func (remoteResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

// rejectSocksCommand отвечает «команда не поддерживается» (UDP ASSOCIATE через SSH не пробрасывается).
func rejectSocksCommand(_ context.Context, w io.Writer, _ *socks5.Request) error {
	return socks5.SendReply(w, statute.RepCommandNotSupported, nil)
}

// wgPool запускает горутины SOCKS-сервера под WaitGroup раннера, чтобы Stop их дождался.
type wgPool struct{ wg *sync.WaitGroup }

func (p wgPool) Submit(f func()) error {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		f()
	}()
	return nil
}

// closeAll закрывает то, что не nil. Клиент закрывается первым: Close слушателя R отправляет
// cancel-tcpip-forward и ждёт ответа, на зависшем соединении это заблокировало бы Stop; с
// закрытым клиентом запрос сразу завершается ошибкой.
func closeAll(ln, rln net.Listener, client *ssh.Client) {
	if client != nil {
		closeQuietly(client)
	}
	for _, l := range []net.Listener{rln, ln} {
		if l != nil {
			closeQuietly(l)
		}
	}
}

// closeQuietly закрывает ресурс при остановке или после основной ошибки; ошибка закрытия не важна.
func closeQuietly(c io.Closer) {
	_ = c.Close() //nolint:gosec // G104: best-effort cleanup
}
