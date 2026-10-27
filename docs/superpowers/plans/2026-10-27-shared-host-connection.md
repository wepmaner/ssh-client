# Общее подключение к серверу, порт без адреса, дублирование проброса: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Все пробросы и консоли одного хоста работают поверх одного SSH-подключения с одним входом, пробросы группируются по хосту с «Запустить все / Остановить все», поле «Слушать» принимает и показывает только порт, проброс можно дублировать.

**Architecture:** Новый объект `hostConn` в `app` владеет `ssh.Client` хоста, его входом (вопросы задаёт тот, кто открыл подключение), памятью секретов (фраза ключа и пароль, только в памяти) и переподключением с паузой. `ForwardRunner` получает режим общего клиента: он берёт клиента у `hostConn` и никогда его не закрывает. Сессии терминала тоже берут клиента у `hostConn` и открывают на нём свой канал. Фронтенд группирует карточки по хосту; мини-терминал входа переезжает с карточки в заголовок группы.

**Tech Stack:** Go (`golang.org/x/crypto/ssh`), Wails v3 beta.25, Svelte 5 + TypeScript, Vitest, `internal/sshclient/sshtest` для тестов.

**Spec:** `docs/superpowers/specs/2026-10-27-forwards-shared-connection-design.md` (читать целиком перед любой задачей) и `CLAUDE.md`.

## Global Constraints

- Фраза ключа и пароль живут только в памяти процесса как `[]byte`; каждая копия зачищается `clear`; в хранилище, на диск, в DTO, события и логи не попадают никогда.
- Секреты подключения зачищаются, когда подключение закрывается: ушёл последний пользователь, хост удалён, выход из приложения, окончательная ошибка, правка параметров входа хоста.
- Запуск/остановка/правка одного проброса и открытие/закрытие консоли не рвут подключение и других его пользователей.
- `ForwardStop` возвращается, когда локальный порт свободен.
- Keepalive подключения: `keepalive@openssh.com` каждые 15 с, три неудачи подряд закрывают клиента (`keepaliveInterval`, `keepaliveMaxFails` в `app/forwards.go`).
- Пауза переподключения: 2, 4, 8, 16, 32, затем 60 с (`defaultBackoff` в `internal/sshclient/forward.go`).
- Подсказка «Слушать»: `5432` для L, `9000` для R, `1080` для D; пояснение под полем ровно «Порт; адрес по умолчанию 127.0.0.1».
- Тексты ожидания входа ровно: в консоли `Идёт вход для пробросов: ответьте в разделе «Пробросы»`, в мини-терминале группы `Идёт вход в консоли «<имя хоста>»` (каждый с `\r\n`).
- `math/rand` запрещён; фронтенд ходит к Go только через `frontend/src/lib/api.ts`; биндинги перегенерировать `wails3 generate bindings -clean=true -ts` и закоммитить `frontend/bindings/`.
- Нативные правила CLAUDE.md: без спиннеров, фокус по `tokens.css`, контекстное меню `ui/Menu.svelte`, тексты по-русски.
- Формат хранилища не меняется (`model.Forward`, `model.Host` как есть).
- Комментарии и стиль — как в окружающем коде (`app/*` — английские комментарии, `internal/sshclient/*` — русские).
- В начале работы в дереве есть чужие незакоммиченные правки (`app/app.go`, `app/forwards.go`, `app/sessions.go`, `frontend/src/lib/ui/Menu.svelte`, `frontend/src/lib/ui/Menu.test.ts`). До Task 1 контролёр выясняет у владельца, коммитить ли их отдельным коммитом; в коммиты задач они не попадают. Правка в `forwardDial` (зачистка `t.Key.Private`, если `knownHosts()` вернул ошибку) должна сохраниться в новом коде dial подключения хоста (Task 3).

## Review Focus

1. Два проброса одного хоста с зашифрованным ключом запускаются одновременно (автозапуск): фраза спрашивается ровно один раз, сервер видит одно TCP-подключение. Тест — Task 4 `TestForwardStartAllAsksPassphraseOnce`.
2. Остановка R-проброса, когда сервер завис (не отвечает на `cancel-tcpip-forward`): `ForwardStop` не висит дольше 10 с, общий клиент не закрывается. Тест — Task 2 `TestSharedRunnerStopRemoteOnFrozenServer`.
3. Консоль начала вход, пробросы ждут, консоль закрыли во время вопроса: вход начинается заново, вопрос приходит в мини-терминал группы. Тест — Task 5 `TestSessionCloseDuringLoginHandsOverToForwards`.
4. Правка единственного работающего проброса хоста (смена порта) не закрывает подключение и не спрашивает фразу снова. Тест — Task 4 `TestForwardSaveKeepsConnection`.
5. Выход из приложения, пока подключение переподключается после обрыва: `ServiceShutdown` возвращается быстро, секреты зачищены. Тест — Task 3 `TestHostConnCloseDuringReconnect`.

