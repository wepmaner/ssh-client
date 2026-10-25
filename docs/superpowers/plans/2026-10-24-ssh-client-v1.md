# SSH-клиент v1: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Рабочий настольный SSH-клиент для Windows 11: хосты и группы, ключи (создание, импорт OpenSSH/PEM/PPK, экспорт), терминал с вкладками, пробросы L/R/D, зашифрованное хранилище с опциональным паролем, выпуск через лаунчер Stash (портативная папка, без установщика).

**Architecture:** Go-ядро (`internal/*`) без зависимости от UI: хранилище-конверт с AES-GCM и DPAPI/Argon2id, пакет ключей, SSH-клиент с PTY-сессиями и менеджер пробросов. Тонкий слой биндингов Wails (`app/*`) публикует методы во фронтенд и шлёт события. Фронтенд Svelte 5 + TypeScript повторяет макеты 1:1, терминал на xterm.js, все запросы паролей и подтверждения отпечатков идут текстом через поток терминала, а не отдельными диалогами.

**Tech Stack:** Go 1.23+, Wails v3 beta.25 (WebView2; переход с v2 — Task 13a), Svelte 5 + TypeScript + Vite, `@xterm/xterm` 6 (+ addon-webgl, addon-fit, addon-search), `golang.org/x/crypto/ssh` (+ `knownhosts`, `argon2`), `golang.org/x/sys/windows` (DPAPI, реестр), `github.com/kayrus/putty` (чтение .ppk), `github.com/liornabat/putty` (запись .ppk), `github.com/kevinburke/ssh_config`, `github.com/things-go/go-socks5`, `github.com/google/uuid`.

**Spec:** `CLAUDE.md` в корне проекта плюс макеты https://claude.ai/artifact/7cyxKFMe6YDkds18e7ZN4B (артборды: Main, AddServer, Terminal, Keys, KeyCreate, VaultEncrypt, Forwards, Settings, Empty, Error).

## Global Constraints

- Только Windows 10/11 x64; WebView2 Runtime считается установленным (входит в Windows 11).
- Пароли и парольные фразы нигде не хранятся и не запрашиваются в формах; только текстом в потоке терминала при подключении.
- Генерация ключей и соли только через `crypto/rand`; `math/rand` в `internal/store`, `internal/keys`, `internal/sshclient` запрещён (gosec G404 как ошибка).
- Ключ хоста: из хранилища приложения или импорт при создании хоста. ssh-agent, теги, пробросы и пароль в диалоге хоста отсутствуют.
- Хранилище: один файл `%LOCALAPPDATA%\sshclient\vault.json`; защита по умолчанию DPAPI, опционально пароль через Argon2id (time=3, memory=64 МиБ, threads=4) → KEK → обёртка случайного DEK (32 байта) → AES-256-GCM над данными.
- Поведение окна: безрамочное окно с собственным заголовком 40 px, кнопки окна 46 px, вкладки сессий в заголовке, окно открывается на прежнем месте и размере, без сплэша.
- Палитра и размеры из CLAUDE.md «Дизайн»: фон `#1a1d22`, заголовок `#121417`, боковая панель `#16181d`, карточки `#22262d`, терминал `#0f1114`, рамки `#2a2f37`, текст `#e8eaed`, приглушённый `#a3acb8`, акцент `#4f8ff7`, подключено `#3fb950`; радиусы 4 px контролы, 6–8 px карточки; высота контролов 32 px.
- Движение: диалог 150 мс с масштабом от 0,97, наведение 80 мс, вкладки без анимации, уважать `prefers-reduced-motion`.
- Вне первой версии: TUN-режим, разблокировка Windows Hello, разделение окна терминала. Их элементы в UI не показываются.

## Review Focus

1. Сервер с изменившимся ключом хоста: подключение должно быть отклонено с понятным объяснением, а не молча продолжено. Тест в Task 9.
2. Ключ, защищённый парольной фразой, при подключении: фраза запрашивается в терминале без эха, неверная фраза даёт повторный запрос, а не падение. Тест в Task 9.
3. Файл хранилища повреждён или от другой учётной записи Windows (DPAPI не расшифровывает): приложение сообщает об этом и предлагает начать заново с резервной копией, а не падает при старте. Тест в Task 4.
4. Импорт .ppk с парольной фразой и .ppk версии 2: фраза запрашивается, старый формат читается. Тест в Task 6.
5. Занятый локальный порт при старте проброса: проброс переходит в состояние ошибки с текстом, остальные продолжают работать. Тест в Task 12.

---

## Структура файлов

```
ssh-client/
  main.go                         запуск Wails, параметры окна, восстановление размера
  wails.json                      имя приложения sshclient, фронтенд frontend/
  go.mod                          module sshclient
  .golangci.yml                   gosec с G404 как ошибкой для internal/
  app/
    app.go                        App: startup/shutdown, открывает Vault, держит сервисы
    hosts_api.go                  методы Hosts* и Groups*
    keys_api.go                   методы Keys*
    sessions_api.go               Connect/Write/Resize/Disconnect, события term:*
    forwards_api.go               методы Forwards*
    vault_api.go                  состояние хранилища, включение/выключение пароля, разблокировка
    settings_api.go               настройки, импорт ssh_config и PuTTY
  internal/
    model/model.go                Host, Group, Key, Forward, Settings, VaultData
    store/
      crypto.go                   DeriveKEK, WrapDEK/UnwrapDEK, Seal/Open (AES-GCM)
      dpapi_windows.go            dpapiProtect/dpapiUnprotect
      envelope.go                 формат файла, Marshal/Unmarshal конверта
      vault.go                    Vault: Open, Unlock, Save, SetPassword, RemovePassword
      store.go                    Store: типизированные CRUD поверх VaultData
    keys/
      generate.go                 Generate(kind, comment)
      parse.go                    ParsePrivate(data, passphrase) для OpenSSH/PEM/PPK
      export.go                   PublicLine, PrivateOpenSSH, PrivatePPK
      fingerprint.go              FingerprintSHA256
    sshclient/
      prompter.go                 Prompter: вопросы/ответы через поток терминала
      knownhosts.go               KnownHosts: проверка, добавление, файл known_hosts
      client.go                   Dial(ctx, Target, Prompter) *ssh.Client
      session.go                  Session: PTY, Write, Resize, Close, канал вывода
      forward.go                  Forward: StartLocal/StartRemote/StartDynamic, Stop
      testserver_test.go          встроенный SSH-сервер для тестов
    appdir/appdir.go              путь к %LOCALAPPDATA%\sshclient
  frontend/
    src/
      App.svelte                  каркас: заголовок, боковая панель, текущий раздел, вкладки
      lib/api.ts                  обёртки над wailsjs/go/app/App и EventsOn
      lib/stores.ts               состояние: hosts, groups, keys, sessions, forwards, vault
      lib/ui/                     Button, Input, Select, Dialog, Segmented, StatusDot
      lib/titlebar/TitleBar.svelte
      lib/sidebar/Sidebar.svelte
      lib/hosts/HostsView.svelte, HostTile.svelte, HostDialog.svelte, EmptyState.svelte
      lib/keys/KeysView.svelte, KeyCreateDialog.svelte, KeyImportPanel.svelte
      lib/terminal/TerminalView.svelte, SearchBar.svelte, ErrorCard.svelte
      lib/forwards/ForwardsView.svelte, ForwardDialog.svelte
      lib/settings/SettingsView.svelte, VaultDialog.svelte, UnlockScreen.svelte
      styles/tokens.css           палитра и размеры из CLAUDE.md
  build/windows/installer/        NSIS по умолчанию от Wails
```

---

## Этап A. Каркас

### Task 1: Репозиторий, проект Wails, безрамочное окно

**Files:**
- Create: `main.go`, `wails.json`, `go.mod`, `.gitignore`, `.golangci.yml`, `frontend/*` (из шаблона `svelte-ts`), `frontend/src/styles/tokens.css`, `frontend/src/lib/titlebar/TitleBar.svelte`, `frontend/src/App.svelte`
- Create: `internal/appdir/appdir.go`, `internal/appdir/appdir_test.go`

**Interfaces:**
- Produces: `appdir.Dir() (string, error)` — `%LOCALAPPDATA%\sshclient`, создаёт каталог при первом вызове.
- Produces: `App` в `app/app.go` с методами `Startup(ctx)`, `Shutdown(ctx)`; поле `ctx context.Context`.

- [ ] **Step 1: Инициализировать репозиторий и проект**

```powershell
git init; git branch -M main
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails init -n sshclient -t svelte-ts -d .
```
После `wails init` переименовать модуль в `go.mod` на `sshclient`, фронтенд перевести на Svelte 5 (`npm i svelte@5 @sveltejs/vite-plugin-svelte@5`).

- [ ] **Step 2: Написать падающий тест `internal/appdir/appdir_test.go`**

```go
func TestDirUnderLocalAppData(t *testing.T) {
    t.Setenv("LOCALAPPDATA", t.TempDir())
    d, err := appdir.Dir()
    if err != nil { t.Fatal(err) }
    want := filepath.Join(os.Getenv("LOCALAPPDATA"), "sshclient")
    if d != want { t.Fatalf("got %s want %s", d, want) }
    if _, err := os.Stat(d); err != nil { t.Fatalf("dir not created: %v", err) }
}
```

