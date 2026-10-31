# Доступ к серверам через MCP: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** встроить в `sshclient.exe` локальный MCP-сервер: список хостов, команды, заливка и скачивание файлов через общее подключение хоста, с режимом доступа на хост и журналом.

**Architecture:** `internal/mcpserver` — транспорт, инструменты и проверка режима просмотра, без знаний о приложении; зависит только от интерфейса `Backend`. `internal/sshclient` получает выполнение команды и SFTP-передачу поверх `*ssh.Client`. `app` реализует `Backend`: вход через `hostConn`, удержание, журнал, жизненный цикл, Wails-методы для Настроек.

**Tech Stack:** Go 1.27, `golang.org/x/crypto/ssh`, `github.com/modelcontextprotocol/go-sdk` v1.8.0, `github.com/pkg/sftp` v1.13.12, Wails v3, Svelte, xterm.js.

**Spec:** `docs/superpowers/specs/2026-10-31-mcp-access-design.md`

Ветка `mcp-access`.

## Global Constraints

- Порт по умолчанию `47821`, допустимо 1024–65535; слушать только `127.0.0.1`, путь `/mcp`.
- Токен: 32 байта `crypto/rand` в hex; проверка `crypto/subtle`; `GetSettings` отдаёт его пустым.
- Тумблер выключен, все существующие и новые хосты закрыты (`MCPAccess == ""`). Значения: `""`, `read`, `full`.
- Тексты интерфейса по-русски и не называют Claude. Подписи: «Доступ по MCP», «Разрешить доступ к серверам по MCP», «Скопировать команду подключения», «Скопировать промпт», «Сменить токен», «Журнал», «Вход по MCP: <хост>», «Закрыт», «Только просмотр», «Полный».
- Вывод: последние 200 КБ на поток, `timeout_sec` по умолчанию 60, максимум 600; передача файла 30 минут; ожидание входа внутри вызова 2 минуты; удержание 30 минут; после сигнала отмены ждать 2 секунды.
- Журнал: 500 записей, команда обрезается до 2 КБ, без вывода, вне резервной копии.
- Секреты, ключи и токен не попадают в ответы инструментов, журнал, события и логи. Секреты в `[]byte` с затиранием.
- Фронтенд ходит к Go только через `frontend/src/lib/api.ts`; биндинги: `wails3 generate bindings -clean=true -ts`, коммитятся. Никаких спиннеров, фокус по `tokens.css`.
- Проверки перед завершением: `go vet ./...`, `go test ./... -race`, `golangci-lint run`, в `frontend` `npm test` и `npx svelte-check`, `wails3 build`.

## Review Focus

1. Два одновременных вызова на хост с фразой: один вход, один диалог, оба выполняются. Тест в Task 6.
2. Путь к папке данных другим регистром, через `..` или через символическую ссылку: отказ в обе стороны. Тест в Task 5.
3. Бинарный вывод и обрез посреди UTF-8: ответ остаётся валидным JSON. Тест в Task 3.
4. Порт занят в момент запуска: приложение работает, Настройки показывают ошибку, повторное включение пробует снова. Тест в Task 7.
5. Обрыв передачи на середине: временный файл удалён, целевой файл цел. Тест в Task 3.

---

### Task 1: Данные: режим хоста, настройки, журнал

**Files:**
- Modify: `internal/model/model.go`, `internal/store/store.go`, `internal/store/backup.go`, `app/hosts_api.go`, `app/settings_api.go`
- Test: `internal/store/store_test.go`, `internal/store/backup_test.go`, `app/hosts_api_test.go`, `app/settings_api_test.go`

**Interfaces:**
- Produces: `model.Host.MCPAccess string` (`json:"mcp_access,omitempty"`); константы `model.MCPAccessClosed = ""`, `MCPAccessRead = "read"`, `MCPAccessFull = "full"`; `model.Settings.MCPEnabled bool`, `MCPPort int`, `MCPToken string`; `model.DefaultMCPPort = 47821`; `model.MCPRecord{ID, Time time.Time, HostID, HostName, Tool, Text, Access, Refused string..., ExitCode *int, DurationMs int64}`; `model.VaultData.MCPJournal []MCPRecord`; `(*Store).MCPJournalAdd(r model.MCPRecord) error` (обрезка до 500), `MCPJournalFinish(id string, exit *int, ms int64) error`, `MCPJournal() []model.MCPRecord` (от новых к старым), `MCPJournalClear() error`, `SaveMCP(enabled bool, port int, token string) error`. `HostDTO.MCPAccess`.