---

### Task 1: Запомненный пароль в `sshclient.Dial`

**Files:**
- Modify: `internal/sshclient/client.go` (`Target`, `authMethods`, `password`, `keyboardInteractive`, `dialState`)
- Test: `internal/sshclient/client_test.go`

**Interfaces:**
- Produces: поля `Target.Password []byte` и `Target.OnPassword func(p []byte)` с тем же контрактом, что `Passphrase`/`OnPassphrase`: Dial пробует `Password` первым без вопроса (один раз), затирает буфер перед возвратом при любом исходе; `OnPassword` вызывается не больше одного раза, только после успешной аутентификации, с тем секретом, которым прошёл вход; получатель копирует.
- «Секрет пароля» — ответ на `PasswordCallback` или на раунд keyboard-interactive ровно с одним вопросом без эха. Запоминается первый такой ответ за Dial (второй раунд, например одноразовый код, не запоминается). Запомненный пароль пробуется в первом подходящем месте (PasswordCallback или первый раунд KI с одним вопросом без эха) один раз; дальше — обычные вопросы.

- [ ] **Step 1: Write the failing tests** в `client_test.go` на `sshtest`:
  - `TestDialPasswordFromMemoryNoPrompt`: `Opts{Password: "s3cret"}`, `Target{Password: []byte("s3cret")}`, Prompter с пустым входом → Dial успешен, в выводе Prompter нет `Пароль`; буфер `Password` после Dial — все нули.
  - `TestDialKBIPasswordFromMemoryNoPrompt`: `Opts{KBIAnswer: "s3cret"}` → то же через keyboard-interactive.
  - `TestDialOnPasswordGetsAcceptedSecret`: пароль вводится в Prompter (`"s3cret\r"`), `OnPassword` получает `s3cret` ровно один раз.
  - `TestDialWrongRememberedPasswordAsks`: `Target.Password = "old"`, ввод `"s3cret\r"` → успех, вопрос был ровно один, `OnPassword` получил `s3cret`.
  - `TestDialOnPasswordNotCalledOnFailure`: неверный пароль три раза → ошибка `ErrAuthDenied`, `OnPassword` не вызван.

- [ ] **Step 2: Run** `go test ./internal/sshclient/ -run 'TestDial.*Password' -race` — Expected: FAIL (нет полей).
- [ ] **Step 3: Implement** в `dialState`: поле `password []byte` (своя копия, зачищается в конце Dial вместе с фразой), признак «запомненный уже пробовали»; `password(t)` и `keyboardInteractive` сначала отдают `string(t.Password)` один раз, затем спрашивают; принятый ответ кладут в `d.password`, если он ещё пуст. После успешного `NewClientConn` — `t.OnPassword(d.password)`, если оба не nil. `defer clear(t.Password)` в начале Dial.
- [ ] **Step 4: Run** `go test ./internal/sshclient/ -race` — Expected: PASS (включая старые тесты).
- [ ] **Step 5: Commit** `feat(sshclient): Dial takes a remembered password and reports the accepted one`

---

### Task 2: Режим общего клиента у `ForwardRunner`

**Files:**
- Modify: `internal/sshclient/forward.go`
- Test: `internal/sshclient/forward_test.go`

**Interfaces:**
- Produces: `func NewSharedForwardRunner(f model.Forward, acquire func(ctx context.Context) (*ssh.Client, error)) *ForwardRunner`.
  - `acquire` блокируется до готового клиента, ошибки попытки или отмены ctx. Клиент принадлежит вызывающему (подключению хоста): раннер его **никогда не закрывает** (ни в `teardown`, ни в `listenerFailed`, ни в `supervise`, ни при ошибке `listenRemote`).
  - Пауза между вызовами `acquire` при переподключении — 0 (паузу держит подключение хоста); `ErrHostNotTrusted`/`ErrHostKeyChanged` из `acquire` — окончательная ошибка, как сейчас.
  - Остановка R в этом режиме: закрытие серверного слушателя (`cancel-tcpip-forward`) ждёт не больше `forwardDialTimeout` (10 с), затем раннер считает порт снятым и идёт дальше; клиент не закрывается.
  - Обрыв общего клиента (`client.Wait` вернулся) раннер обрабатывает как сейчас: `starting`, затем снова `acquire`.
- `NewForwardRunner` (свой клиент) остаётся без изменений поведения.

