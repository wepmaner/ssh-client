package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"sshclient/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a := app.New()

	err := wails.Run(&options.App{
		Title:     "sshclient",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 0x1a, G: 0x1d, B: 0x22, A: 255},
		Windows: &windows.Options{
			DisableFramelessWindowDecorations: false,
			Theme:                             windows.Dark,
		},
		OnStartup:     a.Startup,
		OnBeforeClose: a.BeforeClose,
		OnShutdown:    a.Shutdown,
		Bind:          []interface{}{a},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
