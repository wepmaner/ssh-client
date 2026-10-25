package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/keys"
	"sshclient/internal/model"
	"sshclient/internal/store"
)

// User-facing messages of the keys API, shown verbatim by the UI.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errKeyNameEmpty  = errors.New("Укажите название ключа")
	errKeyNameTaken  = errors.New("Ключ с таким названием уже есть")
	errKeyNotFound   = errors.New("Ключ не найден")
	errWrongPhrase   = errors.New("Неверная парольная фраза")
	errBadKey        = errors.New("Не удалось прочитать ключ: неподдерживаемый или повреждённый формат")
	errBadKeyType    = errors.New("Поддерживаются только ключи Ed25519 и RSA")
	errBadExportKind = errors.New("Неизвестный формат экспорта")
	errExportLocked  = errors.New("Ключ с парольной фразой экспортируется только исходным файлом")
	errNoWindow      = errors.New("Окно приложения недоступно")
	errClipboard     = errors.New("Не удалось скопировать в буфер обмена")
	errReadFile      = errors.New("Не удалось прочитать файл")
)

// ErrCancelled is returned when the user dismisses a system file dialog; the UI ignores it.
var ErrCancelled = errors.New("cancelled")

// dialogCancelledText is the error text of a dismissed file dialog in Wails v3
// (cfd.ErrorCancelled, an internal package that cannot be imported).
const dialogCancelledText = "cancelled by user"

// dialogPath maps the result of a Wails file dialog: dismissing it, which v3
// reports as an error, becomes ErrCancelled like an empty path.
func dialogPath(path string, err error) (string, error) {
	if err != nil {
		if err.Error() == dialogCancelledText {
			return "", ErrCancelled
		}
		return "", err
	}
	if path == "" {
		return "", ErrCancelled
	}
	return path, nil
}

const passphraseRequiredPrefix = "passphrase_required:"

// KeyDTO is a key as shown in the UI. The private part never leaves Go.
// Format is openssh|pem|ppk; Encrypted means the key has its own passphrase,
// asked in the terminal on every connection.
type KeyDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Format      string   `json:"format"`
	Encrypted   bool     `json:"encrypted"`
	PublicLine  string   `json:"public_line"`
	Comment     string   `json:"comment"`
	Fingerprint string   `json:"fingerprint"`
	Source      string   `json:"source"`
	CreatedAt   string   `json:"created_at"`
	HostIDs     []string `json:"host_ids"`
}

func (a *App) keyDTO(k model.Key) KeyDTO {
	ids := []string{}
	for _, h := range a.store.HostsUsingKey(k.ID) {
		ids = append(ids, h.ID)
	}
	return KeyDTO{
		ID: k.ID, Name: k.Name, Type: k.Type, Format: k.Format, Encrypted: k.Encrypted, PublicLine: k.PublicLine,
		Comment: k.Comment, Fingerprint: k.Fingerprint, Source: k.Source,
		CreatedAt: k.CreatedAt.Format(time.RFC3339), HostIDs: ids,
	}
}

// KeysList returns all stored keys without private material.
func (a *App) KeysList() []KeyDTO {
	out := []KeyDTO{}
	if !a.storeUnlocked() {
		return out
	}
	for _, k := range a.store.Keys() {
		out = append(out, a.keyDTO(k))
	}
	return out
}

func (a *App) findKey(id string) (model.Key, error) {
	for _, k := range a.store.Keys() {
		if k.ID == id {
			return k, nil
		}
	}
	return model.Key{}, errKeyNotFound
}

// checkName validates a key name; exceptID is the key being renamed.
func (a *App) checkName(name, exceptID string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errKeyNameEmpty
	}
	for _, k := range a.store.Keys() {
		if k.ID != exceptID && strings.EqualFold(k.Name, name) {
			return "", errKeyNameTaken
		}
	}
	return name, nil
}

func (a *App) saveKey(k model.Key, name string) (KeyDTO, error) {
	name, err := a.checkName(name, "")
	if err != nil {
		return KeyDTO{}, err
	}
	k.Name = name
	saved, err := a.store.SaveKey(k)
	if err != nil {
		return KeyDTO{}, err
	}
	return a.keyDTO(saved), nil
}

// defaultComment is user@computer, used when the form leaves the comment empty.
func defaultComment() string {
	user := os.Getenv("USERNAME")
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	switch {
	case user != "" && host != "":
		return user + "@" + host
	case user != "":
		return user
	}
	return host
}

// KeyGenerate creates and stores a new key of kind ed25519 or rsa. A
// non-empty passphrase encrypts the key; the passphrase itself is not kept.
func (a *App) KeyGenerate(name, kind, comment, passphrase string) (KeyDTO, error) {
	if !a.storeUnlocked() {
		return KeyDTO{}, errVaultLocked
	}
	name, err := a.checkName(name, "")
	if err != nil {
		return KeyDTO{}, err
	}
	if strings.TrimSpace(comment) == "" {
		comment = defaultComment()
	}
	var pp []byte
	if passphrase != "" {
		pp = []byte(passphrase)
		defer clear(pp)
	}
	k, err := keys.Generate(kind, strings.TrimSpace(comment), pp)
	if err != nil {
		if errors.Is(err, keys.ErrUnsupportedKeyType) {
			return KeyDTO{}, errBadKeyType
		}
		return KeyDTO{}, err
	}
	return a.saveKey(k, name)
}