- [ ] **Step 1: Write the failing tests**:
  - `TestSharedRunnerDoesNotCloseClient`: один клиент к `sshtest`, два раннера L на нём, Stop первого → второй продолжает пропускать соединения (`roundTrip`), клиент жив (`SendRequest` успешен).
  - `TestSharedRunnerReacquiresAfterDrop`: `DropConnections`, `acquire` во второй раз отдаёт новый клиент → раннер снова `running`, `acquire` вызван ровно 2 раза.
  - `TestSharedRunnerStopRemoteOnFrozenServer`: R-раннер, `FreezeConnections`, `Stop` возвращается за ≤ 11 с, клиент не закрыт (`client.Conn` не завершён — `Wait` не вернулся).
  - `TestSharedRunnerFinalErrorFromAcquire`: `acquire` возвращает `ErrHostKeyChanged` при переподключении → статус `error`, локальный порт свободен, `acquire` больше не вызывается.
- [ ] **Step 2: Run** `go test ./internal/sshclient/ -run TestSharedRunner -race` — Expected: FAIL.
- [ ] **Step 3: Implement**: поле `shared bool` в `ForwardRunner`; `closeAll` в shared-режиме не трогает клиента, а `rln.Close()` выполняет в горутине с ожиданием ≤ `forwardDialTimeout`; `backoff` в shared-режиме возвращает 0.
- [ ] **Step 4: Run** `go test ./internal/sshclient/ -race` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(sshclient): forward runner over a shared client it never closes`

---

### Task 3: Подключение хоста `hostConn`

**Files:**
- Create: `app/hostconn.go`
- Create: `app/hostconn_test.go`
- Modify: `internal/sshclient/sshtest/sshtest.go` (счётчик подключений)
- Modify: `app/app.go` (поля `App`: `hcMu sync.Mutex`, `hostConns map[string]*hostConn`, `hostKeyChanged map[string]bool`; `ServiceShutdown` в конце вызывает `a.closeAllHostConns()`)
- Modify: `app/forwards.go` (функция `keepalive` переезжает в `hostconn.go` без изменений)

**Interfaces:**
- Consumes: `Target.Password/OnPassword` (Task 1), `Target.Passphrase/OnPassphrase`, `dialTarget`, `a.hostTarget`, `a.knownHosts`, `a.trustAsker`, `keepalive`.
- Produces:
  ```go
  // connAsker is who answers the login questions of one acquire: a session's terminal or a host's forwards group.
  type connAsker struct {
      out   *outStream
      in    *inputQueue
      trust trustOwner
      label    string              // "" for the forwards group; the host name for a session
      prompter *sshclient.Prompter // optional: a session passes its own to read Pending after login; nil — built from out/in
  }
  type hostParams struct{ Address string; Port int; User, KeyID string }

  func (a *App) hostConnFor(hostID string) (*hostConn, error)   // current conn of the host, created on demand; errHostNotFound
  func (c *hostConn) join(u any)                                // u is a *fwdRun or a *session
  func (c *hostConn) leave(u any)                               // last user: close client, stop loops, wipe secrets, drop from a.hostConns
  func (c *hostConn) acquire(ctx context.Context, u any, ask *connAsker) (*ssh.Client, error)
  func (c *hostConn) markStale()                                // wipe secrets, detach from a.hostConns; current users stay
  func (c *hostConn) state() HostConnStateDTO
  func (a *App) closeAllHostConns()                             // shutdown: stop every conn, wipe every secret, wait for goroutines

  // HostConnStateDTO is the payload of fwdhost:state:<hostID>.
  type HostConnStateDTO struct {
      Status    string `json:"status"`     // none|connecting|connected|error
      Error     string `json:"error"`
      Detail    string `json:"detail"`
      Retryable bool   `json:"retryable"`  // false after ErrHostKeyChanged until restart
      Asker     string `json:"asker"`      // ""|"forwards"|"session": who answers the current login
      Session   string `json:"session"`    // the session's label when Asker is "session"
  }
  ```
  `hostConn` получает при создании функцию `emit func(HostConnStateDTO)` (Task 4 подключает её к событию группы; в Task 3 — тестовый приёмник).
- Поведение `acquire` (по спеке, раздел «Подключение хоста»):
  - подключено → сразу клиент;
  - вход уже идёт → ждать его итога; если `ask` не тот, кто отвечает, напечатать в `ask.out` текст ожидания из Global Constraints (для группы — `Идёт вход в консоли «label»`, для консоли — `Идёт вход для пробросов…`);
  - иначе начать вход с `ask` (Prompter на `ask.out`/`ask.in`, `Trust` через `a.trustAsker(ask.trust, …)`, `Passphrase`/`Password` из памяти, `OnPassphrase`/`OnPassword` кладут копии в память; фраза привязана к `KeyID`);
  - вход отменён, потому что ctx отвечающего закончился, а ожидающие остались → новый вход с `ask` одного из оставшихся;
  - ошибка входа → её получают все ожидающие; `ErrHostKeyChanged` → `a.hostKeyChanged[hostID] = true`, дальше `acquire` этого хоста сразу возвращает `ErrHostKeyChanged` до перезапуска.
- После успеха: горутина `keepalive(client, interval, keepaliveMaxFails)` (interval — `a.fwdKeepalive` или `keepaliveInterval`) и наблюдатель `client.Wait()`. Обрыв: если среди пользователей есть `*fwdRun` — цикл переподключения с `defaultBackoff`-паузами, вход с отвечающим «группа пробросов» (`connAsker`, который возвращает функция `forwardsAsker func() *connAsker`, переданная при создании; в Task 3 — тестовая); `acquire` во время цикла ждёт итога **следующей** попытки (поколение попыток) и возвращает её ошибку или клиента. Окончательные ошибки (`ErrHostNotTrusted`, `ErrHostKeyChanged`) останавливают цикл и зачищают секреты. Если `*fwdRun` нет — закрыть, зачистить, ждать нового `acquire`.
- `hostConnFor`: если параметры хоста (`hostParams`) отличаются от параметров текущего подключения — `markStale()` старого и новое подключение.
- `sshtest`: `func (s *Server) Accepted() int` — число принятых TCP-подключений за жизнь сервера.

- [ ] **Step 1: Write the failing tests** в `app/hostconn_test.go` (харнесс `newSessionsApp`/`newEncryptedForwardsApp` из существующих тестов):
  - `TestHostConnSharesOneClient`: два `acquire` разных пользователей → один и тот же `*ssh.Client`, `srv.Accepted() == 1`.
  - `TestHostConnConcurrentAcquireOneLogin`: зашифрованный ключ, два `acquire` одновременно, фраза вводится один раз в `in` первого → оба получают клиента; в выводе ровно один `Парольная фраза ключа`; у второго в `out` строка ожидания.
  - `TestHostConnLastLeaveClosesAndWipes`: после `leave` последнего — клиент закрыт (`Wait` вернулся), память фразы и пароля — нули (проверить через внутренние буферы, как в `TestForwardStopClearsPassphrase`).
  - `TestHostConnReconnectUsesRememberedPassword`: хост без ключа, `Opts{Password}`, пользователь `*fwdRun`, `DropConnections` → новое подключение без вопроса, `srv.Accepted() == 2`.
  - `TestHostConnDropWithoutForwardsCloses`: пользователь только сессия, `DropConnections` → подключение закрыто, секреты зачищены, переподключения нет (`Accepted()` остаётся 1 спустя 3 с с пониженным backoff).
  - `TestHostConnHostKeyChangedBlocksHost`: подменённый ключ сервера в known_hosts → `ErrHostKeyChanged`, повторный `acquire` возвращает её сразу без TCP (`Accepted()` не растёт), `state().Retryable == false`.
  - `TestHostConnStaleOnHostEdit`: после смены `User` хоста `hostConnFor` возвращает другой объект; старый продолжает обслуживать своего пользователя; секреты старого зачищены.
  - `TestHostConnCloseDuringReconnect`: пользователь `*fwdRun`, `Stop` сервера (`DropConnections` + закрытый порт), идёт цикл; `closeAllHostConns()` возвращается за ≤ 2 с, секреты — нули.
  - `TestHostConnKnownHostsErrorWipesKey`: `knownHosts()` возвращает ошибку → копия `t.Key.Private` зачищена (перенос незакоммиченной правки из `forwardDial`).
- [ ] **Step 2: Run** `go test ./app/ -run TestHostConn -race` — Expected: FAIL.
- [ ] **Step 3: Implement** `app/hostconn.go` и `Server.Accepted()` по Interfaces.
- [ ] **Step 4: Run** `go test ./app/ ./internal/... -race` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(app): one SSH connection per host with its login, secrets and reconnect`

