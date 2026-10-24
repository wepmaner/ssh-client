package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

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
	errNoWindow      = errors.New("Окно приложения недоступно")
	errReadFile      = errors.New("Не удалось прочитать файл")
)

// ErrCancelled is returned when the user dismisses a system file dialog; the UI ignores it.
var ErrCancelled = errors.New("cancelled")

const passphraseRequiredPrefix = "passphrase_required:"

// KeyDTO is a key as shown in the UI. The private part never leaves Go.
type KeyDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
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
		ID: k.ID, Name: k.Name, Type: k.Type, PublicLine: k.PublicLine,
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

// KeyGenerate creates and stores a new key of kind ed25519 or rsa.
func (a *App) KeyGenerate(name, kind, comment string) (KeyDTO, error) {
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
	k, err := keys.Generate(kind, strings.TrimSpace(comment))
	if err != nil {
		if errors.Is(err, keys.ErrUnsupportedKeyType) {
			return KeyDTO{}, errBadKeyType
		}
		return KeyDTO{}, err
	}
	return a.saveKey(k, name)
}

// parseKey maps key parser errors to UI messages; a missing passphrase is
// reported as passphrase_required:<ref> so the UI can ask and retry.
func parseKey(data []byte, passphrase, ref string) (model.Key, error) {
	var pp []byte
	if passphrase != "" {
		pp = []byte(passphrase)
		defer clear(pp)
	}
	k, err := keys.ParsePrivate(data, pp)
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
	if a.ctx == nil {
		return KeyDTO{}, errNoWindow
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Импорт приватного ключа",
		Filters: []runtime.FileFilter{
			{DisplayName: "Ключи (*.ppk;*.pem;id_*)", Pattern: "*.ppk;*.pem;id_*"},
			{DisplayName: "Все файлы (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return KeyDTO{}, err
	}
	if path == "" {
		return KeyDTO{}, ErrCancelled
	}
	return a.KeyImportFileWithPassphrase(path, "")
}

// KeyImportFileWithPassphrase imports the key file at path, decrypting it with passphrase.
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

// KeyImportTextWithPassphrase is KeyImportText for an encrypted key.
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
	if a.ctx == nil {
		return errNoWindow
	}
	return runtime.ClipboardSetText(a.ctx, keys.PublicLine(k))
}

// exportPayload returns the file content and default file name for a format.
func exportPayload(k model.Key, format string) ([]byte, string, error) {
	switch format {
	case "public":
		return []byte(keys.PublicLine(k) + "\n"), k.Name + ".pub", nil
	case "openssh":
		return keys.PrivateOpenSSH(k), k.Name, nil
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
	if a.ctx == nil {
		return errNoWindow
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Экспорт ключа",
		DefaultFilename: name,
	})
	if err != nil {
		return err
	}
	if path == "" {
		return ErrCancelled
	}
	return os.WriteFile(path, data, 0o600)
}