// parseKey reads a key without decrypting it and maps errors to UI messages.
// Only a legacy encrypted PEM, which has no public part, needs the passphrase:
// it is reported as passphrase_required:<ref> so the UI can ask and retry.
// The passphrase only reads the public key; the key is stored as it came.
func parseKey(data []byte, passphrase, ref string) (model.Key, error) {
	var k model.Key
	var err error
	if passphrase != "" {
		pp := []byte(passphrase)
		k, err = keys.InspectWithPassphrase(data, pp)
		clear(pp)
	} else {
		k, err = keys.Inspect(data)
	}
	switch {
	case err == nil:
		return k, nil
	case errors.Is(err, keys.ErrPassphraseRequired):
		return model.Key{}, errors.New(passphraseRequiredPrefix + ref)
	case errors.Is(err, keys.ErrWrongPassphrase):
		return model.Key{}, errWrongPhrase
	case errors.Is(err, keys.ErrUnsupportedKeyType):
		return model.Key{}, errBadKeyType
	default:
		return model.Key{}, errBadKey
	}
}

func fileKeyName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// KeyImportFile asks for a key file and imports it.
func (a *App) KeyImportFile() (KeyDTO, error) {
	if !a.storeUnlocked() {
		return KeyDTO{}, errVaultLocked
	}
	if a.wapp == nil {
		return KeyDTO{}, errNoWindow
	}
	path, err := dialogPath(a.wapp.Dialog.OpenFile().
		AttachToWindow(a.mainWin).
		SetTitle("Импорт приватного ключа").
		AddFilter("Ключи (*.ppk;*.pem;id_*)", "*.ppk;*.pem;id_*").
		AddFilter("Все файлы (*.*)", "*.*").
		PromptForSingleSelection())
	if err != nil {
		return KeyDTO{}, err
	}
	return a.KeyImportFileWithPassphrase(path, "")
}

// KeyImportFileWithPassphrase imports the key file at path; passphrase is
// only for a legacy encrypted PEM (see parseKey).
func (a *App) KeyImportFileWithPassphrase(path, passphrase string) (KeyDTO, error) {
	if !a.storeUnlocked() {
		return KeyDTO{}, errVaultLocked
	}
	data, err := os.ReadFile(path) //nolint:gosec // path chosen by the user in the system dialog
	if err != nil {
		return KeyDTO{}, errReadFile
	}
	defer clear(data)
	k, err := parseKey(data, passphrase, path)
	if err != nil {
		return KeyDTO{}, err
	}
	return a.saveKey(k, fileKeyName(path))
}

// KeyImportText imports a pasted private key. An empty name falls back to the
// key comment, then to key-YYYY-MM-DD.
func (a *App) KeyImportText(text, name string) (KeyDTO, error) {
	return a.KeyImportTextWithPassphrase(text, name, "")
}

// KeyImportTextWithPassphrase is KeyImportText for a legacy encrypted PEM.
func (a *App) KeyImportTextWithPassphrase(text, name, passphrase string) (KeyDTO, error) {
	if !a.storeUnlocked() {
		return KeyDTO{}, errVaultLocked
	}
	data := []byte(strings.TrimSpace(text))
	defer clear(data)
	k, err := parseKey(data, passphrase, "text")
	if err != nil {
		return KeyDTO{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = k.Comment
	}
	if strings.TrimSpace(name) == "" {
		name = "key-" + time.Now().Format("2006-01-02")
	}
	return a.saveKey(k, name)
}

// KeyRename changes the display name of a key.
func (a *App) KeyRename(id, name string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	k, err := a.findKey(id)
	if err != nil {
		return err
	}
	if k.Name, err = a.checkName(name, id); err != nil {
		return err
	}
	_, err = a.store.SaveKey(k)
	return err
}

// KeyDelete removes a key that no host uses.
func (a *App) KeyDelete(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	used := a.store.HostsUsingKey(id)
	err := a.store.DeleteKey(id)
	if errors.Is(err, store.ErrKeyInUse) {
		names := make([]string, 0, len(used))
		for _, h := range used {
			names = append(names, h.Name)
		}
		//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
		return errors.New("Ключ используется хостами: " + strings.Join(names, ", "))
	}
	return err
}

// KeyCopyPublic puts the public key line on the clipboard.
func (a *App) KeyCopyPublic(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	k, err := a.findKey(id)
	if err != nil {
		return err
	}
	if a.wapp == nil {
		return errNoWindow
	}
	if !a.wapp.Clipboard.SetText(keys.PublicLine(k)) {
		return errClipboard
	}
	return nil
}

// exportPayload returns the file content and default file name for a format.
// "original" is the stored file as is; an encrypted key is exported only that
// way, never converted.
func exportPayload(k model.Key, format string) ([]byte, string, error) {
	if k.Encrypted && (format == "openssh" || format == "ppk") {
		return nil, "", errExportLocked
	}
	switch format {
	case "public":
		return []byte(keys.PublicLine(k) + "\n"), k.Name + ".pub", nil
	case "original":
		data, ext := keys.PrivateOriginal(k)
		return data, k.Name + ext, nil
	case "openssh":
		data, err := keys.PrivateOpenSSH(k)
		return data, k.Name, err
	case "ppk":
		data, err := keys.PrivatePPK(k)
		return data, k.Name + ".ppk", err
	}
	return nil, "", errBadExportKind
}

// KeyExport saves a key to a file chosen in the system dialog. A vault with a
// password requires it again; the UI asks for it first.
func (a *App) KeyExport(id, format, password string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if err := a.verifyPassword(password); err != nil {
		return err
	}
	k, err := a.findKey(id)
	if err != nil {
		return err
	}
	data, name, err := exportPayload(k, format)
	if err != nil {
		return err
	}
	if a.wapp == nil {
		return errNoWindow
	}
	path, err := dialogPath(a.wapp.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    "Экспорт ключа",
		Filename: name,
		Window:   a.mainWin,
	}).PromptForSingleSelection())
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
