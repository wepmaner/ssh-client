package sshclient

import (
	"errors"
	"fmt"
	"net"
)

// dialError explains a failed TCP connect in Russian; the system text (in English on
// most Windows installs, «connectex: No connection could be made…») stays as the cause
// for the details on the error card. Sessions and forwards both get it through Dial.
func dialError(addr string, err error) error {
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		host, port = addr, ""
	}
	var dns *net.DNSError
	switch {
	case isTimeout(err) || errors.Is(err, errTimedOut):
		return timeoutError(err)
	case errors.Is(err, errRefused):
		return &userError{msg: fmt.Sprintf("Сервер %s отклонил подключение к порту %s: SSH на нём не запущен или порт закрыт", host, port), cause: err}
	case errors.Is(err, errHostUnreachable) || errors.Is(err, errNetUnreachable):
		return &userError{msg: fmt.Sprintf("Нет маршрута до %s: сеть недоступна или адрес вне вашей сети", host), cause: err}
	case errors.As(err, &dns) && dns.IsNotFound:
		return &userError{msg: fmt.Sprintf("Адрес %s не найден: проверьте имя хоста", host), cause: err}
	case errors.As(err, &dns):
		return &userError{msg: fmt.Sprintf("Не удалось узнать адрес %s: нет связи с DNS-сервером", host), cause: err}
	}
	return fmt.Errorf("подключение к %s: %w", addr, err)
}