- [ ] **Step 1: Тесты.** `TestMCPJournalKeepsLast500`, `TestMCPJournalTruncatesText` (2 КБ, не рвёт руну), `TestMCPJournalFinish`, `TestBackupHasNoJournal`, `TestOldVaultHostsClosed` (JSON без поля → `MCPAccess == ""`, `MCPPort == 0` читается через `Port()`-вычисление как 47821), `TestHostSaveRejectsUnknownAccess` (`field:mcp_access`), `TestSaveSettingsKeepsMCPFields` (подмена enabled, port, token в `SaveSettings` игнорируется), `TestGetSettingsHidesToken`.
- [ ] **Step 2:** `go test ./internal/store ./app -run 'MCP|Backup|Access|Settings'` падает.
- [ ] **Step 3: Реализовать.** `backupPlain` обнуляет `MCPJournal`. `SaveSettings` сохраняет три поля MCP из хранилища, как `Window`. `GetSettings` очищает `MCPToken`. `DefaultSettings` ставит `MCPPort: DefaultMCPPort`; нулевой порт старого хранилища читать как 47821. Смена режима в `HostSave` не вызывает `markHostStale` (`paramsOf` не меняется); реакцию на понижение добавит Task 7.
- [ ] **Step 4:** тесты проходят.
- [ ] **Step 5:** коммит `feat(mcp): host access mode, settings and journal in the vault`.

### Task 2: Проверка команд режима просмотра

**Files:**
- Create: `internal/mcpserver/readonly.go`, `internal/mcpserver/readonly_test.go`

**Interfaces:**
- Produces: `func CheckReadOnly(command string) error` (nil — можно выполнять; ошибка по-русски называет причину).

- [ ] **Step 1: Табличный тест `TestCheckReadOnly`.** Разрешены: `docker ps -a`, `docker logs web --tail 50`, `docker compose ps`, `docker stats --no-stream`, `df -h`, `free -m`, `journalctl -u nginx -n 100`, `systemctl status nginx`, `ss -tlnp`, `ip addr`, `ip route show`, `cat /etc/os-release | grep -i name`, `date`, `hostname`, `hostname -I`, `grep "a b" /var/log/syslog | tail -n 5`. Отказ: `docker ps; rm -rf /`, `cat $(reboot)`, ``cat `reboot` ``, `cat "$(reboot)"`, `ls > x`, `ls & reboot`, `ls && reboot`, `ls\nreboot`, `date -s 2020-01-01`, `hostname evil`, `ss -tK`, `ss --kill`, `journalctl --vacuum-size=1M`, `journalctl --rotate`, `systemctl restart nginx`, `docker rm web`, `docker compose up`, `docker exec web sh`, `ip link set eth0 down`, `ls | sh`, `find / -delete`, `echo "unterminated`, пустая строка, `ls \` .
- [ ] **Step 2:** `go test ./internal/mcpserver -run CheckReadOnly` падает.
- [ ] **Step 3: Реализовать** разбор по таблице из раздела спеки «Только просмотр»: проход по рунам с состояниями «вне кавычек», `'`, `"`; разрез на звенья по `|` вне кавычек (`||` — отказ); затем проверка первого токена. Слитные короткие ключи `ss` проверять посимвольно на `K`. Запрещающий список только для ключей `journalctl`, всё прочее по разрешающему.
- [ ] **Step 4:** тест проходит.
- [ ] **Step 5:** коммит `feat(mcp): read-only command check`.

### Task 3: Команда и передача файлов поверх ssh.Client

**Files:**
- Create: `internal/sshclient/exec.go`, `internal/sshclient/transfer.go`, тесты рядом
- Modify: `internal/sshclient/sshtest/sshtest.go`, `go.mod`, `go.sum`