---

### Task 4: Пробросы на подключении хоста, групповые биндинги

**Files:**
- Modify: `app/forwards.go`, `app/forwards_api.go`, `app/trust_api.go`, `app/hosts_api.go` (`HostSave` → `markStale` при смене `hostParams`; `HostDelete` закрывает подключение хоста)
- Modify: `app/forwards_test.go`, `app/trust_api_test.go`
- Regenerate: `frontend/bindings/`

**Interfaces:**
- Consumes: Task 2 `NewSharedForwardRunner`, Task 3 `hostConn`.
- Produces (биндинги и события, которыми пользуется Task 6):
  ```go
  type ForwardHostDTO struct {
      HostID   string `json:"host_id"`
      HostName string `json:"host_name"`
      HostConnStateDTO
  }
  func (a *App) ForwardHostsList() []ForwardHostDTO      // хосты, у которых есть пробросы, по имени хоста
  func (a *App) ForwardStartAll(hostID string) error      // запускает остановленные и упавшие пробросы хоста
  func (a *App) ForwardStopAll(hostID string) error       // останавливает все пробросы хоста; консоли не трогает; возвращается, когда порты свободны
  func (a *App) ForwardHostWrite(hostID, dataB64 string) error // ввод в мини-терминал группы
  func (a *App) ForwardHostAttach(hostID string) error    // как ForwardAttach, для событий группы
  ```
  События группы: `fwdhost:state:<hostID>` (`HostConnStateDTO`), `fwdhost:data:<hostID>` (base64, вывод вопросов входа). Один `outStream` группы на хост живёт в `App` (как `fwdEntry.out`), одна `inputQueue` на текущий вход группы (новая на каждый вход; `ForwardHostWrite` пишет в текущую).
  `TrustAskDTO`: поле `ForwardID`/`forward_id` заменяется на `ForwardHostID string \`json:"forward_host_id"\``; `trustOwner{sessionID, forwardHostID string}`.
  Удаляются: `ForwardWrite`, событие `fwd:data:<id>`, вопросы входа у отдельного проброса, память секрета в `fwdRun` (`secretFor`/`rememberSecret`/`forgetSecret` — память теперь в `hostConn`). `fwd:state:<id>` и `ForwardAttach` остаются. `ForwardStateDTO.Retryable` = `!a.hostKeyChanged[hostID]`.
