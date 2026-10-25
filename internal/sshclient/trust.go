package sshclient

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
)

// TrustQuestion — вопрос о доверии неизвестному ключу сервера.
type TrustQuestion struct {
	Host              string
	Port              int
	KeyType           string // например ssh-ed25519
	FingerprintSHA256 string
	FingerprintMD5    string
}

// TrustAsker спрашивает пользователя, доверять ли неизвестному ключу сервера. true — доверять;
// отмена ctx прерывает вопрос с ctx.Err(). В приложении это окно доверия, в тестах и как запасной
// путь — Prompter в консоли.
type TrustAsker interface {
	AskTrust(ctx context.Context, q TrustQuestion) (bool, error)
}

// trustQuestion собирает вопрос по адресу host:port из рукопожатия и ключу сервера.
func trustQuestion(hostname string, key ssh.PublicKey) TrustQuestion {
	q := TrustQuestion{Host: hostname, KeyType: key.Type(),
		FingerprintSHA256: ssh.FingerprintSHA256(key), FingerprintMD5: ssh.FingerprintLegacyMD5(key)}
	if host, port, err := net.SplitHostPort(hostname); err == nil {
		if n, err := strconv.Atoi(port); err == nil {
			q.Host, q.Port = host, n
		}
	}
	return q
}

// AskTrust задаёт вопрос о доверии в консоли. Ответ читается из потока ввода, поэтому прервать его
// может только закрытие ввода (EOF даёт ErrCancelled); отмена ctx проверяется до и после вопроса.
func (p *Prompter) AskTrust(ctx context.Context, q TrustQuestion) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	addr := q.Host
	if q.Port > 0 {
		addr = net.JoinHostPort(q.Host, strconv.Itoa(q.Port))
	}
	ok, err := p.Confirm(fmt.Sprintf("Сервер %s неизвестен.\r\nКлюч %s, отпечаток %s\r\nДоверять и запомнить? [yes/no] ",
		addr, q.KeyType, q.FingerprintSHA256))
	if cerr := ctx.Err(); cerr != nil {
		return false, cerr
	}
	return ok, err
}