**Interfaces:**
- Produces: `type ExecResult struct{ Stdout, Stderr string; ExitCode int; Truncated, TimedOut bool }`; `func Exec(ctx context.Context, c *ssh.Client, command string, limit int) (ExecResult, error)` (`ctx` с дедлайном даёт `TimedOut`, отмена `ctx` даёт `context.Canceled` вместе с частичным выводом); `func Upload(ctx, c *ssh.Client, local, remote string, overwrite bool) (int64, error)`; `func Download(ctx, c *ssh.Client, remote, local string, overwrite bool) (int64, error)`; `var ErrExists`, `ErrNoSFTP` с русскими текстами.
- `sshtest.Opts` получает `Exec func(cmd string, stdout, stderr io.Writer, stop <-chan string) int` (`stop` получает имя сигнала) и `SFTPRoot string`; `""`/nil — запрос отклоняется, как сейчас.

- [ ] **Step 1: Тесты.** `TestExecStreamsAndExitCode`; `TestExecNonZeroExitIsNotError`; `TestExecKeepsTail` (limit 10, вывод 100 байт, `Truncated`, последние 10); `TestExecInvalidUTF8` (байт `0xff` и обрез посреди руны, `utf8.ValidString`); `TestExecTimeoutSignalsThenCloses` (сервер получает `TERM`, вызов завершается не позже 2 с + допуск, `TimedOut`); `TestUploadDownloadRoundTrip`; `TestUploadRefusesExisting`; `TestDownloadRefusesExisting`; `TestDownloadInterruptedKeepsTarget` (сервер обрывается на середине: цель прежняя, временных файлов в папке нет); `TestUploadInterruptedRemovesRemoteTemp`; `TestNoSFTPIsClearError`.
- [ ] **Step 2:** `go get github.com/pkg/sftp@v1.13.12`; тесты падают.
- [ ] **Step 3: Реализовать.** `Exec`: `ssh.Session`, `Stdout`/`Stderr` в кольцевые буферы размера `limit`, `strings.ToValidUTF8(…, "�")`, при завершении `ctx`: `sess.Signal(ssh.SIGTERM)`, 2 с, `sess.Close()`. `*ssh.ExitMissingError` при обрыве возвращать ошибкой. Передача: `sftp.NewClient(c)`, запись в `<цель>.sshclient-<uuid>.tmp` рядом с целью, затем `PosixRename` (удалённо) или `os.Rename` (локально), временный файл удалять при любой ошибке; `ctx` отменяет, закрывая файлы и SFTP-клиент. В `sshtest`: запрос `exec` с `Opts.Exec`, `signal`, запрос `subsystem` `sftp` через `sftp.NewServer` на `SFTPRoot`.
- [ ] **Step 4:** `go test ./internal/sshclient/... -race` проходит.
- [ ] **Step 5:** коммит `feat(sshclient): run a command and transfer files over a client`.

### Task 4: MCP-сервер

