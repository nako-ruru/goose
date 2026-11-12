# Move-path microbench: how fast can OUR window process actual position
# changes vs a bare native control window? (WM_NULL only tests empty msgs;
# this tests the WM_MOVING/POSCHANGING/bounds-sync path drag actually uses.)
Add-Type @'
using System;
using System.Runtime.InteropServices;
using System.Diagnostics;
public class Mover {
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr after, int x, int y, int cx, int cy, uint flags);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern IntPtr CreateWindowEx(uint ex, string cls, string name, uint style, int x, int y, int w, int h, IntPtr parent, IntPtr menu, IntPtr inst, IntPtr param);
  [DllImport("user32.dll")] public static extern bool DestroyWindow(IntPtr h);
  public struct RECT { public int Left, Top, Right, Bottom; }
}
'@

function Bench-Move([IntPtr]$h, [string]$label) {
  if ($h -eq [IntPtr]::Zero) { Write-Output "$label : no hwnd"; return }
  $sw = [Diagnostics.Stopwatch]::StartNew()
  for ($i = 0; $i -lt 120; $i++) {
    [void][Mover]::SetWindowPos($h, [IntPtr]::Zero, 200 + ($i % 40) * 4, 150 + ($i % 30) * 3, 0, 0, 0x0001) # SWP_NOSIZE|NOACTIVATE
  }
  $sw.Stop()
  $per = [math]::Round($sw.ElapsedMilliseconds * 1000 / 120, 1)  # microseconds each
  Write-Output ("$label : total=" + $sw.ElapsedMilliseconds + "ms for120 moves, avg=" + $per + "us/move")
}

# our window
$ours = Get-Process -Name rimuse-shell-test -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $ours) { Write-Output "shell not running"; exit 1 }

# control: bare native Win32 window
$style = 0x00CF0000 -bor 0x00200000  # WS_OVERLAPPEDWINDOW|WS_VISIBLE
$ctrl = [Mover]::CreateWindowEx(0, "Static", "control-bench", $style, 50, 50, 400, 300, [IntPtr]::Zero, [IntPtr]::Zero, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Milliseconds 300

# warmup ours first (one slow move is expected after idle)
[void][Mover]::SetWindowPos($ours.MainWindowHandle, [IntPtr]::Zero, 300, 200, 0, 0, 0x0001)
Start-Sleep -Milliseconds 200

Bench-Move $ours.MainWindowHandle "OURS (rimuse)     "
Bench-Move $ctrl "CONTROL (native)  "
Bench-Move $ours.MainWindowHandle "OURS round2       "
Bench-Move $ctrl "CONTROL round2    "

[void][Mover]::DestroyWindow($ctrl)
