// Minimal RAW Go + WebView2 (webview_go binding) — no Wails, no Tauri.
// Same binding family as orbital-client, for the move/drag isolation matrix.
package main

import (
	"github.com/bisoncraft/webview_go"
)

func main() {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("min-gowv")
	w.SetSize(900, 600, webview.HintNone)
	w.Navigate("file:///D:/temp/opencode/minowv/dist/index.html")
	w.Run()
}