- [ ] **Step 3: Запустить, убедиться что падает**

Run: `go test ./internal/appdir/ -run TestDirUnderLocalAppData -v`
Expected: FAIL, `undefined: appdir.Dir`

- [ ] **Step 4: Реализовать `appdir.Dir() (string, error)`**

`os.MkdirAll(path, 0o700)`; при пустом `LOCALAPPDATA` вернуть ошибку.

- [ ] **Step 5: Тест проходит**

Run: `go test ./internal/appdir/ -v`
Expected: PASS

- [ ] **Step 6: Настроить окно в `main.go`**

`wails.Run(&options.App{Title: "sshclient", Width: 1280, Height: 800, MinWidth: 900, MinHeight: 600, Frameless: true, BackgroundColour: &options.RGBA{R: 0x1a, G: 0x1d, B: 0x22, A: 255}, Windows: &windows.Options{DisableFramelessWindowDecorations: false, Theme: windows.Dark}, OnStartup: app.Startup, OnShutdown: app.Shutdown, Bind: []interface{}{app}})`.

- [ ] **Step 7: Собрать заголовок окна `TitleBar.svelte`**

Корень заголовка `height: 40px; --wails-draggable: drag`; кнопки и вкладки `--wails-draggable: no-drag`. Кнопки окна вызывают `WindowMinimise`, `WindowToggleMaximise`, `Quit` из `wailsjs/runtime`. Двойной щелчок по пустой области заголовка вызывает `WindowToggleMaximise`. Размеры и цвета из `tokens.css`.

- [ ] **Step 8: Настроить `.golangci.yml`**

Включить `gosec`; `G404` считается ошибкой; `internal/` без исключений. Добавить `golangci-lint run ./...` в `Makefile`/`package.json` скрипт `lint`.

- [ ] **Step 9: Проверить вручную**

Run: `wails dev`
Expected: открывается окно 1280×800 без системной рамки, с заголовком и тремя кнопками справа; перетаскивание за заголовок работает; крестик закрывает приложение; `golangci-lint run ./...` без ошибок.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "feat: scaffold Wails project with frameless window and app dir"
```

### Task 2: Модель данных и сохранение состояния окна

**Files:**
- Create: `internal/model/model.go`
- Modify: `app/app.go`, `main.go`

**Interfaces:**
- Produces (все с JSON-тегами в snake_case):
  - `type Host struct { ID, Name, Address string; Port int; User, GroupID, KeyID string; OS string; CreatedAt time.Time }` — `OS` одно из `ubuntu|debian|other`, нужен только для цвета монограммы.
  - `type Group struct { ID, Name string }`
  - `type Key struct { ID, Name, Type string; PrivatePEM []byte; PublicLine, Comment, Fingerprint, Source string; CreatedAt time.Time }` — `Type` одно из `ed25519|rsa`, `Source` одно из `generated|openssh|ppk`.
  - `type Forward struct { ID, Name, Kind, HostID, Listen, Target string; AutoStart bool }` — `Kind` одно из `L|R|D`; `Listen` и `Target` вида `host:port`, для `D` `Target` пустой.
  - `type Settings struct { TerminalFont string; TerminalSize int; CopyOnSelect, BlinkCursor, WebGL bool; LockAfterMin int; Window WindowState }`
  - `type WindowState struct { X, Y, W, H int; Maximized bool }`
  - `type VaultData struct { Hosts []Host; Groups []Group; Keys []Key; Forwards []Forward; Settings Settings }`
  - `func DefaultSettings() Settings` — `TerminalFont: "Cascadia Mono"`, `TerminalSize: 13`, все булевы `true` кроме `LockAfterMin: 0`, `Window: {W: 1280, H: 800}`.

- [ ] **Step 1: Написать `model.go` по интерфейсам выше и тест `TestDefaultSettings`** (проверяет шрифт, размер 13 и `WebGL == true`).

- [ ] **Step 2: Запустить тест, убедиться что проходит**

Run: `go test ./internal/model/ -v`
Expected: PASS

- [ ] **Step 3: Восстановление окна**

В `app.Startup` после открытия хранилища (Task 4) читать `Settings.Window`; если `W>0`, вызывать `runtime.WindowSetPosition` и `WindowSetSize`, при `Maximized` — `WindowMaximise`. В `app.Shutdown` сохранять текущие значения через `WindowGetPosition/WindowGetSize/WindowIsMaximised`. До Task 4 держать состояние в файле `window.json` в `appdir.Dir()` через те же структуры.

- [ ] **Step 4: Проверить вручную**

Run: `wails dev`; подвинуть и изменить окно, закрыть, запустить снова.
Expected: окно открывается там же и того же размера.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: data model and window state persistence"
```

---

## Этап B. Хранилище

### Task 3: Криптография конверта

**Files:**
- Create: `internal/store/crypto.go`, `internal/store/crypto_test.go`, `internal/store/envelope.go`, `internal/store/envelope_test.go`, `internal/store/dpapi_windows.go`

**Interfaces:**
- Produces:
  - `type KDFParams struct { Salt []byte; Time, MemoryKiB uint32; Threads uint8 }`
  - `func NewKDFParams() KDFParams` — соль 16 байт из `crypto/rand`, `Time 3`, `MemoryKiB 65536`, `Threads 4`.
  - `func DeriveKEK(password []byte, p KDFParams) []byte` — Argon2id, 32 байта.
  - `func NewDEK() ([]byte, error)` — 32 байта.
  - `func Seal(key, plaintext []byte) (nonce, ciphertext []byte, err error)` и `func Open(key, nonce, ciphertext []byte) ([]byte, error)` — AES-256-GCM, nonce 12 байт.
  - `type Envelope struct { Version int; Wrap string; KDF *KDFParams; DEKNonce, DEKEnc, Nonce, Data []byte }` — `Wrap` одно из `dpapi|password`; JSON с base64.
  - `func dpapiProtect(b []byte) ([]byte, error)`, `func dpapiUnprotect(b []byte) ([]byte, error)` — `windows.CryptProtectData` с флагом `CRYPTPROTECT_UI_FORBIDDEN`.
  - `var ErrWrongPassword = errors.New("store: wrong password")`, `var ErrCorrupt = errors.New("store: vault file is corrupt or belongs to another user")`.

- [ ] **Step 1: Написать падающие тесты `crypto_test.go`**

```go
func TestSealOpenRoundTrip(t *testing.T) {
    key, _ := store.NewDEK()
    n, c, err := store.Seal(key, []byte("hello"))
    if err != nil { t.Fatal(err) }
    p, err := store.Open(key, n, c)
    if err != nil || string(p) != "hello" { t.Fatalf("got %q %v", p, err) }
}
func TestOpenWithWrongKeyFails(t *testing.T) { /* другой ключ → err != nil */ }
func TestDeriveKEKDeterministic(t *testing.T) {
    p := store.NewKDFParams()
    a := store.DeriveKEK([]byte("pw"), p); b := store.DeriveKEK([]byte("pw"), p)
    if !bytes.Equal(a, b) || len(a) != 32 { t.Fatal("kek must be stable, 32 bytes") }
    if bytes.Equal(a, store.DeriveKEK([]byte("pw2"), p)) { t.Fatal("different passwords same kek") }
}
func TestKDFParamsDefaults(t *testing.T) { /* Time==3, MemoryKiB==65536, Threads==4, len(Salt)==16 */ }
func TestDPAPIRoundTrip(t *testing.T) { /* protect → unprotect == исходное; порченые байты → ошибка */ }
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `go test ./internal/store/ -v`
Expected: FAIL, undefined functions

- [ ] **Step 3: Реализовать `crypto.go`, `dpapi_windows.go`, `envelope.go`**

Argon2id через `golang.org/x/crypto/argon2.IDKey`. Envelope: `MarshalEnvelope(e) ([]byte, error)`, `UnmarshalEnvelope(b) (Envelope, error)`; неизвестная версия или отсутствующие поля → `ErrCorrupt`.

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): envelope crypto with Argon2id, AES-GCM and DPAPI"
```

### Task 4: Vault и Store

**Files:**
- Create: `internal/store/vault.go`, `internal/store/vault_test.go`, `internal/store/store.go`, `internal/store/store_test.go`
- Modify: `app/app.go`

