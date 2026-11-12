//go:build !windows

package main

// move guard is Windows-only (per-message WebView2 position notify lives in
// the Windows wndproc); other platforms pass every message through.
func moveThrottleInterceptor() func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	return nil
}
