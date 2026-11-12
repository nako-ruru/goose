package main

// Minimal Wails v3 app: default everything (no extra browser args, no
// interceptor, no visual hosting flags) — pure framework baseline for
// drag/move latency comparison against minimal Tauri2.

import (
	"embed"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name: "minwails",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "minwails",
		Width:  900,
		Height: 600,
		URL:    "/",
	})

	if err := app.Run(); err != nil {
		panic(err)
	}
}