**Interfaces:**
- Consumes: Task 3.
- Produces:
  - `type Vault struct` с методами:
    - `func OpenVault(path string) (*Vault, error)` — читает файл; если файла нет, создаёт новый с `Wrap: dpapi` и пустым `VaultData` с `DefaultSettings()`; если `Wrap == dpapi`, сразу расшифровывает; если `password`, остаётся заблокированным.
    - `func (v *Vault) Locked() bool`
    - `func (v *Vault) Unlock(password []byte) error` — `ErrWrongPassword` при неверном.
    - `func (v *Vault) Data() *model.VaultData` — nil пока заблокирован.
    - `func (v *Vault) Save() error` — атомарная запись: во временный файл рядом, затем `os.Rename`; перед записью копия прошлой версии в `vault.json.bak`.
    - `func (v *Vault) SetPassword(password []byte) error` — переобёртывает тот же DEK паролем; данные не перешифровываются.
    - `func (v *Vault) RemovePassword() error` — обратно на DPAPI.
    - `func (v *Vault) HasPassword() bool`
  - `type Store struct` поверх `*Vault`: `Hosts() []model.Host`, `SaveHost(h model.Host) (model.Host, error)` (пустой `ID` → новый uuid, пустое `Name` → `Address`), `DeleteHost(id) error`, аналогично `Groups`, `Keys`, `Forwards`, `Settings() model.Settings`, `SaveSettings(model.Settings) error`. Каждая запись вызывает `Vault.Save()`.
  - `func (s *Store) HostsUsingKey(keyID string) []model.Host`.
  - `DeleteKey` возвращает ошибку `ErrKeyInUse`, если ключ привязан к хостам.

- [ ] **Step 1: Написать падающие тесты `vault_test.go`**

```go
func TestOpenCreatesNewDPAPIVault(t *testing.T)          // файла нет → Locked()==false, Data() с DefaultSettings, файл появился
func TestSetPasswordLocksOnReopen(t *testing.T)           // SetPassword("pw"); Save; OpenVault → Locked()==true; Unlock("bad")→ErrWrongPassword; Unlock("pw")→nil, данные те же
func TestSetPasswordKeepsDEK(t *testing.T)                // Data байты до и после SetPassword совпадают после перечтения
func TestCorruptFileReturnsErrCorrupt(t *testing.T)       // записать мусор в путь → OpenVault возвращает ErrCorrupt (errors.Is)
func TestSaveIsAtomicAndKeepsBackup(t *testing.T)         // после двух Save существует vault.json.bak с прошлым содержимым
```

Тесты `store_test.go`: `TestSaveHostAssignsIDAndDefaultName`, `TestDeleteKeyInUseFails`, `TestHostsUsingKey`.

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `go test ./internal/store/ -v`
Expected: FAIL

- [ ] **Step 3: Реализовать `vault.go` и `store.go`**

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Подключить в `app.Startup`**

`OpenVault(filepath.Join(appdir.Dir(), "vault.json"))`; при `ErrCorrupt` приложение показывает экран с текстом ошибки и кнопкой «Начать заново (старый файл сохранён как vault.json.corrupt)». Перенести состояние окна из Task 2 в `Settings.Window`.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(store): vault with optional password and typed store"
```

### Task 5: API хранилища и экран разблокировки

**Files:**
- Create: `app/vault_api.go`, `app/settings_api.go`, `frontend/src/lib/api.ts`, `frontend/src/lib/stores.ts`, `frontend/src/lib/settings/UnlockScreen.svelte`, `frontend/src/lib/settings/VaultDialog.svelte`

**Interfaces:**
- Produces (методы `App`, видны во фронтенде через `wailsjs/go/app/App`):
  - `VaultState() VaultStateDTO` — `{Locked bool; HasPassword bool; Error string}`.
  - `VaultUnlock(password string) error`
  - `VaultSetPassword(password string) error`, `VaultRemovePassword(password string) error` (проверяет старый пароль через `Unlock` на копии).
  - `GetSettings() model.Settings`, `SaveSettings(model.Settings) error`.
- Фронтенд: `stores.ts` экспортирует `vault`, `settings` (Svelte 5 `$state` в модуле) и функцию `refreshAll()`.

- [ ] **Step 1: Реализовать методы API и типы DTO**

- [ ] **Step 2: Экран разблокировки `UnlockScreen.svelte`**

Показывается вместо каркаса, пока `VaultState().Locked`: заголовок приложения, поле пароля в фокусе, кнопка «Разблокировать», текст ошибки «Неверный пароль» под полем. Без побочных элементов.

- [ ] **Step 3: Диалог `VaultDialog.svelte` по артборду VaultEncrypt**

Пароль + повтор, индикатор из четырёх делений (длина ≥ 12 и три класса символов = 4 деления), предупреждение о невосстановимости, подвал «Будет зашифровано: N ключей, N хостов». Галочка Windows Hello не отображается (вне v1). Кнопка активна только при совпадении паролей длиной ≥ 8.

- [ ] **Step 4: Проверить вручную**

Run: `wails dev`; включить шифрование, перезапустить приложение.
Expected: появляется экран разблокировки; неверный пароль даёт ошибку; верный открывает приложение с теми же данными.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: vault API, unlock screen and encryption dialog"
```

---

## Этап C. Ключи

### Task 6: Пакет ключей

**Files:**
- Create: `internal/keys/generate.go`, `internal/keys/parse.go`, `internal/keys/export.go`, `internal/keys/fingerprint.go`, `internal/keys/keys_test.go`, `internal/keys/testdata/` (ed25519 OpenSSH без фразы, RSA PEM, `v2.ppk`, `v3.ppk` с фразой `test`, `v3-plain.ppk`)

**Interfaces:**
- Produces:
  - `func Generate(kind string, comment string) (model.Key, error)` — `kind` `ed25519|rsa`; RSA 4096; `PrivatePEM` в формате OpenSSH (`ssh.MarshalPrivateKey`), `PublicLine` вида `ssh-ed25519 AAAA… comment`, `Source: generated`.
  - `var ErrPassphraseRequired = errors.New("keys: passphrase required")`
  - `func ParsePrivate(data []byte, passphrase []byte) (model.Key, error)` — определяет формат по заголовку: `-----BEGIN OPENSSH PRIVATE KEY-----`/PEM → `ssh.ParseRawPrivateKey[WithPassphrase]`, `PuTTY-User-Key-File-` → `kayrus/putty`; при необходимости фразы без неё возвращает `ErrPassphraseRequired`; результат всегда перекодирован в OpenSSH без фразы; `Source` `openssh|ppk`; `Comment` из ключа; `Type` только `ed25519|rsa`, иначе ошибка `ErrUnsupportedKeyType`. **Отменено решением от 2026-10-24: см. Task 9b, фраза ключа сохраняется.**
  - `func PublicLine(k model.Key) string`
  - `func PrivateOpenSSH(k model.Key) []byte`
  - `func PrivatePPK(k model.Key) ([]byte, error)` — PPK v3 через `liornabat/putty`, без фразы.
  - `func FingerprintSHA256(pub ssh.PublicKey) string` — `SHA256:` + base64 без паддинга.

- [ ] **Step 1: Написать падающие тесты**