- `startForward`: `hc.join(run)` → `NewSharedForwardRunner(f, func(ctx) { return hc.acquire(ctx, run, a.forwardsAsker(hostID)) })`. `stopForward`: `run.stop()`, затем `hc.leave(run)`. Перезапуск в `ForwardSave` (смена типа/портов): `join` нового run **до** `leave` старого, чтобы подключение не закрылось. Смена хоста: `leave` у старого подключения, `join` у нового.
- Название по умолчанию в `validateForward`: `shortListen(f.Listen)` вместо `f.Listen`, где `func shortListen(addr string) string` убирает префикс `127.0.0.1:` (`L 5432 → db:5432`, `SOCKS5 1080`).

- [ ] **Step 1: Write the failing tests** в `forwards_test.go` (старые тесты про секрет у проброса — `TestForwardReconnectDoesNotReaskPassphrase`, `TestForwardStopClearsPassphrase`, `TestForwardHostChangeClearsPassphrase`, `TestForwardErrorClearsPassphrase`, `TestForwardPromptAndStopDuringPrompt` — переписать на группу хоста, смысл сохранить):
  - `TestForwardStartAllAsksPassphraseOnce`: зашифрованный ключ, три L-проброса, `ForwardStartAll` → в `fwdhost:data:<hostID>` ровно один вопрос фразы, после ответа через `ForwardHostWrite` все три `running`, `srv.Accepted() == 1`.
  - `TestForwardStartOnLiveConnectionNoPrompt`: после предыдущего — новый проброс `ForwardSave` + `ForwardStart` → `running` без новых байтов в `fwdhost:data`, `Accepted() == 1`.
  - `TestForwardStopOneKeepsOthers`: `ForwardStop` одного → у других `roundTrip` проходит, их `conns` не обнулились от разрыва.
  - `TestForwardSaveKeepsConnection`: единственный работающий проброс, смена `Listen` → новый порт работает, старый свободен, `Accepted() == 1`, вопроса фразы нет.
  - `TestForwardStopAllClosesConnection`: `ForwardStopAll` → все порты свободны, подключение закрыто (`fwdhost:state` `none`), секреты — нули.
  - `TestForwardDefaultNameShortListen`: `Listen: "5432"` без названия → `Name == "L 5432 → db.example.test:5432"`; `Listen: "0.0.0.0:9000"` → название содержит `0.0.0.0:9000`. Поправить ожидания в `TestForwardSaveValidation` (`L 127.0.0.1:15432 → …` → `L 15432 → …`, `SOCKS5 127.0.0.1:1080` → `SOCKS5 1080`).
  - `TestHostSaveChangedUserMarksStale`: работающий проброс, смена пользователя хоста → проброс работает дальше; следующий `ForwardStart` другого проброса открывает новое подключение (`Accepted() == 2`).
  - `TestForwardTrustAskCarriesHost`: неизвестный ключ сервера → `trust:ask` с `forward_host_id == hostID`, ответ `TrustAnswer(true)` поднимает все ожидающие пробросы.
  - `TestForwardSecretNeverInStore` — оставить, проверить и пароль.
