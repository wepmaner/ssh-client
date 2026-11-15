//go:build !windows

package app

import "errors"

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var errDragOutNoOS = errors.New("Перетаскивание в Проводник есть только в Windows")

func (a *App) dragOut(*filesTab, []string) error { return errDragOutNoOS }
