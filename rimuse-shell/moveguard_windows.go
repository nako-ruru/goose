//go:build windows

package main

// moveguard: throttle the per-message WebView2 position notification.
//
// Wails calls chromium.NotifyParentWindowPositionChanged() on EVERY
// WM_MOVE AND WM_MOVING — two synchronous cross-process COM calls per
// dragged frame (165Hz display = 660 calls/s). When the browser/GPU
// process is busy (another full-screen app repainting), each call can take
// tens of ms, the move loop backs up, and the window trails the cursor —
// the "drag lag". Measured:6.2ms per position change idle vs ~46ms under
// load.
//
// The interceptor rate-limits those two messages to one pass each per
// MOVE_THROTTLE_NS; skipped WM_MOVE is dropped entirely (its Wails handler
// is only the notify + a debounced event), skipped WM_MOVING still returns
// TRUE (rect valid) so the system proceeds with the move unchanged.
// WM_EXITSIZEMOVE resets the limiter so the final settle notifies at once.
// Microsoft documents the notify as required for a11y/dialogs — a ≤25ms
// position staleness during an active drag is imperceptible.
//
// A/B: RIMUSE_NO_MOVE_GUARD=1 disables the guard (returns passthrough).
// All other messages are never touched.

import (
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const moveThrottleNS int64 = 500_000_000 //500ms default — see header comment

const (
	wmMove         = 0x0003
	wmMoving       = 0x0216
	wmExitSizeMove = 0x0232
)

// cooldownNS: per-message-type cooldown before Wails's
// NotifyParentWindowPositionChanged is allowed through again.
// Tunable via RIMUSE_MOVE_GUARD_MS (diagnostics / preference).
func cooldownNS() int64 {
	if v := os.Getenv("RIMUSE_MOVE_GUARD_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			return int64(ms) * 1_000_000
		}
	}
	return moveThrottleNS
}

var (
	moveGuardEnabled    = os.Getenv("RIMUSE_NO_MOVE_GUARD") != "1"
	lastMoveAllowedNs   atomic.Int64
	lastMovingAllowedNs atomic.Int64
)

func throttleMoveMessage(now int64, last *atomic.Int64, cooldown int64) bool {
	prev := last.Load()
	if now-prev < cooldown {
		return false // still inside the cooldown -> skip
	}
	last.Store(now)
	return true
}

func moveThrottleInterceptor() func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	return func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
		if !moveGuardEnabled {
			return 0, false
		}
		switch msg {
		case wmMove:
			if !throttleMoveMessage(time.Now().UnixNano(), &lastMoveAllowedNs, cooldownNS()) {
				return 0, true // swallow: skip Wails's notify for this frame
			}
		case wmMoving:
			if !throttleMoveMessage(time.Now().UnixNano(), &lastMovingAllowedNs, cooldownNS()) {
				return 1, true // swallow but tell the move loop the rect is valid
			}
		case wmExitSizeMove:
			// drag ended: let the very next move message through immediately
			lastMoveAllowedNs.Store(0)
			lastMovingAllowedNs.Store(0)
		}
		return 0, false
	}
}