```go
func TestGenerateEd25519(t *testing.T)            // Type, PublicLine начинается с "ssh-ed25519 ", Fingerprint с "SHA256:", ParsePrivate(PrivatePEM) возвращает тот же Fingerprint
func TestGenerateRSAIs4096(t *testing.T)          // размер модуля 4096
func TestParseOpenSSHNoPassphrase(t *testing.T)
func TestParsePEMRSA(t *testing.T)
func TestParsePPKv3NeedsPassphrase(t *testing.T)  // без фразы → ErrPassphraseRequired; с "test" → ok, Source=="ppk"
func TestParsePPKv2(t *testing.T)
func TestParseECDSAUnsupported(t *testing.T)      // ErrUnsupportedKeyType
func TestExportPPKRoundTrip(t *testing.T)         // PrivatePPK → ParsePrivate → тот же Fingerprint
func TestGenerateUsesCryptoRand(t *testing.T)     // два вызова Generate дают разные Fingerprint
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `go test ./internal/keys/ -v`
Expected: FAIL

- [ ] **Step 3: Реализовать пакет**

Тестовые .ppk сгенерировать один раз `puttygen` и положить в `testdata/` с фразой `test`.

- [ ] **Step 4: Тесты проходят; gosec чист**

Run: `go test ./internal/keys/ -v && golangci-lint run ./internal/...`
Expected: PASS, 0 issues

- [ ] **Step 5: Commit**

```bash
git add internal/keys
git commit -m "feat(keys): generate, parse OpenSSH/PEM/PPK, export, fingerprint"
```

### Task 7: API ключей и раздел «Ключи»

**Files:**
- Create: `app/keys_api.go`, `frontend/src/lib/keys/KeysView.svelte`, `frontend/src/lib/keys/KeyCreateDialog.svelte`, `frontend/src/lib/keys/KeyImportPanel.svelte`, `frontend/src/lib/ui/*`

**Interfaces:**
- Consumes: Task 4 `Store`, Task 6.
- Produces (методы `App`):
  - `KeysList() []KeyDTO` — `{ID, Name, Type, PublicLine, Comment, Fingerprint, Source string; CreatedAt string; HostIDs []string}`; приватная часть во фронтенд никогда не передаётся.
  - `KeyGenerate(name, kind, comment string) (KeyDTO, error)`
  - `KeyImportFile() (KeyDTO, error)` — открывает системный диалог `runtime.OpenFileDialog` с фильтром `*.ppk;*.pem;id_*;*`, имя ключа = имя файла без расширения; при `ErrPassphraseRequired` возвращает ошибку с кодом `passphrase_required`, фронтенд запрашивает фразу маленьким диалогом внутри карточки импорта и повторяет через `KeyImportFileWithPassphrase(path, passphrase)`.
  - `KeyImportText(text, name string) (KeyDTO, error)` — имя по умолчанию `Comment`, иначе `key-<дата>`.
  - `KeyRename(id, name string) error`, `KeyDelete(id string) error` (ошибка `key_in_use` с именами хостов).
  - `KeyCopyPublic(id string) error` — `runtime.ClipboardSetText`.
  - `KeyExport(id, format string) error` — `format` `public|openssh|ppk`; системный диалог сохранения; при включённом пароле хранилища сначала `VaultUnlock` через диалог во фронтенде.
- Компоненты `lib/ui`: `Button` (primary/outline/ghost), `Input`, `Select`, `Segmented`, `Dialog` (центр, 150 мс масштаб от 0,97, `prefers-reduced-motion` отключает), `StatusDot`.

- [ ] **Step 1: Реализовать `keys_api.go`**

- [ ] **Step 2: Собрать `KeysView.svelte` по артборду Keys**

Тулбар, карточка состояния хранилища (жёлтый замок если `!HasPassword`, зелёный если пароль включён, кнопка ведёт в `VaultDialog`), список 340 px, карточка ключа с экспортом.

- [ ] **Step 3: Собрать `KeyCreateDialog.svelte` по артборду KeyCreate**

- [ ] **Step 4: Проверить вручную**

Run: `wails dev`
Expected: создание Ed25519 и RSA, импорт .ppk с фразой (фраза запрашивается, после импорта ключ без фразы), копирование публичного ключа в буфер, экспорт в .ppk и его чтение в PuTTYgen, удаление ключа, привязанного к хосту, блокируется с сообщением.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: keys API and Keys screen"
```

---

## Этап C′. Переделка: парольная фраза ключа сохраняется

### Task 9b: Ключи хранятся с собственной парольной фразой

Решение владельца продукта (2026-10-24): фраза ключа — последний рубеж, приложение её не снимает и не сохраняет. Выполняется после Task 10, до Task 11.

**Files:**
- Modify: `internal/model/model.go` (`Key`: `PrivatePEM` → `Private []byte` исходные байты; новые поля `Format string` ∈ `openssh|pem|ppk`, `Encrypted bool`)
- Modify: `internal/keys/parse.go`, `export.go`, `generate.go`, `keys_test.go`; Create: `internal/keys/signer.go`
- Modify: `internal/sshclient/client.go` (`passphraseSigners` использует `keys.Signer`), `client_test.go`
- Modify: `app/keys_api.go`, `app/keys_api_test.go`, `frontend/src/lib/keys/KeyImportPanel.svelte`, `KeyCreateDialog.svelte`, `KeyDetail.svelte`

**Interfaces:**
- `keys.Inspect(data []byte) (model.Key, error)` — без фразы: определяет `Format`, `Encrypted`, извлекает `PublicLine`, `Fingerprint`, `Comment`, `Type` из открытой части (OpenSSH-контейнер и PPK хранят публичный ключ открыто); для старого зашифрованного PEM (`Proc-Type: 4,ENCRYPTED`) без открытой части возвращает `ErrPassphraseRequired`, тогда вызывается `InspectWithPassphrase(data, passphrase)` — фраза используется только для чтения публичного ключа и не сохраняется; `Private` = исходные байты без изменений.
- `keys.Signer(k model.Key, passphrase []byte) (ssh.Signer, error)` — разбирает `k.Private` по `k.Format`; `Encrypted && len(passphrase)==0` → `ErrPassphraseRequired`; неверная фраза → `ErrWrongPassphrase` (для PPK по MAC).
- `keys.Generate(kind, comment string, passphrase []byte) (model.Key, error)` — при непустой фразе `ssh.MarshalPrivateKeyWithPassphrase`, `Encrypted=true`.
- `keys.PrivateOpenSSH(k)` и `keys.PrivatePPK(k)` только для `!k.Encrypted`; для зашифрованного экспорт — `k.Private` как есть, в исходном формате (`keys.PrivateOriginal(k) ([]byte, ext string)`).
- `sshclient.Dial`: цикл фразы (до трёх `AskSecret("Парольная фраза ключа <name>: ")`) через `keys.Signer`; работает для всех трёх форматов.
- `app`: `KeyImportFile/Text` больше не возвращают `passphrase_required:` для OpenSSH/PPK; только для старого PEM; `KeyGenerate(name, kind, comment, passphrase string)`; `KeyDTO` получает `Encrypted bool`, `Format string`; `KeyExport` для зашифрованного ключа принимает только `format == "original"`.
- UI: панель импорта без шага фразы (кроме старого PEM); диалог создания с необязательным полем «Парольная фраза» и повтором; карточка ключа показывает бейдж «с парольной фразой» и в экспорте для зашифрованного — одну кнопку «Приватный ключ (исходный файл)»; в хранении: «Фраза спрашивается при подключении».

- [ ] **Step 1: Тесты** — `TestInspectOpenSSHEncryptedWithoutPassphrase` (fingerprint из открытой части, `Encrypted==true`), `TestInspectPPKEncrypted`, `TestInspectLegacyPEMNeedsPassphrase`, `TestSignerWrongPassphrase` (OpenSSH и PPK), `TestSignerCorrectPassphrase`, `TestGenerateWithPassphrase` (Signer без фразы → `ErrPassphraseRequired`), `TestExportEncryptedKeepsOriginalBytes`; в `client_test.go` тест с PPK-ключом с фразой при подключении.
- [ ] **Step 2: Реализация и миграция хранилища** — при открытии хранилища ключи без `Format` считаются `openssh`, `Encrypted=false`.
- [ ] **Step 3: `go test ./... -count=1`, `golangci-lint run ./...`, `npm test`, `svelte-check`, `wails build`; commit.**

## Этап D. SSH и терминал

### Task 8: Prompter и known_hosts

**Files:**
- Create: `internal/sshclient/prompter.go`, `internal/sshclient/knownhosts.go`, `internal/sshclient/prompter_test.go`, `internal/sshclient/knownhosts_test.go`

**Interfaces:**
- Produces:
  - `type Prompter struct` — создаётся `NewPrompter(out io.Writer, in io.Reader)`; методы `Ask(prompt string) (string, error)` (с эхом), `AskSecret(prompt string) (string, error)` (без эха, печатает `\r\n` после ввода), `Confirm(prompt string) (bool, error)` (принимает `yes|y|да|д` / `no|n|нет|н`, иначе повторяет вопрос). Читает строки до `\r` или `\n`, поддерживает Backspace (`\x7f`, `\b`), Ctrl+C возвращает `ErrCancelled`.
  - `type KnownHosts struct` — `OpenKnownHosts(path string) (*KnownHosts, error)` (создаёт файл); `Callback(p *Prompter) ssh.HostKeyCallback`: неизвестный хост → печатает `Сервер <host> неизвестен.\r\nОтпечаток <SHA256:…>\r\nДоверять и запомнить?` и при согласии дописывает строку в файл, при отказе возвращает `ErrCancelled`; изменившийся ключ → возвращает `ErrHostKeyChanged` с текстом про возможную подмену и путём к файлу `known_hosts`, продолжить не предлагает. Поведение не настраивается.

- [ ] **Step 1: Написать падающие тесты**

```go
func TestAskSecretDoesNotEcho(t *testing.T)       // in: "pw\r"; out не содержит "pw", содержит prompt
func TestAskHandlesBackspace(t *testing.T)        // "abc\x7fd\n" → "abd"
func TestConfirmRussianYes(t *testing.T)          // "да\n" → true
func TestConfirmRepeatsOnGarbage(t *testing.T)    // "maybe\nn\n" → false, prompt напечатан дважды
func TestCtrlCCancels(t *testing.T)               // "\x03" → ErrCancelled
func TestUnknownHostAddedAfterYes(t *testing.T)   // Callback с in="yes\n" → nil, файл содержит host
func TestUnknownHostRejectedAfterNo(t *testing.T)
func TestChangedKeyRejected(t *testing.T)         // файл с другим ключом → ErrHostKeyChanged, prompt не задаётся
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `go test ./internal/sshclient/ -run 'Prompter|Host' -v`
Expected: FAIL

- [ ] **Step 3: Реализовать**

`knownhosts` на `golang.org/x/crypto/ssh/knownhosts`; ошибка `*knownhosts.KeyError` с пустым `Want` = неизвестный, с непустым = изменившийся.

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/sshclient/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sshclient
git commit -m "feat(ssh): terminal prompter and known_hosts policy"
```

### Task 9: Dial и PTY-сессия

**Files:**
- Create: `internal/sshclient/client.go`, `internal/sshclient/session.go`, `internal/sshclient/testserver_test.go`, `internal/sshclient/client_test.go`, `internal/sshclient/session_test.go`

**Interfaces:**
- Consumes: Task 8, `model.Key`.
- Produces:
  - `type Target struct { Address string; Port int; User string; Key *model.Key }`
  - `func Dial(ctx context.Context, t Target, p *Prompter, kh *KnownHosts) (*ssh.Client, error)` — порядок аутентификации: `PublicKeys` из `t.Key` (если ключ требует фразу, `AskSecret("Парольная фраза ключа <name>: ")`, до трёх попыток), затем `KeyboardInteractive` и `PasswordCallback`, оба через `p.AskSecret` с подсказкой сервера; таймаут соединения 10 с; при ошибке таймаута текст `Сервер не ответил за 10 секунд`.
  - `type Session struct` — `NewSession(client *ssh.Client, cols, rows int, term string) (*Session, error)` запрашивает PTY `xterm-256color` и shell; `Output() <-chan []byte`; `Write(b []byte) error`; `Resize(cols, rows int) error`; `Wait() error` (код выхода в `*ssh.ExitError`); `Close() error`.
  - `var ErrCancelled`, `var ErrHostKeyChanged`, `var ErrTimeout`.
- Тестовый сервер `testserver_test.go`: `startTestServer(t, opts)` на `127.0.0.1:0` на `golang.org/x/crypto/ssh` с настраиваемыми `PublicKeyCallback`, `PasswordCallback`, поддержкой `pty-req`, `shell` (эхо-shell: печатает `ok\r\n` и повторяет ввод) и `window-change`.

- [ ] **Step 1: Написать падающие тесты**

```go
func TestDialWithKey(t *testing.T)                    // сервер принимает публичный ключ → nil err
func TestDialPassphraseKeyAsksInTerminal(t *testing.T) // ключ с фразой в тесте (ssh.MarshalPrivateKeyWithPassphrase); in: "wrong\n" "right\n" → подключение успешно, prompt напечатан дважды, фразы нет в out
func TestDialFallsBackToPassword(t *testing.T)        // сервер отвергает ключ, принимает пароль "s3cret"; in "s3cret\n"
func TestDialChangedHostKeyRefused(t *testing.T)      // errors.Is ErrHostKeyChanged, соединение не установлено
func TestDialTimeoutMessage(t *testing.T)             // адрес 10.255.255.1 с ctx timeout 300ms → ErrTimeout
func TestSessionEchoAndResize(t *testing.T)           // Write("ls\r") → Output содержит "ls"; Resize(100,30) → сервер получил window-change
func TestSessionExitCode(t *testing.T)                // сервер закрывает канал с кодом 3 → Wait возвращает ExitError 3
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `go test ./internal/sshclient/ -v`
Expected: FAIL

- [ ] **Step 3: Реализовать `client.go`, `session.go`, тестовый сервер**

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/sshclient/ -race -v`
Expected: PASS, без data race

- [ ] **Step 5: Commit**

```bash
git add internal/sshclient
git commit -m "feat(ssh): dial with terminal prompts and PTY session"
```

### Task 10: API сессий и экран терминала

**Files:**
- Create: `app/sessions_api.go`, `frontend/src/lib/terminal/TerminalView.svelte`, `frontend/src/lib/terminal/SearchBar.svelte`, `frontend/src/lib/terminal/ErrorCard.svelte`
- Modify: `frontend/src/App.svelte`, `frontend/src/lib/titlebar/TitleBar.svelte`

**Interfaces:**
- Consumes: Task 9, Task 4.
- Produces (методы `App`):
  - `SessionOpen(hostID string, cols, rows int) (string, error)` — создаёт id сессии, сразу возвращает его; в горутине: `EventsEmit("term:state:"+id, "connecting")`, печатает в поток `Подключение к user@host:port …\r\n`, `Dial` с `Prompter`, чей `in` питается от `SessionWrite`, затем `NewSession`; вывод шлёт `EventsEmit("term:data:"+id, base64)` пакетами не чаще раза в 8 мс; по завершении `term:state` = `closed` с полем `exitCode`, при ошибке `error` с `{message, detail, retryable}`.
  - `SessionWrite(id string, dataB64 string) error`, `SessionResize(id string, cols, rows int) error`, `SessionClose(id string) error`, `SessionList() []SessionDTO` — `{ID, HostID, HostName, State string; LatencyMs int}`; латентность измеряется `client.SendRequest("keepalive@openssh.com")` раз в 15 с.
- Фронтенд:
  - `TerminalView.svelte`: `@xterm/xterm` с темой из `tokens.css` (фон `#0f1114`), `addon-webgl` если `Settings.WebGL` (с откатом на canvas при ошибке контекста), `addon-fit` на `ResizeObserver` → `SessionResize`, `addon-search` для `SearchBar`. Ввод `onData` → `SessionWrite`. Ctrl+Shift+C копирует, Ctrl+Shift+V вставляет, правая кнопка вставляет, выделение копирует при `CopyOnSelect`. Ctrl+F открывает `SearchBar`.
  - Вкладки сессий в `TitleBar`: моноширинное имя, точка состояния (`connecting` серая, `connected` зелёная, `error` красная), крестик, Ctrl+Tab переключает, Ctrl+W закрывает вкладку сразу, без подтверждения.
  - `ErrorCard.svelte` по артборду Error внутри области терминала при `state == error`: заголовок, объяснение (`Сервер не ответил…` для `ErrTimeout`, текст про подмену для `ErrHostKeyChanged`, `Доступ запрещён` для auth), детали моноширинным, «Повторить», «Открыть хост», «Скопировать ошибку».
  - Строка состояния: «Подключено · N мс», адрес, имя ключа, кодировка `UTF-8`, размер `cols×rows`.

- [ ] **Step 1: Реализовать `sessions_api.go`**

- [ ] **Step 2: Собрать `TerminalView.svelte`, `SearchBar.svelte`, `ErrorCard.svelte`, вкладки**

- [ ] **Step 3: Проверить вручную против реального сервера**

Run: `wails dev`
Expected: подключение к хосту с ключом открывает shell за один шаг; первый вход спрашивает доверие отпечатку в терминале; `vim`, `htop` и `tmux` рисуются корректно; изменение размера окна меняет размер PTY; поиск находит и подсвечивает; при выключенном сервере через 10 с появляется карточка ошибки с «Повторить»; закрытие вкладки обрывает сессию.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: sessions API, xterm terminal, tabs and error card"
```

---

## Этап E. Хосты

### Task 11: API хостов и экраны «Хосты», «Новый хост», «Первый запуск»

**Files:**
- Create: `app/hosts_api.go`, `frontend/src/lib/hosts/HostsView.svelte`, `HostTile.svelte`, `HostDialog.svelte`, `EmptyState.svelte`, `frontend/src/lib/sidebar/Sidebar.svelte`

**Interfaces:**
- Consumes: Task 4, Task 7 (`KeyImportFile`, `KeyImportText`).
- Produces (методы `App`):
  - `HostsList() []HostDTO` — `{ID, Name, Address string; Port int; User, GroupID, GroupName, KeyID, KeyName, OS string; Connected bool}`.
  - `HostSave(h HostDTO) (HostDTO, error)` — валидация: адрес непустой, порт 1–65535, пользователь непустой, `KeyID` непустой; ошибки с кодом поля (`address|port|user|key`) и русским текстом.
  - `HostDelete(id string) error` — закрывает его сессии и останавливает его пробросы.
  - `GroupsList() []GroupDTO` (`{ID, Name string; HostCount, ConnectedCount int}`), `GroupSave(name string) (GroupDTO, error)`, `GroupRename`, `GroupDelete` (хосты остаются без группы).
  - `HostSearch` не нужен: поиск во фронтенде по `Name`, `Address`, `User`.
- Фронтенд:
  - `HostsView.svelte` по артборду Main: тулбар, группы, плитки 64 px с монограммой по `OS` (`ubuntu` `#b9521c` «Ub», `debian` `#b8405a` «Db», иначе `#1f8a80` две первые буквы имени), наведение показывает «Подключить» и «Ещё» (меню: Изменить, Дублировать, Удалить). Щелчок по плитке = `SessionOpen`. Переключатель плитки/список хранится в `localStorage`. Ctrl+K фокусирует поиск.
  - `HostDialog.svelte` по артборду AddServer: поля и карточка «Приватный ключ» с переключателем «Из хранилища / Импортировать»; импорт вызывает `KeyImportFile`/`KeyImportText` и подставляет созданный ключ в `KeyID`; при `passphrase_required` запрашивает фразу внутри карточки. Кнопки «Сохранить» и «Подключиться» (`HostSave` затем `SessionOpen`). Поле «Адрес» в фокусе при открытии; Esc закрывает; Enter в полях = «Подключиться».
  - `EmptyState.svelte` по артборду Empty при нулевом числе хостов.
  - `Sidebar.svelte`: Хосты (счётчик), Ключи (счётчик), Пробросы (точка при активных), внизу Настройки.

- [ ] **Step 1: Реализовать `hosts_api.go` с тестом валидации `app/hosts_api_test.go`**

```go
func TestHostSaveValidation(t *testing.T) // порт 70000 → ошибка поля "port"; пустой KeyID → "key"; пустое имя → Name == Address
```

- [ ] **Step 2: Собрать экраны**

- [ ] **Step 3: Проверить вручную**

Run: `wails dev`
Expected: пустое хранилище показывает «Первый запуск»; создание хоста с импортом .ppk из диалога подключает за один шаг; группы считают хосты; удаление хоста закрывает его вкладку.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: hosts API, Hosts screen, host dialog and empty state"
```

---

## Этап F. Пробросы

### Task 12: Менеджер пробросов

**Files:**
- Create: `internal/sshclient/forward.go`, `internal/sshclient/forward_test.go`

**Interfaces:**
- Consumes: Task 9 `Dial`.
- Produces:
  - `type ForwardRunner struct` — `NewForwardRunner(f model.Forward, dial func(ctx) (*ssh.Client, error)) *ForwardRunner`; `Start(ctx) error` (возвращает ошибку сразу, если локальный порт занят или dial не удался), `Stop()`, `State() ForwardState` (`{Status string; Error string; Conns int}` где `Status` `stopped|starting|running|error`), `Events() <-chan ForwardState`.
  - `L`: `net.Listen` на `Listen`, каждое соединение → `client.Dial("tcp", Target)` и двусторонний `io.Copy`.
  - `R`: `client.Listen("tcp", Listen)` на сервере, каждое соединение → `net.Dial("tcp", Target)` локально.
  - `D`: SOCKS5 на `Listen` через `things-go/go-socks5` с `Dial` через `client.Dial`.
  - Переподключение: при обрыве `client` статус `starting`, повтор через 2, 4, 8… до 60 с, пока не `Stop()`.

- [ ] **Step 1: Написать падающие тесты (на тестовом сервере Task 9, который умеет `direct-tcpip` и `tcpip-forward`)**

```go
func TestLocalForwardProxiesBytes(t *testing.T)    // локальный echo-сервер за "сервером"; через Listen получаем эхо
func TestLocalForwardPortBusy(t *testing.T)        // занять порт заранее → Start возвращает ошибку с текстом порта, State().Status=="error"
func TestDynamicForwardSocks5(t *testing.T)        // golang.org/x/net/proxy.SOCKS5 через наш порт достигает echo
func TestRemoteForward(t *testing.T)
func TestStopClosesListener(t *testing.T)          // после Stop порт свободен
func TestReconnectAfterDrop(t *testing.T)          // сервер рвёт соединение → статус starting → running
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `go test ./internal/sshclient/ -run Forward -v`
Expected: FAIL

- [ ] **Step 3: Реализовать `forward.go` и расширить тестовый сервер**

- [ ] **Step 4: Тесты проходят**

Run: `go test ./internal/sshclient/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sshclient
git commit -m "feat(ssh): local, remote and dynamic forwards with reconnect"
```

### Task 13: API пробросов и экран «Пробросы»

**Files:**
- Create: `app/forwards_api.go`, `frontend/src/lib/forwards/ForwardsView.svelte`, `ForwardDialog.svelte`
- Modify: `app/app.go` (автозапуск), `frontend/src/lib/terminal/TerminalView.svelte` (счётчик в заголовке)

**Interfaces:**
- Consumes: Task 12, Task 4.
- Produces (методы `App`):
  - `ForwardsList() []ForwardDTO` — `{ID, Name, Kind, HostID, HostName, Listen, Target string; AutoStart bool; Status, Error string; Conns int}`.
  - `ForwardSave(f ForwardDTO) (ForwardDTO, error)` — валидация адресов `host:port`; имя по умолчанию `<Kind> <Listen> → <Target>` (для `D` `SOCKS5 <Listen>`).
  - `ForwardDelete(id) error` (останавливает), `ForwardStart(id) error`, `ForwardStop(id) error`.
  - События `forward:state:<id>` с `ForwardState`.
  - В `Startup` после открытия хранилища запускаются все `AutoStart` пробросы; пробросы с паролем хранилища стартуют после разблокировки. Пробросы используют собственный `ssh.Client` на хост, независимый от терминалов; запрос доверия отпечатку идёт через окно доверия (Task 13b); пароль и фразу ключа для проброса без открытого терминала спрашивает «мини-терминал» внутри карточки проброса (тот же `Prompter`, вывод в маленький xterm 6 строк).
- Фронтенд по артборду Forwards: карточки, диалог создания (тип сегментом L/R/D, хост, слушать, назначение, автозапуск), счётчик пробросов текущего хоста в заголовке терминала и в строке состояния.

- [ ] **Step 1: Реализовать API и автозапуск**

- [ ] **Step 2: Собрать экран и диалог**

- [ ] **Step 3: Проверить вручную**

Run: `wails dev`
Expected: L-проброс к Postgres работает из `psql` без открытого терминала; занятый порт показывает ошибку на карточке; после перезапуска приложения автозапуск поднимает пробросы; остановка освобождает порт.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: forwards API, autostart and Forwards screen"
```

---

### Task 13a: Переход на Wails v3

Решение владельца продукта (2026-10-25): перейти с Wails v2.16 на Wails v3 (`v3.0.0-beta.25`, как в Stash) до оставшихся задач оболочки (13b, 14, 15). Причины: системные контекстные меню, события окна и доступ к HWND (обход бага DPI из Stash переносится как есть), `UseVisualHosting`/`WebviewUserDataPath`, единый со Stash подход к сборке.

**Files:**
- Modify: `go.mod`/`go.sum`, `main.go` (`application.New`, `Services`, одно `WebviewWindow` 1280×800, `Frameless`, `BackgroundColour`, `Windows: {UseVisualHosting: true, WebviewUserDataPath: <папка данных>\WebView2, Theme dark}`), `app/app.go` (`Startup` → `ServiceStartup(ctx, opts)`, `BeforeClose`/`Shutdown` → события окна `WindowClosing`/`ServiceShutdown`), `app/events.go` (эмиттер через `app.Event.Emit`), `app/window_state.go` (размер и позиция через методы `WebviewWindow`), `app/keys_api.go`/`settings_api.go` (диалоги файлов через `application.OpenFileDialog`/`SaveFileDialog`), `wails.json` → `build/config.yml` + `Taskfile.yml` (образец: `wepmaner/stash/app`), `.golangci.yml`/`Makefile` (команды `wails3`), `frontend/package.json` (`@wailsio/runtime`), `frontend/src/lib/api.ts` (биндинги из `frontend/bindings/`), `frontend/src/lib/titlebar/TitleBar.svelte` (`Window.Minimise/ToggleMaximise/Close`, `--wails-draggable` остаётся), `TerminalView.svelte`/`sessions.svelte.ts`/`forwards.svelte.ts`/`MiniTerminal.svelte` (`Events.On` из `@wailsio/runtime`), все тесты с моками `../api` и рантайма, `frontend/wailsjs/` удаляется.
- Create: `app/restorefix_windows.go` (копия из Stash с заменой имени окна; обход DPI после восстановления), `build/` по шаблону `wails3 init` (без `build/ios`, `build/darwin`, `build/linux`).

**Interfaces:**
- Все методы `App` остаются теми же публичными методами одного сервиса `application.NewService(app)`; имена событий (`term:*`, `fwd:*`) не меняются. Фронтенд меняет только импорт биндингов и рантайма.
- Правила нативного поведения CLAUDE.md проверяются заново вживую: перетаскивание за заголовок, двойной щелчок разворачивает, Snap-раскладки на «Развернуть» (в v3 проверить `Window.ToggleMaximise` и hover), Alt+Space, восстановление геометрии при запуске, закрытие по крестику сохраняет геометрию, старт без сплэша.
- Контекстные меню хостов, ключей и пробросов переводятся на системные (`application.NewContextMenu` / `window.ShowContextMenu` в v3) там, где v3 это поддерживает на Windows; `ui/Menu.svelte` остаётся для случаев, где системное меню недоступно, с пометкой в CLAUDE.md.
- Баг DPI (Task 15 Step 1b) закрывается здесь: `UseVisualHosting: true` + `fixRestoreScale(window)` на `events.Windows.WindowUnMinimise`.

- [ ] **Step 1: Ветка и инвентаризация** — `git grep -n "wailsjs\|wailsapp/wails/v2"`; зафиксировать список точек контакта в отчёте.
- [ ] **Step 2: Go** — `go get github.com/wailsapp/wails/v3@v3.0.0-beta.25`, `main.go`, сервис, события, окно, диалоги; `go build ./... && go test ./... -count=1 && golangci-lint run ./...`.
- [ ] **Step 3: Фронтенд** — `wails3 generate bindings`, `@wailsio/runtime`, правка `api.ts` и импортов, тесты (`npm test`, `svelte-check`), сборка `wails3 build`.
- [ ] **Step 4: Живая проверка контроллером** по списку правил нативного поведения, плюс: терминал печатает в том же кадре, события пробросов доходят, системное контекстное меню на хосте/ключе, баг DPI (развернуть → свернуть → восстановить, на двух мониторах с разным масштабом).
- [ ] **Step 5: Документы** — CLAUDE.md «Стек» (v3 beta.25, причина), «Правила нативного поведения» (меню системные), Task 15 (сборка через `wails3 build`, `-X main.version` в `build/windows/Taskfile.yml`, Step 1b закрыт здесь); commit `feat: migrate the shell to Wails v3`.

### Task 13b: Окно доверия неизвестному серверу

Решение владельца продукта (2026-10-25): консольный вопрос «Доверять и запомнить?» сбивает с толку. Для неизвестного ключа сервера показывается собственное окно в стиле остальных диалогов (как «Security Alert» в PuTTY); пароль и парольная фраза по-прежнему спрашиваются в консоли. Изменившийся ключ сервера по-прежнему всегда отклоняется карточкой ошибки. Выполняется после Task 13, до Task 14: пробросы используют тот же механизм доверия.

**Files:**
- Modify: `internal/sshclient/knownhosts.go` (`KnownHosts` получает интерфейс `TrustAsker` вместо прямого `Prompter.Confirm`), `knownhosts_test.go`, `app/sessions.go`, `app/events.go`, `app/forwards_api.go` (пробросы), `frontend/src/lib/terminal/TerminalView.svelte`, `frontend/src/lib/forwards/*` (мини-терминал больше не нужен для доверия)
- Create: `app/trust_api.go`, `app/trust_api_test.go`, `frontend/src/lib/ui/TrustDialog.svelte`, `TrustDialog.test.ts`

**Interfaces:**
- `sshclient.TrustAsker` — `AskTrust(ctx, q TrustQuestion) (bool, error)`; `TrustQuestion{Host string; Port int; KeyType string; FingerprintSHA256 string; FingerprintMD5 string}`. `Prompter` реализует его для тестов и как запасной путь; в приложении его реализует `app`.
- `app`: событие `trust:ask` с `{ID, SessionID, ForwardID, Host, Port, KeyType, Fingerprint}`; метод `TrustAnswer(id string, trust bool) error`. Один открытый вопрос на сессию/проброс; отмена сессии (закрытие вкладки, Stop проброса) снимает вопрос. Пока вопрос открыт, таймаут рукопожатия приостановлен (как у консольных вопросов). В терминал при этом печатается строка «Сервер <host> неизвестен, отпечаток <fp>» для истории, без ожидания ввода.
- `TrustDialog.svelte`: заголовок «Неизвестный сервер», строка `user@host:port`, тип ключа и отпечаток SHA256 моноширинным с кнопкой «Скопировать», пояснение в одну строку («Клиент видит этот сервер впервые. Если отпечаток совпадает с тем, что дал администратор, подключение безопасно.»), кнопки «Доверять и подключиться» (основная, в фокусе) и «Отмена»; Esc = отмена. Отмена завершает подключение сообщением «Подключение отменено: сервер не подтверждён» без записи в known_hosts.

- [ ] **Step 1: Тесты** — `TestKnownHostsAsksTrustAsker` (вопрос содержит host, port, тип и SHA256; `true` записывает, `false` возвращает `ErrHostNotTrusted` и не пишет); `TestTrustAnswerUnknownID`; `TestTrustCancelledWithSession` (закрытие сессии снимает вопрос, Dial завершается); `TrustDialog.test.ts` (поля, копирование отпечатка, Esc → отмена, фокус на основной кнопке).
- [ ] **Step 2: Реализация и проверка** — `go test ./... -count=1`, `golangci-lint run ./...`, `npm test`, `svelte-check`, `wails build`; живая проверка контроллером: первое подключение показывает окно, «Доверять и подключиться» пишет known_hosts, повторное подключение окна не показывает, «Отмена» даёт карточку с текстом отмены.
- [ ] **Step 3: Commit** — `feat: in-app trust dialog for unknown host keys`.

## Этап G. Настройки, импорт, сборка

### Task 14: Настройки и импорт хостов

**Files:**
- Create: `internal/importers/sshconfig.go`, `internal/importers/putty.go`, `internal/importers/importers_test.go`, `frontend/src/lib/settings/SettingsView.svelte`
- Modify: `app/settings_api.go`

**Interfaces:**
- Produces:
  - `func ParseSSHConfig(r io.Reader) ([]model.Host, []string /*предупреждения*/, error)` — `Host` без шаблонов `*`/`?`; `HostName`, `Port`, `User`, `IdentityFile` (путь возвращается в предупреждении «ключ нужно импортировать отдельно», `KeyID` пустой).
  - `func ReadPuTTYSessions() ([]model.Host, []string, error)` — реестр `HKCU\Software\SimonTatham\PuTTY\Sessions`: `HostName`, `PortNumber`, `UserName`, имя сессии URL-декодируется в `Name`; сессии не-SSH пропускаются с предупреждением.
  - `App.ImportSSHConfig() (ImportResult, error)` — `{Added int; Skipped int; Warnings []string}`; файл `%USERPROFILE%\.ssh\config`; дубликаты по `User@Address:Port` пропускаются. `App.ImportPuTTY()` аналогично.
- `SettingsView.svelte` по артборду Settings без блока TUN и без галочки про отпечаток (изменившийся ключ хоста отклоняется всегда): Терминал (шрифт из фиксированного списка `Cascadia Mono`, `JetBrains Mono`, `Consolas`; размер 10–20), Хранилище и безопасность (карточка состояния + кнопка диалога, блокировка после простоя 0/5/15/30 мин: при включённом пароле показывает `UnlockScreen` после бездействия, «Открыть папку с данными» через `runtime.BrowserOpenURL` на каталог, «Экспорт хостов в файл» в JSON без ключей), Импорт. Изменения применяются сразу, без кнопки «Сохранить».

- [ ] **Step 1: Написать падающие тесты импортёров**

```go
func TestParseSSHConfigBasic(t *testing.T)       // Host prod / HostName 10.0.4.11 / User deploy / Port 2222 → один Host
func TestParseSSHConfigSkipsWildcards(t *testing.T)
func TestParseSSHConfigWarnsIdentityFile(t *testing.T)
func TestPuTTYSessionNameDecoded(t *testing.T)   // "my%20server" → "my server" (через интерфейс чтения реестра с фейком)
```

- [ ] **Step 2: Запустить, убедиться что падают; реализовать; тесты проходят**

Run: `go test ./internal/importers/ -v`
Expected: PASS после реализации

- [ ] **Step 3: Собрать `SettingsView.svelte`, подключить блокировку по простою**

- [ ] **Step 4: Проверить вручную**

Run: `wails dev`
Expected: смена шрифта и размера мгновенно меняет открытые терминалы; импорт из `~/.ssh/config` добавляет хосты и перечисляет предупреждения; блокировка после 5 минут простоя показывает экран разблокировки.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: settings screen, idle lock, ssh_config and PuTTY import"
```

