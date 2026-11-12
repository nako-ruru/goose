// Minimal CGo-free Go + WebView2 via crgimenes/glaze (purego).
package main

import (
	"log"

	"github.com/crgimenes/glaze"
)

func main() {
	w, err := glaze.New(false)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Destroy()

	w.SetTitle("min-glaze")
	w.SetSize(900, 600, glaze.HintNone)
	w.Navigate("file:///D:/temp/opencode/minglaze/dist/index.html")
	w.Run()
}