- [ ] **Step 2: Run** `go test ./app/ -run 'TestForward|TestHostSave|TestTrust' -race` — Expected: FAIL.
- [ ] **Step 3: Implement** по Interfaces.
- [ ] **Step 4: Run** `go test ./... -race` и `go vet ./...` — Expected: PASS.
- [ ] **Step 5: Regenerate bindings** `wails3 generate bindings -clean=true -ts`; правки `frontend/src/lib/api.ts` — реэкспорт новых функций, убрать `ForwardWrite` (компиляция фронтенда чинится в Task 6; в этой задаче — только `api.ts` и `bindings/`).
- [ ] **Step 6: Commit** `feat(app): forwards share their host's connection; start and stop all of a host`

---

### Task 5: Консоли на подключении хоста

**Files:**
- Modify: `app/sessions.go`, `app/sessions_api.go`, `app/hosts_api.go` (если `closeSessionsOfHost` трогает клиента)
- Modify: `app/sessions_test.go`, `app/wipe_test.go` (если `dialTarget` переедет)

**Interfaces:**
- Consumes: Task 3 `hostConnFor`, `join/leave/acquire`, `connAsker{out: s.out, in: s.in, trust: trustOwner{sessionID: s.id}, label: s.hostName}`.
- Изменения: `SessionOpen` берёт `hc := a.hostConnFor(hostID)`, `hc.join(s)`; `session.run` вместо `dialTarget` вызывает `hc.acquire(s.ctx, s, ask)`; сессия **не закрывает** общий клиент ни при каком исходе — закрывает только свой канал `sshclient.Session`; по окончании `run` — `hc.leave(s)`. `session.close()` не ждёт сети: отменяет ctx, закрывает свой канал (если `sess.Close` может блокироваться на зависшем клиенте — в горутине). `handover` и `Prompter.Pending` — как сейчас, Prompter сессии создаётся в `run` и передаётся в `connAsker.prompter` (Task 3), чтобы после `acquire` взять `Pending`.
- Обрыв общего клиента: канал сессии закрывается, сессия завершается карточкой ошибки как сейчас (`errorState`). «Переподключить» в UI открывает новую сессию через `SessionOpen` → `acquire` ждёт идущего переподключения пробросов.

- [ ] **Step 1: Write the failing tests** в `sessions_test.go`:
  - `TestSessionReusesForwardConnection`: работает проброс (фраза введена в группе), `SessionOpen` → `connected` без вопроса фразы в `term:data`, `srv.Accepted() == 1`.
  - `TestForwardReusesSessionConnection`: открыта консоль (фраза введена в консоли), `ForwardStart` → `running` без байтов в `fwdhost:data`, `Accepted() == 1`.
  - `TestSessionCloseKeepsForwards`: проброс и консоль на одном подключении, `SessionClose` → `roundTrip` через проброс проходит.
  - `TestStopAllForwardsKeepsSession`: `ForwardStopAll` при открытой консоли → консоль остаётся `connected`, ввод доходит до shell.
  - `TestSessionWaitsForForwardsLogin`: вход начат пробросами (вопрос висит), `SessionOpen` → в `term:data` строка `Идёт вход для пробросов: ответьте в разделе «Пробросы»`; после ответа в группе консоль `connected`.
  - `TestSessionCloseDuringLoginHandsOverToForwards`: вход начат консолью, проброс ждёт, `SessionClose` во время вопроса → в `fwdhost:data` появляется вопрос фразы; после ответа проброс `running`.
  - `TestSessionDropShowsErrorForwardsReconnect`: `DropConnections` → сессия `error`, проброс снова `running` без вопроса.
  - Старые тесты сессий (вопросы в консоли, Ctrl+C, тайм-аут, изменившийся ключ, `Pending`) проходят без изменения ожиданий.
- [ ] **Step 2: Run** `go test ./app/ -run 'TestSession|TestForwardReuses|TestStopAll' -race` — Expected: FAIL.
- [ ] **Step 3: Implement** по Interfaces.
- [ ] **Step 4: Run** `go test ./... -race` и `go vet ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(app): terminal sessions share the host connection with forwards`

---

### Task 6: Раздел «Пробросы» по группам хостов

**Files:**
- Create: `frontend/src/lib/forwards/ForwardGroup.svelte` (заголовок группы + сетка карточек)
- Modify: `frontend/src/lib/forwards/forwards.svelte.ts`, `ForwardsView.svelte`, `ForwardCard.svelte`, `MiniTerminal.svelte`, `frontend/src/lib/trust.svelte.ts`
- Test: `frontend/src/lib/forwards/ForwardsView.test.ts`, новый `frontend/src/lib/forwards/ForwardGroup.test.ts`

