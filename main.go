package main

import (
	"embed"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/app"
	"sshclient/internal/appdir"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set by CI from the release tag through -X main.version=X.Y.Z
// (build/windows/Taskfile.yml); Settings shows it (Task 15).
var version = "dev" //nolint:unused // read only after -X links it into Settings

func main() {
	a := app.New(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "sshclient",
		Width:            1280,
		Height:           800,
		MinWidth:         900,
		MinHeight:        600,
		Frameless:        true,
		BackgroundColour: application.NewRGB(0x1a, 0x1d, 0x22),
		URL:              "/",
		Windows: application.WindowsWindow{
			Theme:                             application.Dark,
			DisableFramelessWindowDecorations: false,
		},
	})

	wapp := application.New(application.Options{
		Name:     "sshclient",
		Services: []application.Service{application.NewService(a)},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			// Масштабом WebView2 управляет Wails по DPI окна, иначе на двух мониторах
			// восстановленное окно подхватывает чужой масштаб (см. app/restorefix_windows.go).
			UseVisualHosting:    true,
			WebviewUserDataPath: webviewDataPath(),
		},
	})
	if err := wapp.Run(); err != nil {
		log.Fatal(err)
	}
}

// webviewDataPath keeps the WebView2 cache in the data folder, never next to
// the exe: Stash restores the package folder to its reference files.
func webviewDataPath() string {
	dir, err := appdir.Dir()
	if err != nil {
		log.Printf("webview data path: %v", err)
		return ""
	}
	return filepath.Join(dir, "WebView2")
}
