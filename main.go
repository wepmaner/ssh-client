package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/app"
	"sshclient/internal/appdir"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set by CI from the release tag through -X main.version=X.Y.Z
// (build/windows/Taskfile.yml); Settings shows it.
var version = "dev"

func main() {
	// Before the first appdir.Dir: older builds kept the data in %LOCALAPPDATA%.
	// The outcome also goes to migrate.log in the data folder (no console in release builds).
	if res, err := appdir.Migrate(); err != nil {
		log.Printf("data folder: %v", err)
	} else if res != appdir.NothingToMove {
		log.Printf("data folder: %s", res)
	}

	a := app.New(mainWindow(), version)

	wapp := application.New(application.Options{
		Name:           "sshclient",
		Services:       []application.Service{application.NewService(a)},
		SingleInstance: singleInstance(app.SecondLaunchHandler(a)),
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

// singleInstanceID names the per-user Windows mutex of the running copy. It never changes:
// builds with different IDs would run side by side and save over each other's vault.
const singleInstanceID = "ssh-client-wepmaner"

// singleInstance keeps one running copy: application.New of a second start notifies the
// first one (onLaunch raises its window there) and exits with code 0.
func singleInstance(onLaunch func(application.SecondInstanceData)) *application.SingleInstanceOptions {
	return &application.SingleInstanceOptions{
		UniqueID:               singleInstanceID,
		OnSecondInstanceLaunch: onLaunch,
	}
}

// mainWindow is the frameless main window. The title bar is drawn by the frontend;
// the decorations (shadow, rounded corners, Alt+Space system menu) stay with Windows.
// Composition hosting with non-client regions lets the frontend mark its maximise
// button (--wails-non-client-region: maximize in TitleBar.svelte): Windows then
// hit-tests it as HTMAXBUTTON and shows the Snap layouts on hover.
func mainWindow() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
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
			NonClientRegionSupport:            true,
			WebView2CompositionHosting:        true,
		},
	}
}

// webviewDataPath keeps the WebView2 cache in the data folder, never next to
// the exe: Stash restores the package folder to its reference files.
func webviewDataPath() string {
	dir, err := appdir.Dir()
	if err != nil {
		log.Printf("webview data path: %v", err)
	}
	return webviewDataDir(dir, err)
}

// webviewDataDir is the WebView2 folder under the data folder. Without one it
// falls back to the temp folder: an empty path would make WebView2 write next
// to the exe.
func webviewDataDir(dataDir string, err error) string {
	if err != nil || dataDir == "" {
		return filepath.Join(os.TempDir(), "sshclient", "WebView2")
	}
	return filepath.Join(dataDir, "WebView2")
}