**Interfaces:**
- Consumes: Task 4 биндинги `ForwardHostsList`, `ForwardStartAll`, `ForwardStopAll`, `ForwardHostWrite`, `ForwardHostAttach`, события `fwdhost:state:<hostID>`, `fwdhost:data:<hostID>`, `TrustAskDTO.forward_host_id`.
- Produces: в `forwards.svelte.ts` — `forwards.hosts: app.ForwardHostDTO[]` (обновляется в `refreshForwards` и по `fwdhost:state`), буфер вывода по `hostId` вместо `forwardId` (`attachOutput(hostId, sink)`, `outputSize[hostId]`), `startAll(hostId)`, `stopAll(hostId)`; `MiniTerminal` с пропом `hostId` пишет через `ForwardHostWrite`; `forwardAsksTrust(hostId)` → `hostAsksTrust(hostId)` по `forward_host_id`.
- Заголовок группы: имя хоста; точка состояния (`none` — пустая, `connecting` — серая, `connected` — `--ok`, `error` — `--error`, те же классы `.dot`, что на карточке); «Запустить все» (`variant="primary"`, видна, если есть неактивные; `disabled` при `!retryable` с тем же `title`, что сейчас на карточке); «Остановить все» (`variant="outline"`, видна, если есть активные); кнопка-иконка «+» (`aria-label="Новый проброс для <хост>"`), открывающая `ForwardDialog` с `hostId`. Под заголовком — текст ошибки подключения (`error`, `detail`) и `MiniTerminal`, когда `status === 'connecting'`, есть вывод, `asker === 'forwards'` и нет открытого вопроса доверия этого хоста. Когда `asker === 'session'`, вместо мини-терминала — приглушённая строка `Идёт вход в консоли «<session>»`.
- Карточка: без мини-терминала; остальное как сейчас.
- Группы в порядке имён хостов, карточки внутри по имени; сводка и легенда остаются под всеми группами.

- [ ] **Step 1: Write the failing tests** (Vitest, моки `api.ts` как в `ForwardsView.test.ts`):
  - `groups forwards by host in host-name order`.
  - `start all calls ForwardStartAll with the host id`; `stop all calls ForwardStopAll`.
  - `start all is disabled when the host is not retryable`.
  - `shows the login mini-terminal in the group header while the forwards answer`; `shows the console note instead when a session answers`.
  - `plus button opens the new forward dialog with the host preselected`.
- [ ] **Step 2: Run** `cd frontend && npm test -- forwards` — Expected: FAIL.
- [ ] **Step 3: Implement** по Interfaces.
- [ ] **Step 4: Run** `cd frontend && npm test && npx svelte-check` — Expected: PASS, 0 ошибок.
- [ ] **Step 5: Commit** `feat(forwards): group forwards by host with start all and stop all`

---

### Task 7: «Слушать» только порт и «Дублировать»

**Files:**
- Create: `frontend/src/lib/forwards/format.ts`, `frontend/src/lib/forwards/format.test.ts`
- Modify: `ForwardDialog.svelte`, `ForwardCard.svelte`, `ForwardsView.svelte`, `ForwardGroup.svelte`
- Test: `ForwardDialog.test.ts`, `ForwardsView.test.ts`

**Interfaces:**
- Produces: `export function shortListen(addr: string): string` — `'127.0.0.1:5432' → '5432'`, `'0.0.0.0:9000' → '0.0.0.0:9000'`, `'[::1]:80' → '[::1]:80'`, `'5432' → '5432'`.
- `ForwardDialog`: новый проп `template?: app.ForwardDTO | null` — для нового проброса (`forward === null`) заполняет тип, хост, «Слушать» (`shortListen`), «Назначение», автозапуск; название пустое. При правке существующего «Слушать» показывается через `shortListen`. Подсказки `listenHint`: L `5432`, R `9000`, D `1080`; под полем `<span class="hint">Порт; адрес по умолчанию 127.0.0.1</span>` (если нет ошибки поля). Плейсхолдер названия (`defaultName`) использует `shortListen`. Заголовок диалога для копии — «Новый проброс».
- `ForwardCard`: маршрут показывает `shortListen(forward.listen)`; в меню после «Изменить» — `{ label: 'Дублировать', onselect: onduplicate }`; новый проп `onduplicate: () => void`.
- `ForwardsView`: состояние диалога `{ open, forward, template, hostId }`; «Дублировать» открывает `{ forward: null, template: f }`.

- [ ] **Step 1: Write the failing tests**:
  - `format.test.ts`: четыре случая `shortListen` выше.
  - `ForwardDialog.test.ts`: `duplicate prefills fields with an empty name and saves a new forward` (сохранение уходит с `id: ''`, `listen: '5432'`, тем же `target`, `host_id`); `edit shows a 127.0.0.1 listen as the bare port`; `listen hint is the bare port and the note is shown`.
  - `ForwardsView.test.ts`: `card menu has Дублировать and it opens the new forward dialog`; `card route shows the bare port`.
