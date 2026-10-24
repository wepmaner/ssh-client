package sshclient

import (
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

	mu sync.Mutex
	cb ssh.HostKeyCallback
}

// OpenKnownHosts загружает known_hosts, создавая файл (0o600) и родительский каталог при отсутствии.
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
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("чтение known_hosts: %w", err)
	}
	return &KnownHosts{path: path, cb: cb}, nil
}

// Callback возвращает ssh.HostKeyCallback: неизвестный хост спрашивается у пользователя через p,
// изменившийся ключ отклоняется без вопросов.
func (k *KnownHosts) Callback(p *Prompter) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		k.mu.Lock()
		defer k.mu.Unlock()

		err := k.cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) > 0 {
			return fmt.Errorf("%w: возможна подмена сервера %s (атака «человек посередине»). "+
				"Записанный отпечаток %s, полученный %s. Если ключ сервера сменили намеренно, удалите его строку из %s",
				ErrHostKeyChanged, hostname, ssh.FingerprintSHA256(keyErr.Want[0].Key), ssh.FingerprintSHA256(key), k.path)
		}

		fp := ssh.FingerprintSHA256(key)
		ok, err := p.Confirm(fmt.Sprintf("Сервер %s неизвестен.\r\nОтпечаток %s\r\nДоверять и запомнить?", hostname, fp))
		if err != nil {
			return err
		}
		if !ok {
			return ErrCancelled
		}
		return k.add(hostname, key)
	}
}

// add дописывает ключ в файл и перезагружает проверку.
func (k *KnownHosts) add(hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
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
	cb, err := knownhosts.New(k.path)
	if err != nil {
		return fmt.Errorf("перечитывание known_hosts: %w", err)
	}
	k.cb = cb
	return nil
}
