package app

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Обход бага Wails v3 (beta.25) на системах с мониторами разного масштаба.
//
// Свёрнутое окно Windows паркует за пределами экранов, и оно числится за соседним
// монитором (например, 150%). Сразу после восстановления GetDpiForWindow ещё около
// 150 мс отдаёт DPI того монитора, Wails по нему выставляет WebView2 чужой масштаб, и
// страница растягивается (вьюпорт 1920/1.5 = 1280 px) — интерфейс крупнеет.
//
// Лечим так: после восстановления раз в кадр сравниваем DPI окна с DPI монитора, на
// котором окно реально стоит. Как только Windows обновит DPI окна — шлём окну
// WM_DPICHANGED с его же координатами: Wails ничего не двигает, но пересчитывает
// масштаб WebView2 по верному DPI. Сбрасывать раньше бесполезно и даже вредно —
// сброс по устаревшему DPI закрепляет неверный масштаб. Работает, только когда
// масштабом владеет Wails, — отсюда UseVisualHosting в main.go. Перенесено из Stash.

var (
	user32                = syscall.NewLazyDLL("user32.dll")
	shcore                = syscall.NewLazyDLL("shcore.dll")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procGetDpiForWindow   = user32.NewProc("GetDpiForWindow")
	procMonitorFromWindow = user32.NewProc("MonitorFromWindow")
	procSendMessageW      = user32.NewProc("SendMessageW")
	procGetDpiForMonitor  = shcore.NewProc("GetDpiForMonitor")
)

const (
	wmDpiChanged            = 0x02E0
	monitorDefaultToNearest = 2
	mdtEffectiveDpi         = 0
	frame                   = 16 * time.Millisecond
)

type rect struct{ left, top, right, bottom int32 }

func fixRestoreScale(window *application.WebviewWindow) {
	window.OnWindowEvent(events.Windows.WindowUnMinimise, func(*application.WindowEvent) {
		go func() {
			hwnd := uintptr(window.NativeWindow())
			for waited := time.Duration(0); waited < 700*time.Millisecond; waited += frame {
				time.Sleep(frame)
				if win, mon := windowDPI(hwnd), monitorDPI(hwnd); win != 0 && win == mon {
					application.InvokeAsync(func() { resyncScale(hwnd) })
					break
				}
			}
			// Подстраховка: WebView2 после долгого сворачивания может проснуться позже.
			time.Sleep(300 * time.Millisecond)
			application.InvokeAsync(func() { resyncScale(hwnd) })
		}()
	})
}

func windowDPI(hwnd uintptr) uint32 {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd) //nolint:gosec // G104: the DPI itself is the result, 0 on failure
	return uint32(dpi)                          //nolint:gosec // G115: GetDpiForWindow returns a UINT
}

func monitorDPI(hwnd uintptr) uint32 {
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest) //nolint:gosec // G104: checked as mon == 0
	if mon == 0 {
		return 0
	}
	var x, y uint32
	if hr, _, _ := procGetDpiForMonitor.Call(mon, mdtEffectiveDpi, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); hr != 0 { //nolint:gosec // G104: checked as hr != 0
		return 0
	}
	return x
}

func resyncScale(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	dpi := uintptr(windowDPI(hwnd))
	if dpi == 0 {
		return
	}
	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 { //nolint:gosec // G104: checked as ok == 0
		return
	}
	_, _, _ = procSendMessageW.Call(hwnd, wmDpiChanged, dpi<<16|dpi, uintptr(unsafe.Pointer(&r))) //nolint:gosec // G104: WM_DPICHANGED has no result to check
}