- [ ] **Step 2: Run** `cd frontend && npm test -- forwards` — Expected: FAIL.
- [ ] **Step 3: Implement** по Interfaces.
- [ ] **Step 4: Run** `cd frontend && npm test && npx svelte-check` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(forwards): listen takes a bare port; duplicate a forward`

---

### Task 8: Правила в CLAUDE.md и полная проверка

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Переписать** в «Решения по продукту»: первый пункт («Пароли не храним…») — добавить исключение: пароль входа, принятый сервером, держит подключение хоста только в памяти, пока оно открыто (решение 2026-10-27); пункт про парольную фразу — исключение 13c заменить на «подключение хоста (общее для пробросов и консолей хоста, решение 2026-10-27) держит принятую фразу и пароль только в памяти, пока открыто, и использует их при автоматических переподключениях пробросов; зачищает при закрытии подключения, удалении хоста, правке его параметров входа и выходе из приложения; на диск не попадают никогда». В «План» добавить строку со ссылкой на спеку и этот план.
- [ ] **Step 2: Run** полную проверку CI локально: `go vet ./...`, `go test ./... -race`, `golangci-lint run`, `cd frontend && npm test && npx svelte-check`, `wails3 build` — Expected: всё зелёное, `build/bin/sshclient.exe` собран.
- [ ] **Step 3: Живая проверка** по `[[ssh-client-ui-driving]]` (память): два проброса одного хоста с зашифрованным ключом — «Запустить все», фраза один раз; открыть консоль к тому же хосту — без фразы; закрыть консоль — пробросы живы; «Дублировать», поменять порт, сохранить — новый проброс работает без фразы.
- [ ] **Step 4: Commit** `docs: shared host connection keeps the passphrase and password in memory`

---

### Task 9: Понятные ошибки занятого порта (запрос владельца 2026-10-27)

Владелец: вместо «не удалось занять локальный порт 127.0.0.1:13001: listen tcp 127.0.0.1:13001: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted» карточка должна говорить по-человечески. Системный текст остаётся в подробностях (`ForwardState.Detail` через `errorCauses`), как у `dialError`.

**Files:**
- Modify: `internal/sshclient/neterr.go` (новая `listenError`), `neterr_windows.go`, `neterr_other.go` (коды), `internal/sshclient/forward.go` (места: `open` для L/D, `listenRemote`, `listenerFailed`)
- Test: `internal/sshclient/neterr_test.go`, `internal/sshclient/forward_test.go`

**Interfaces:**
- Produces: `func listenError(addr string, err error) error` — `*userError` с причиной `err`. Тексты ровно (порт — только номер, адрес — только если не `127.0.0.1`):
  - занят (`WSAEADDRINUSE` / `syscall.EADDRINUSE`): `Порт 13001 уже занят другой программой: выберите другой порт или закройте программу, которая его держит`; для адреса не 127.0.0.1 — `Порт 0.0.0.0:13001 уже занят …` с тем же хвостом;
  - запрещён (`WSAEACCES` / `syscall.EACCES`): `Windows не даёт занять порт 13001: он зарезервирован системой (часто Hyper-V или WSL), выберите другой порт`;
  - адрес не наш (`WSAEADDRNOTAVAIL` / `syscall.EADDRNOTAVAIL`): `Адрес 10.0.0.5 не принадлежит этому компьютеру: укажите 127.0.0.1 или адрес своей сети`;
  - прочее: `Не удалось занять локальный порт 13001` + причина в подробностях.
- Сервер отказал в R-порту (`client.Listen` вернул ошибку): `Сервер не открыл порт 9000: он занят на сервере или проброс запрещён его настройкой (AllowTcpForwarding, GatewayPorts)`, причина — в подробностях.
- Поломка слушателя после запуска (`listenerFailed`): `Локальный порт 13001 перестал принимать соединения`, причина — в подробностях.

- [ ] **Step 1: Write the failing tests**: `TestListenErrorAddrInUse` (занять порт `net.Listen`, второй раннер L на тот же адрес → `Error` ровно первый текст, `Detail` содержит системный текст), `TestListenErrorTexts` (табличный тест `listenError` на обёрнутых кодах для каждого случая, включая адрес не 127.0.0.1), `TestRemoteListenRefusedText` (sshtest отклоняет `tcpip-forward` → текст про сервер). Поправить ожидания существующих тестов, которые ищут старый текст (`не удалось занять локальный порт`) — в `internal/sshclient` и `app`.
- [ ] **Step 2: Run** `go test ./internal/sshclient/ ./app/` — Expected: FAIL.
- [ ] **Step 3: Implement** по Interfaces.
- [ ] **Step 4: Run** `go test ./... ` и `go vet ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(sshclient): busy, reserved and foreign listen addresses explained in Russian`
