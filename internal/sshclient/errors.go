// Package sshclient содержит SSH-клиент: интерактивные вопросы в потоке терминала и политику known_hosts.
package sshclient

import "errors"

var (
	// ErrCancelled означает, что пользователь отказался или прервал ввод (Ctrl+C, EOF, "нет").
	ErrCancelled = errors.New("отменено пользователем")
	// ErrHostKeyChanged означает, что ключ сервера не совпадает с записанным в known_hosts.
	ErrHostKeyChanged = errors.New("ключ сервера изменился")
	// ErrHostNotTrusted означает, что пользователь не подтвердил неизвестный ключ сервера.
	ErrHostNotTrusted = errors.New("сервер не подтверждён")
	// ErrTimeout означает превышение времени ожидания.
	ErrTimeout = errors.New("превышено время ожидания")
	// ErrAuthDenied означает, что сервер не принял ни ключ, ни ответы пользователя.
	ErrAuthDenied = errors.New("доступ запрещён")
)
