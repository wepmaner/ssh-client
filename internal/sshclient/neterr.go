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
		return &userError{msg: fmt.Sprintf("Адрес %s не найден: проверьте имя хоста или подключение к сети", host), cause: err}
	case errors.As(err, &dns):
		return &userError{msg: fmt.Sprintf("Не удалось узнать адрес %s: нет связи с DNS-сервером", host), cause: err}
	}
	return fmt.Errorf("подключение к %s: %w", addr, err)
}

// listenError объясняет по-русски, почему не удалось занять локальный адрес проброса; системный
// текст остаётся причиной для подробностей карточки. Порт показывается числом, адрес — только
// если он не 127.0.0.1.
func listenError(addr string, err error) error {
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		host, port = "", addr
	}
	switch {
	case errors.Is(err, errAddrInUse):
		shown := port
		if host != "" && host != "127.0.0.1" {
			shown = addr
		}
		return &userError{msg: fmt.Sprintf("Порт %s уже занят другой программой: выберите другой порт или закройте программу, которая его держит", shown), cause: err}
	case errors.Is(err, errAccess):
		return &userError{msg: fmt.Sprintf("Windows не даёт занять порт %s: он зарезервирован системой (часто Hyper-V или WSL), выберите другой порт", port), cause: err}
	case errors.Is(err, errAddrNotAvail):
		if host == "" {
			host = addr
		}
		return &userError{msg: fmt.Sprintf("Адрес %s не принадлежит этому компьютеру: укажите 127.0.0.1 или адрес своей сети", host), cause: err}
	}
	return &userError{msg: fmt.Sprintf("Не удалось занять локальный порт %s", port), cause: err}
}

// remoteListenError объясняет отказ сервера открыть порт для проброса R.
func remoteListenError(addr string, err error) error {
	_, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		port = addr
	}
	return &userError{msg: fmt.Sprintf("Сервер не открыл порт %s: он занят на сервере или проброс запрещён его настройкой (AllowTcpForwarding, GatewayPorts)", port), cause: err}
}

// listenerBrokenError — локальный слушатель перестал принимать соединения уже после запуска.
func listenerBrokenError(addr string, err error) error {
	_, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		port = addr
	}
	return &userError{msg: fmt.Sprintf("Локальный порт %s перестал принимать соединения", port), cause: err}
}
