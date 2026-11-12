# Interleaved A/B/C move bench: openidea vs rimuse vs native control, same instant
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class Mover2 {
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr after, int x, int y, int cx, int cy, uint flags);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int n);
}
'@

function Bench([IntPtr]$h, [string]$label, [int]$n = 60) {
  if ($h -eq [IntPtr]::Zero) { Write-Output "$label : no hwnd"; return }
  $sw = [Diagnostics.Stopwatch]::StartNew()
  for ($i = 0; $i -lt $n; $i++) {
    [void][Mover2]::SetWindowPos($h, [IntPtr]::Zero, 260 + ($i % 30) * 4, 140 + ($i % 25) * 3, 0, 0, 0x0011) # NOSIZE|NOACTIVATE
  }
  $sw.Stop()
  $per = [math]::Round($sw.ElapsedMilliseconds * 1000 / $n, 0)
  Write-Output ("$label : " + $per + "us/move (n=" + $n + ")")
}

$ours = Get-Process -Name rimuse-shell-test -EA SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
$oi = Get-Process -Name openidea -EA SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if (-not $ours) { Write-Output "rimuse window missing"; exit 1 }
if (-not $oi) { Write-Output "openidea window missing - cannot compare"; exit 1 }

# native control window
Add-Type -AssemblyName System.Windows.Forms
# use a WinForms borderless-ish normal window
$frm = New-Object System.Windows.Forms.Form
$frm.StartPosition = 'Manual'
$frm.Location = New-Object System.Drawing.Point(60, 60)
$frm.Size = New-Object System.Drawing.Size(400, 300)
$frm.Show() | Out-Null
$frm.Refresh()
Start-Sleep -Milliseconds 500

Write-Output "=== interleave x3 (same instant) ==="
for ($r = 1; $r -le 3; $r++) {
  Write-Output "--- round $r ---"
  Bench $ours.MainWindowHandle "OURS (rimuse)     "
  Bench $oi.MainWindowHandle  "OPENIDEA (tauri)   "
  Bench $frm.Handle           "CONTROL (winforms) "
}

$frm.Close()
$frm.Dispose()
"bench done"