**Files:**
- Create: `internal/mcpserver/server.go`, `internal/mcpserver/tools.go`, `internal/mcpserver/server_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `CheckReadOnly`, `sshclient.ExecResult`.
- Produces:
  ```go
  type HostInfo struct {
      ID, Name, Group, Address, User, Access string
      Port int
  }
  type Backend interface {
      Hosts(ctx context.Context) ([]HostInfo, error)   // только незакрытые; ошибка при блокировке
      Run(ctx context.Context, h HostInfo, command string, timeout time.Duration) (sshclient.ExecResult, error)
      Upload(ctx context.Context, h HostInfo, local, remote string, overwrite bool) (int64, error)
      Download(ctx context.Context, h HostInfo, remote, local string, overwrite bool) (int64, error)
      Refused(h HostInfo, tool, text, reason string)   // отказ режима попадает в журнал
  }
  func Listen(port int) (net.Listener, error)   // 127.0.0.1:port
  func Serve(l net.Listener, token string, version string, b Backend) *http.Server
  func Prompt(port int, token string) string
  func AddCommand(port int, token string) string
  ```

- [ ] **Step 1: Тесты** (HTTP-клиент и клиент go-sdk на `httptest`-подобном слушателе, поддельный `Backend`). `TestRejectsMissingToken`, `TestRejectsWrongToken`, `TestRejectsForeignHostHeader`, `TestRejectsOrigin` (все 401/403 без вызова `Backend`); `TestListenLoopbackOnly`; `TestToolsListed` (ровно четыре имени); `TestHostByNameAndID`; `TestAmbiguousNameListsIDs`; `TestClosedHostLooksMissing` (тот же текст, что у несуществующего); `TestReadModeRefusesCommandWithoutRun`; `TestReadModeRefusesUpload`; `TestReadModeAllowsDownload`; `TestTimeoutClamped` (0 → 60 с, 9999 → 600 с); `TestTimedOutTextWarns` (текст ответа содержит «может ещё выполняться»); `TestPromptExact` (полный текст из спеки с подставленными портом и токеном); `TestAddCommandExact`.
- [ ] **Step 2:** `go get github.com/modelcontextprotocol/go-sdk@v1.8.0`; тесты падают.
- [ ] **Step 3: Реализовать.** `mcp.NewServer`, `mcp.AddTool` с типизированными входами, `mcp.NewStreamableHTTPHandler`, обёртка проверки (`Host` равен `127.0.0.1:<порт>` или `localhost:<порт>`, `Origin` пуст, токен через `subtle.ConstantTimeCompare`). Описания инструментов на английском, ошибки по-русски как обычный результат с `IsError`.
- [ ] **Step 4:** тесты проходят.
- [ ] **Step 5:** коммит `feat(mcp): server with host tools and token auth`.

### Task 5: Запрет папки данных

**Files:**
- Create: `app/mcp_paths.go`, `app/mcp_paths_test.go`

**Interfaces:**
- Consumes: `appdir.Dir()`.
- Produces: `func checkLocalPath(dataDir, path string) error`.

- [ ] **Step 1: Тест `TestCheckLocalPath`:** отказ для относительного пути; для `<dir>\vault.json`; другого регистра; `<dir>\sub\..\vault.json`; пути через символическую ссылку на `<dir>` (пропуск, если ОС не даёт создать ссылку); для несуществующего файла внутри папки-ссылки. Разрешён соседний каталог с общим префиксом `<dir>-other\x`.
- [ ] **Step 2:** тест падает.
- [ ] **Step 3: Реализовать:** `filepath.IsAbs`, `Clean`, `EvalSymlinks` ближайшего существующего родителя, сравнение `strings.EqualFold` по компонентам.
- [ ] **Step 4:** тест проходит.
- [ ] **Step 5:** коммит `feat(mcp): keep the data folder out of file transfers`.

### Task 6: Вход и пользователи подключения

**Files:**
- Create: `app/mcp_conn.go`, `app/mcp_conn_test.go`
- Modify: `app/app.go` (поля), `app/outstream.go` (`onOutput func()`), `app/trust_api.go`, `app/events.go` при необходимости

**Interfaces:**
- Consumes: `joinHostConn`, `hostConn.acquire`, `hostConn.leave`, `connAsker`, `newOutStreamFor`, `newInputQueue`.
- Produces:
  ```go
  type mcpHold struct{ hostID string; timer *time.Timer; ... }   // удержание на хост
  type mcpCall struct{ hostID string; cancel context.CancelFunc } // пользователь на время вызова
  func (a *App) mcpBegin(ctx context.Context, hostID string) (*ssh.Client, *mcpCall, error)
  func (a *App) mcpEnd(c *mcpCall)
  func (a *App) mcpReleaseHost(hostID string)   // отмена вызовов хоста и снятие удержания
  func (a *App) mcpReleaseAll()
  func (a *App) MCPLoginWrite(hostID, dataB64 string) error
  func (a *App) MCPLoginCancel(hostID string) error
  func (a *App) MCPLoginAttach(hostID string) error
  ```
  `trustOwner` получает поле `mcpHostID`; `TrustAskDTO` поле `MCPHostID` (`json:"mcp_host_id"`). События: `mcplogin:open` `{host_id, host_name}`, `mcplogin:close` `{host_id}`, вывод `mcplogin:data:<hostID>`.

- [ ] **Step 1: Тесты** (`sshtest`, зашифрованный ключ из `internal/keys/testdata`). `TestMCPConnectedHostNoLogin`; `TestMCPSecondCallNoPrompt`; `TestMCPHoldExpires` (таймер подменяемым полем `mcpHoldTTL`; соединение закрыто, секреты зачищены); `TestMCPTwoCallsOneLogin` (одно событие `mcplogin:open`, оба получают клиента); `TestMCPLoginOutlivesCall` (срок 2 минуты подменён на 50 мс: вызов возвращает `errMCPLoginPending`, ввод фразы завершает вход, повтор садится без вопроса); `TestMCPLoginCancelled`; `TestMCPTrustRefusedIsError`; `TestMCPHostKeyChanged`; `TestMCPWaitsForConsoleLogin`; `TestMCPNewCallAfterEditUsesNewParams` (выполняющийся вызов на старом подключении завершается, новый идёт на новое); `TestMCPReleaseHostKeepsConsole` (консоль хоста живёт); `TestMCPReleaseHostLastUserWipes`; `TestMCPDialogOnlyWhenInput` (ключ без фразы: нет `mcplogin:open`).
- [ ] **Step 2:** тесты падают.
- [ ] **Step 3: Реализовать.** На хост один фоновый вход `mcpLogin`: удержание вступает в подключение через `joinHostConn`, `acquire` идёт в его собственном контексте, а не в контексте вызова, поэтому отказ по 2 минутам вход не отменяет. Вызов: новый `*mcpCall` вступает через `joinHostConn` (всегда текущее подключение), затем `acquire`; если у подключения нет живого клиента, ждёт фоновый вход не дольше 2 минут. Каждый вызов продлевает удержание и снова вводит его через `joinHostConn`, что снимает его со `stale`-подключения. `outStream.onOutput` однократно вызывает `mcplogin:open` и поднимает окно (`mainWin.Restore`, `Focus`, nil в тестах). `mcpReleaseHost` пропускает, если `hcMu` уже взят: вызывается без него, как `closeHostConns`.
- [ ] **Step 4:** `go test ./app -race -run MCP` проходит.
- [ ] **Step 5:** коммит `feat(mcp): log in through the host connection`.

### Task 7: Backend и жизненный цикл

**Files:**
- Create: `app/mcp_backend.go`, `app/mcp_api.go`, `app/mcp_api_test.go`
- Modify: `app/app.go`, `app/vault_api.go`, `app/backup_api.go`, `app/hosts_api.go`, `internal/sshclient/neterr.go`

**Interfaces:**
- Consumes: `mcpBegin`, `mcpEnd`, `mcpReleaseHost`, `mcpReleaseAll`, `checkLocalPath`, `sshclient.Exec/Upload/Download`, `mcpserver.*`, журнал из Task 1.
- Produces: `type MCPStatusDTO struct{ Enabled bool; Port int; State, Error, Detail string }` (`State`: `off|running|error`); Wails-методы `MCPStatus() MCPStatusDTO`, `MCPSetEnabled(on bool) (MCPStatusDTO, error)`, `MCPSetPort(port int) (MCPStatusDTO, error)` (`field:port`), `MCPRotateToken() error`, `MCPAddCommand() (string, error)`, `MCPPrompt() (string, error)`, `MCPJournalList() []model.MCPRecord`, `MCPJournalClear() error`. Событие `mcp:status`. `sshclient.ListenError(addr, err) error` (экспорт существующей `listenError`).

- [ ] **Step 1: Тесты.** `TestMCPOffByDefault`; `TestMCPEnableCreatesTokenOnce`; `TestMCPDisableClosesPort`; `TestMCPPortBusyShowsError` (порт занят: `State == "error"`, текст по-русски, приложение цело; после освобождения `MCPSetEnabled(true)` успешен); `TestMCPRotateTokenInvalidatesOld`; `TestMCPLockedRefusesNewCalls` (новый вызов: «Клиент заблокирован: разблокируйте его», выполняющийся доводится, старт с заблокированным хранилищем не слушает порт, `VaultUnlock` запускает); `TestMCPRestoreStopsServer`; `TestMCPShutdownClosesPort`; `TestMCPJournalBeforeRun` (запись есть до начала выполнения, код выхода после); `TestMCPJournalFailureBlocksRun`; `TestMCPRefusalInJournal`; `TestMCPClosedHostCancelsRunning` (`HostSave` на закрытый); `TestMCPReadModeLetsRunningFinish`; `TestMCPCopyRefusedWhenOff`; `TestMCPTokenNeverInEventsOrJournal`.
- [ ] **Step 2:** тесты падают.
- [ ] **Step 3: Реализовать.** Токен держать в памяти (`a.mcpToken`) на время работы сервера. `a.mcpStart()` вызывается из `autostartOnce`-соседней точки при открытом хранилище; остановка: `http.Server.Close`, `mcpReleaseAll`. `ServiceShutdown` и `VaultRestore` останавливают до `closeAllHostConns`, восстановление запускает снова по настройкам новой копии. `HostSave`: если сохранённый режим стал `""`, `mcpReleaseHost`. `HostDelete`: `mcpReleaseHost`. `Backend.Run`: журнал, затем `mcpBegin`, `context.WithTimeout`, `Exec` с лимитом `200 << 10`, `mcpEnd`, `MCPJournalFinish`. Передача с таймаутом 30 минут и `checkLocalPath`.
- [ ] **Step 4:** `go test ./app ./internal/... -race` проходит.
- [ ] **Step 5:** коммит `feat(mcp): run the server from the app`.

### Task 8: Интерфейс

**Files:**
- Create: `frontend/src/lib/settings/McpSection.svelte`, `frontend/src/lib/settings/McpJournalDialog.svelte`, `frontend/src/lib/mcp/McpLoginDialog.svelte`, `frontend/src/lib/mcp/mcp.svelte.ts`, тесты рядом
- Modify: `frontend/src/lib/api.ts`, `frontend/src/lib/settings/SettingsView.svelte`, `frontend/src/lib/hosts/HostDialog.svelte`, `frontend/src/lib/forwards/MiniTerminal.svelte`, `frontend/src/lib/trust.svelte.ts`, `frontend/src/App.svelte`, `frontend/bindings/*`

**Interfaces:**
- Consumes: Wails-методы Task 6 и 7, события `mcp:status`, `mcplogin:open`, `mcplogin:close`, `mcplogin:data:<hostID>`.
- `MiniTerminal` получает необязательные `write` и `attach`, по умолчанию текущее поведение пробросов.

- [ ] **Step 1: Тесты.** `McpSection.test.ts`: тумблер вызывает `MCPSetEnabled`; кнопки копирования неактивны в выключенном состоянии и кладут результат `MCPPrompt`/`MCPAddCommand` в `ClipboardSetText`; ошибка статуса видна; токен нигде не отрисован; нет текста «Claude». `HostDialog.test.ts`: поле «Доступ по MCP» с тремя значениями, по умолчанию «Закрыт», для «Полный» подпись о правах пользователя хоста. `McpLoginDialog.test.ts`: `mcplogin:open` показывает «Вход по MCP: <хост>», Esc вызывает `MCPLoginCancel`, очередь из двух хостов. `trust`: вопрос с `mcp_host_id` показывается независимо от активной вкладки.
- [ ] **Step 2:** `npm test` падает.
- [ ] **Step 3: Реализовать.** Сгенерировать биндинги, добавить обёртки в `api.ts`, блок «Доступ по MCP» в `SettingsView` после «Хранилище и безопасность», диалоги через `ui/Dialog.svelte`, `McpLoginDialog` в `App.svelte` рядом с `TrustDialog`.
- [ ] **Step 4:** `npm test` и `npx svelte-check` проходят.
- [ ] **Step 5:** коммит `feat(mcp): settings, host access and login dialog`.

### Task 9: Документы и живая проверка

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1:** CLAUDE.md: в «Решения по продукту» пункт про MCP (режимы, токен, удержание, журнал, третий секрет-пользователь подключения, папка данных); в «Плане» уточнить, что SFTP вне v1 только как интерфейс; в «Поставке» новые зависимости не упоминать.
- [ ] **Step 2:** прогнать все проверки из Global Constraints; ожидается отсутствие ошибок.
- [ ] **Step 3:** живая проверка по памяти `ssh-client-ui-driving`: собрать, включить тумблер, вызвать `tools/list` и `list_hosts` через `curl` с токеном и без него (401). Реального хоста владельца без его участия не трогать.
- [ ] **Step 4:** коммит `docs(mcp): project rules for MCP access`.