### Task 15: Полировка нативности и выпуск через Stash

Программа распространяется через лаунчер Stash: он ставит её как папку файлов в `%LOCALAPPDATA%\Stash\apps\<id>\` без установщика и прав администратора, сверяет файлы пакета с эталоном и обновляет по тегам GitHub-релизов. Поэтому установщика NSIS нет, а программа портативна: рядом с exe ничего не пишет, все данные держит в `%APPDATA%\sshclient\`.

**Files:**
- Modify: `main.go` (переменная версии, `WebviewUserDataPath`), `internal/appdir/appdir.go` и тест, `app/settings_api.go` (отдать версию), `frontend/src/lib/settings/*` (показ версии), `frontend/src/styles/tokens.css`, `frontend/src/lib/ui/Dialog.svelte`, `wails.json` (`info.productVersion` не трогать: версию задаёт CI)
- Create: `stash.json`, `assets/icon.png` (256×256, PNG ≤ 512 КБ, допустимо плейсхолдер в стиле палитры), `.github/workflows/release.yml`, `.github/workflows/stash-ping.yml`
- Delete: `build/darwin/*` (под Windows не нужно)

- [ ] **Step 1: Движение и фокус**

Проверить по списку CLAUDE.md «Правила нативного поведения»: диалог 150 мс / 0,97, наведение 80 мс, вкладки без анимации, `@media (prefers-reduced-motion: reduce)` отключает переходы; фокус-кольцо `2px #5b9cf8` на всех контролах с клавиатуры; Esc закрывает любой диалог; Alt+Space открывает системное меню окна (Wails отдаёт системе при `DisableFramelessWindowDecorations: false`, проверить).

- [ ] **Step 1a: Замечания владельца по разделу «Ключи» (2026-10-25)**

В `frontend/src/lib/keys/KeyDetail.svelte`:
- Вместо кнопки «Переименовать» — иконка карандаша (кнопка-иконка 28×28, `aria-label="Переименовать"`, подсказка) сразу справа от названия ключа; по щелчку то же инлайн-редактирование. Кнопка «Удалить» остаётся текстовой справа.
- Вместо кнопки «Скопировать» в подписи — иконка копирования (`aria-label="Скопировать публичный ключ"`) вплотную к значению публичного ключа, справа от блока.
- Тёмный блок со значением публичного ключа не растягивается на всю ширину: `width: fit-content; max-width: 100%`, длинный RSA переносится по словам внутри (`overflow-wrap: anywhere`), короткий Ed25519 занимает ровно свою длину.
- Тесты `KeyDetail.test.ts` обновить под иконки (поиск по `aria-label`).

- [ ] **Step 1b: Масштаб растёт после «развернуть → свернуть → восстановить» (баг, 2026-10-25)**

Симптом воспроизведён владельцем и контроллером (снимок после сворачивания в 1,5 раза крупнее). Причина описана в руководстве Stash (раздел 6, Wails v3, но механизм тот же): свёрнутое окно Windows паркует за экраном и числит за соседним монитором с другим масштабом; после восстановления `GetDpiForWindow` ещё ~150 мс отдаёт чужой DPI, по нему выставляется масштаб WebView2.

Обход (перенос `restorefix_windows.go` из Stash на Wails v2):
- Wails v2 не отдаёт HWND и не имеет события «восстановлено»: HWND ищется один раз по PID (`EnumWindows` + `GetWindowThreadProcessId`, видимое окно верхнего уровня), триггер — фронтенд: `document.addEventListener('visibilitychange')` при переходе в `visible` вызывает `App.ScaleResync()`.
- `ScaleResync` (файл `app/restorefix_windows.go`, по образцу Stash): раз в кадр (16 мс, до 700 мс) сравнивать `GetDpiForWindow` с `GetDpiForMonitor(MonitorFromWindow)`; как только совпали — `SendMessageW(hwnd, WM_DPICHANGED, dpi<<16|dpi, &rect)` с текущим прямоугольником окна; через 300 мс повтор. Раньше совпадения не сбрасывать: сброс по устаревшему DPI закрепляет неверный масштаб.
- Проверить, что Wails v2 обрабатывает `WM_DPICHANGED` пересчётом масштаба WebView2 (в `frontend/desktop/windows` v2.16); если нет — вызвать `webview.NotifyParentWindowPositionChanged`/обновить zoom через `ICoreWebView2Controller.put_RasterizationScale` тем же путём, что v2 делает при смене монитора. Остаточное мелькание одного кадра допустимо.
- Проверка только на двух мониторах с разным масштабом (100 % и 150 %): развернуть, свернуть, восстановить — интерфейс прежнего размера; повторить пять раз. Записать в CLAUDE.md как известное ограничение WebView2 с обходом.

- [ ] **Step 2: Snap-раскладки при наведении на «Развернуть»**

Проверить в `wails dev`. Если Wails v2 не показывает раскладки для кастомной кнопки, зафиксировать в CLAUDE.md как известное ограничение v2 и оставить двойной щелчок по заголовку и Win+Z как путь.

- [ ] **Step 3: Шрифты**

В `tokens.css` интерфейс `'Segoe UI Variable Text', 'Segoe UI', system-ui`; терминал по `Settings.TerminalFont` с откатом `Consolas`. Google Fonts не загружаются (приложение офлайн).

- [ ] **Step 4: Портативность под Stash**

- `internal/appdir.Dir()` возвращает `%APPDATA%\sshclient` (`os.UserConfigDir()`), а не `%LOCALAPPDATA%`; ошибка, если `APPDATA` пуст. Тесты appdir и те тесты `app`, что подменяют `LOCALAPPDATA`, переводятся на `APPDATA`. Миграция: если `%LOCALAPPDATA%\sshclient\vault.json` существует, а в новой папке ничего нет, при первом старте перенести содержимое старой папки в новую (`os.Rename`, при ошибке копирование) и записать в журнал; тест с двумя временными папками.
- `main.go`: `Windows.WebviewUserDataPath = filepath.Join(appdir.Dir(), "WebView2")`, чтобы кэш WebView2 не создавал `%APPDATA%\sshclient.exe`.
- `main.go`: `var version = "dev"`; CI заполняет через `-ldflags "-X main.version=X.Y.Z"`. `App.Version()` отдаёт её, экран Настроек показывает «Версия X.Y.Z» внизу.
- Проверить, что ни один код не пишет файлы рядом с exe и не читает пути от текущей папки (`grep -rn "os.Getwd\|\"./" --include=*.go`); известный `known_hosts` и геометрия окна уже в папке данных.
- Публичный репозиторий: в коде, тестах, документах и `go.mod` нет локальных путей, адресов серверов и ключей, кроме фикстур `internal/keys/testdata` (происхождение описано в `testdata/README.md`). Проверка: `git grep -n -i -E "c:\\users|/c/users|@proton|BEGIN .*PRIVATE" -- . ':!internal/keys/testdata'` даёт пусто.

- [ ] **Step 5: `stash.json` и иконка**

```jsonc
{
  "id": "ssh-client",
  "name": "SSH-клиент",
  "description": "Лёгкий SSH-клиент для Windows 11: хосты, ключи, терминал с вкладками, пробросы портов.",
  "icon": "terminal",
  "image": "assets/icon.png",
  "hue": 215,
  "kind": "exe",
  "entry": "sshclient.exe",
  "data": ["${APPDATA}\sshclient"],
  "requirements": [
    { "label": "Windows 10 2004+ (WebView2 Runtime)", "check": { "type": "os", "min": "10.0.19041" } }
  ],
  "minStash": "0.1.9"
}
```

`id` не меняется никогда. Имя папки данных `sshclient` совпадает с именем exe (Stash сравнивает без регистра и дефисов), иначе `data` не пройдёт `stash-pack`. `preserve` не нужен: рядом с exe файлов нет.

- [ ] **Step 6: Workflow релиза**

`.github/workflows/release.yml`, триггер `push: tags: ['v*']`, `permissions: contents: write`, раннер `windows-latest` (нативная сборка Wails, `-race` требует cgo, на раннере есть gcc):

1. `actions/checkout@v5` с `fetch-depth: 0`; `actions/setup-go@v6` (`go-version-file: go.mod`); `actions/setup-node@v4` (Node 22, `cache: npm`, `cache-dependency-path: frontend/package-lock.json`).
2. Проверки: `go vet ./...`, `go test ./... -race -count=1`, `golangci-lint run ./...` (через `golangci/golangci-lint-action@v7`), `npm ci && npm test && npx svelte-check` в `frontend`.
3. Сборка: `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`; `VERSION="${GITHUB_REF_NAME#v}"`; `wails build -clean -trimpath -ldflags "-X main.version=${VERSION}"`; `mkdir dist && cp build/bin/sshclient.exe dist/`.
4. Пакет: `go run github.com/wepmaner/stash-pack/cmd/stash-pack@v0.1.5 -dir dist -manifest stash.json -version "${GITHUB_REF_NAME}" -out stash-out`.
5. «Что нового»: текст аннотированного тега (строки `- …` — пункты, `Раздел:` — заголовок; `#` в теге не работает), иначе заголовки коммитов с прошлого тега; `[ -s notes.md ] || echo "- Без описания" > notes.md`. Шаг пишется в bash (`shell: bash`).
6. Релиз: `gh release create "${GITHUB_REF_NAME}" stash-out/* dist/sshclient.exe --notes-file notes.md` (без `--generate-notes`).
7. `continue-on-error: true`: `curl -fsS -X POST -H "Authorization: Bearer ${STASH_CI_TOKEN}" "https://stash.stash-worker.workers.dev/v1/admin/refresh?app=ssh-client"` с секретом `secrets.STASH_CI_TOKEN`. В первом релизе шаг падает: токена ещё нет.

`.github/workflows/stash-ping.yml`: `workflow_dispatch`, один шаг с тем же `curl`, печатает ответ и падает с `::error::`, если код не 200.

Проверка перед коммитом: `act` не нужен; достаточно `wails build -clean -trimpath -ldflags "-X main.version=0.0.0-local"` локально и `go run github.com/wepmaner/stash-pack/cmd/stash-pack@v0.1.5 -dir dist -manifest stash.json -version v0.0.0 -out stash-out` (оба каталога в `.gitignore`). Ожидается `stash-out/{stash.json,files.json,package.zip,icon.png}`.

- [ ] **Step 7: Первый выпуск (делает владелец, не субагент)**

```bash
git remote add origin git@github.com:wepmaner/ssh-client.git   # публичный репозиторий
git push -u origin v1
git tag -a v0.1.0 -m "Первая версия:
- Хосты и группы, ключи OpenSSH/PEM/PPK, терминал с вкладками, пробросы"
git push --tags
```

Затем Stash → Админка → Приложения → «Добавить приложение» → `wepmaner/ssh-client` → «Записать в GitHub через gh» → включить. Проверка: `https://stash.stash-worker.workers.dev/v1/versions/ssh-client` показывает `0.1.0`.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: native polish, Stash manifest and release workflow"
```

---

## Что сознательно не вошло в v1

- TUN-режим (wintun, права администратора) — отдельный план после v1.
- Windows Hello для разблокировки хранилища — отдельный план; кандидат на основу `byteness/keyring` (бэкенд winhello: DEK обёрнут ключом Hello KSP).
- Разделение окна терминала, SFTP, сниппеты.
- Светлая тема.
- Панель «О сервере» (решение 2026-10-25): после v1, только для чтения и только по явному действию пользователя («Собрать сведения» или включённая настройка), с показом выполненных команд: ОС из `/etc/os-release` (заодно заполняет монограмму плитки), аптайм и нагрузка, диск, число вошедших пользователей. Настройка сервера (правка sshd_config) через GUI не делается. Бейдж «пароль разрешён» из списка методов рукопожатия отклонён владельцем.
