//go:build windows

package main

// Windows GUI processes inherit the registry PATH/USERNAME automatically
// (Explorer passes the environment block), so — exactly like upstream
// loginShellPath.ts which returns null on win32 — there is nothing to do.
func resolveLoginPATHUnix() string { return "" }
