//go:build wndlog

package main

// Dev-only window-message histogram (build with -tags wndlog): counts every
// WM_* message id seen by the pump via Wails's WndProcInterceptor and dumps a
// top-N report every5s to a log file. Used to name the message flood that
// saturates the message pump during session open. Atomic increments only —
// must stay cheap enough to run inside the pump.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"
)

const wndMsgSlots = 8192 // covers standard messages + WM_USER/WM_APP ranges

var wndMsgCounts [wndMsgSlots]atomic.Uint64

func wndLogInterceptor() func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	go func() {
		out := filepath.Join(os.TempDir(), "rimuse-wndmsg.log")
		f, _ := os.Create(out)
		if f != nil {
			defer f.Close()
		}
		prev := make([]uint64, wndMsgSlots)
		for i := 0; i < 8; i++ { // dump for ~40s
			time.Sleep(5 * time.Second)
			type row struct {
				msg uint32
				n   uint64
			}
			var rows []row
			for m := 0; m < wndMsgSlots; m++ {
				cur := wndMsgCounts[m].Load()
				if d := cur - prev[m]; d > 0 {
					rows = append(rows, row{uint32(m), d})
				}
				prev[m] = cur
			}
			sort.Slice(rows, func(a, b int) bool { return rows[a].n > rows[b].n })
			if f != nil {
				fmt.Fprintf(f, "=== window %ds ===\n", (i+1)*5)
				for k, r := range rows {
					if k >= 15 || r.n < 20 {
						break
					}
					fmt.Fprintf(f, "  msg=0x%04X (%d) x%d\n", r.msg, r.msg, r.n)
				}
				f.Sync()
			}
		}
	}()

	return func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
		if int(msg) < wndMsgSlots {
			wndMsgCounts[msg].Add(1)
		} else {
			_ = unsafe.Sizeof(msg)
		}
		return 0, false // let the normal pump process it
	}
}
