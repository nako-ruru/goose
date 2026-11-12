//go:build !wndlog

package main

// Normal build: no message interception (zero overhead).
func wndLogInterceptor() func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	return nil
}
