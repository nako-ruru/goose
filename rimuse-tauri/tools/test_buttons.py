"""Verify the two DirSwitcher buttons are actually wired now.

1. "在文件管理器中打开" -> invoke open_directory_in_explorer, then an Explorer
   window must appear.
2. "选择目录"           -> invoke directory_chooser, then a native folder
   dialog must appear. It is MODAL (blocks the shell's main thread), so it is
   fired with --nowait and closed from out here via WM_CLOSE.

Python + ctypes: no PowerShell/GBK encoding traps.
"""
import ctypes
import re
import subprocess
import sys
import time
from ctypes import wintypes

INVOKE = r"D:\projects\goose\rimuse-tauri\tools\invoke_cmd.mjs"
SHELL_EXE = r"C:\Users\PC\AppData\Local\Temp\rimuse-tauri-shell.exe"

user32 = ctypes.windll.user32

DLG_RE = re.compile(r"选择文件夹|Select Folder|浏览文件夹|Browse For Folder", re.I)
EXP_RE = re.compile(r"Temp|File Explorer|资源管理器|文件夹", re.I)


def log(m):
    print(f"[{time.strftime('%H:%M:%S')}] {m}", flush=True)


def visible_windows(pattern):
    """[(hwnd, title)] for top-level visible windows matching pattern."""
    found = []
    EnumProc = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)

    def cb(hwnd, _l):
        if user32.IsWindowVisible(hwnd):
            buf = ctypes.create_unicode_buffer(512)
            user32.GetWindowTextW(hwnd, buf, 512)
            if buf.value and pattern.search(buf.value):
                found.append((hwnd, buf.value))
        return True

    user32.EnumWindows(EnumProc(cb), 0)
    return found


def invoke(cmd, args=None, nowait=False):
    cmd_line = ["node", INVOKE, cmd, json_dumps(args or {})]
    if nowait:
        cmd_line.append("--nowait")
    r = subprocess.run(cmd_line, capture_output=True, text=True, timeout=60)
    return (r.stdout + r.stderr).strip()


def json_dumps(o):
    import json
    return json.dumps(o)


def main():
    log("launching shell (GOOSE_BINARY only)...")
    subprocess.run(["taskkill", "/IM", "rimuse-tauri-shell.exe", "/F"],
                   capture_output=True, text=True)
    time.sleep(1)
    subprocess.run(["cmd", "/c", "start", "", SHELL_EXE], shell=False)
    for _ in range(60):
        out = subprocess.run(["tasklist", "/FI", "IMAGENAME eq rimuse-tauri-shell.exe"],
                             capture_output=True, text=True).stdout
        if "rimuse-tauri-shell.exe" in out:
            break
        time.sleep(0.5)
    time.sleep(7)  # backend spawn + page boot
    log("shell up")

    # --- test1: openDirectoryInExplorer ---
    log("TEST1 open_directory_in_explorer...")
    r1 = invoke("open_directory_in_explorer", {"directoryPath": "C:\\Windows\\Temp"})
    log(f"  invoke => {r1}")
    time.sleep(3)
    exp = visible_windows(EXP_RE)
    log(f"  explorer windows: {len(exp)}  {[t for _, t in exp][:3]}")
    test1 = r1.startswith("OK true") and len(exp) > 0
    log(f"  => {'PASS' if test1 else 'FAIL'}")
    for hwnd, title in exp:
        if "Temp" in title:
            user32.PostMessageW(hwnd, 0x0010, 0, 0)  # WM_CLOSE

    # --- test2: directoryChooser (modal) ---
    log("TEST2 directory_chooser (modal; fired --nowait)...")
    r2 = invoke("directory_chooser", {}, nowait=True)
    log(f"  fired => {r2}")
    time.sleep(3)
    dlgs = visible_windows(DLG_RE)
    log(f"  dialog windows: {len(dlgs)}  {[t for _, t in dlgs][:3]}")
    test2_appeared = len(dlgs) > 0
    log(f"  dialog appeared: {'PASS' if test2_appeared else 'FAIL'}")

    test2_responds = False
    if test2_appeared:
        hwnd, title = dlgs[0]
        log(f"  closing dialog hwnd={hwnd} ({title})")
        user32.SendMessageW(hwnd, 0x0010, 0, 0)  # WM_CLOSE (modal: waits)
        time.sleep(3)
        r3 = invoke("get_app_config", {})
        log(f"  shell responsive after close: {r3}")
        test2_responds = r3.startswith("OK")
        log(f"  main thread unblocked: {'PASS' if test2_responds else 'FAIL'}")

    test2 = test2_appeared and test2_responds

    log("---")
    log(f"explorerBtn={'PASS' if test1 else 'FAIL'}  "
        f"folderDialog={'PASS' if test2 else 'FAIL'}")
    verdict = "BUTTON FIX PASS" if (test1 and test2) else "BUTTON FIX FAIL"
    log(verdict)
    return 0 if (test1 and test2) else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:  # noqa: BLE001
        log(f"FATAL: {type(e).__name__}: {e}")
        sys.exit(1)
