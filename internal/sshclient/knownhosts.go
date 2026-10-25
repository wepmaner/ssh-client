package sshclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHosts проверяет ключи серверов по файлу known_hosts и дописывает новые с согласия пользователя.
type KnownHosts struct {
	path string

	mu sync.Mutex // сериализует чтение и дозапись файла; на время ожидания ответа пользователя не удерживается
}

// OpenKnownHosts проверяет known_hosts, создавая файл (0o600) и родительский каталог при отсутствии.
func OpenKnownHosts(path string) (*KnownHosts, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("создание каталога known_hosts: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600) //nolint:gosec // G304: path comes from the app data dir
	if err != nil {
		return nil, fmt.Errorf("создание known_hosts: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("создание known_hosts: %w", err)
	}
	k := &KnownHosts{path: path}
	if _, err := k.load(); err != nil {
		return nil, err
	}
	return k, nil
}

// load перечитывает known_hosts с диска, чтобы ручные правки файла действовали без перезапуска.
func (k *KnownHosts) load() (ssh.HostKeyCallback, error) {
	cb, err := knownhosts.New(k.path)
	if err != nil {
		return nil, fmt.Errorf("чтение known_hosts: %w", err)
	}
	return cb, nil
}

// check сверяет ключ с файлом; ошибку проверки возвращает как есть.
func (k *KnownHosts) check(hostname string, remote net.Addr, key ssh.PublicKey) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.checkLocked(hostname, remote, key)
}

func (k *KnownHosts) checkLocked(hostname string, remote net.Addr, key ssh.PublicKey) error {
	cb, err := k.load()
	if err != nil {
		return err
	}
	return cb(hostname, remote, key)
}

// changedError строит ошибку про изменившийся ключ; из записанных берёт ключ того же типа, что у предъявленного.
func (k *KnownHosts) changedError(hostname string, keyErr *knownhosts.KeyError, key ssh.PublicKey) error {
	old := keyErr.Want[0].Key
	for _, w := range keyErr.Want {
		if w.Key.Type() == key.Type() {
			old = w.Key
			break
		}
	}
	return fmt.Errorf("%w: возможна подмена сервера %s (атака «человек посередине»). "+
		"Записанный отпечаток %s, полученный %s. Если ключ сервера сменили намеренно, удалите его строку из %s",
		ErrHostKeyChanged, hostname, ssh.FingerprintSHA256(old), ssh.FingerprintSHA256(key), k.path)
}

// msgHostNotTrusted — сообщение для пользователя, когда он не подтвердил неизвестный ключ сервера.
const msgHostNotTrusted = "Подключение отменено: сервер не подтверждён"

// Callback возвращает ssh.HostKeyCallback: о неизвестном хосте спрашивается a (отказ даёт
// ErrHostNotTrusted, отмена ctx — ctx.Err(), в обоих случаях файл не меняется), изменившийся ключ
// отклоняется без вопросов.
func (k *KnownHosts) Callback(ctx context.Context, a TrustAsker) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if remote == nil {
			return errors.New("проверка ключа сервера: не указан адрес сервера")
		}
		err := k.check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) > 0 {
			return k.changedError(hostname, keyErr, key)
		}

		ok, err := a.AskTrust(ctx, trustQuestion(hostname, key))
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return err
		}
		if !ok {
			return &userError{msg: msgHostNotTrusted, cause: ErrHostNotTrusted}
		}

		k.mu.Lock()
		defer k.mu.Unlock()
		// Пока ждали ответ, другое соединение могло уже записать этот ключ.
		err = k.checkLocked(hostname, remote, key)
		if err == nil {
			return nil
		}
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) > 0 {
			return k.changedError(hostname, keyErr, key)
		}
		return k.add(hostname, key)
	}
}

// add дописывает ключ в файл; вызывается под k.mu.
func (k *KnownHosts) add(hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	data, err := os.ReadFile(k.path) //nolint:gosec // G304: path comes from the app data dir
	if err != nil {
		return fmt.Errorf("чтение known_hosts: %w", err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		line = "\n" + line
	}
	f, err := os.OpenFile(k.path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: path comes from the app data dir
	if err != nil {
		return fmt.Errorf("запись в known_hosts: %w", err)
	}
	if _, err := f.WriteString(line); err != nil {
		return errors.Join(fmt.Errorf("запись в known_hosts: %w", err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("запись в known_hosts: %w", err)
	}
	return nil
}
