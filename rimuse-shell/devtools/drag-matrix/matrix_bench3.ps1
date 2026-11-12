# Matrix v3: glaze(cgo-free purego) + raw-go(cgo) + wails + tauri + control.
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class Mover7 {
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr after, int x, int y, int cx, int cy, uint flags);
}
'@

function Bench([IntPtr]$h, [string]$label, [int]$n = 60) {
  if ($h -eq [IntPtr]::Zero) { Write-Output "$label : no hwnd"; return }
  $sw = [Diagnostics.Stopwatch]::StartNew()
  for ($i = 0; $i -lt $n; $i++) {
    [void][Mover7]::SetWindowPos($h, [IntPtr]::Zero, 300 + ($i % 30) * 4, 150 + ($i % 25) * 3, 0, 0, 0x0011)
  }
  $sw.Stop()
  $per = [math]::Round($sw.ElapsedMilliseconds * 1000 / $n, 0)
  Write-Output ("$label : " + $per + "us/move")
}

# fresh launch all four
Get-Process -Name minwails,min-tauri,min-gowv,min-glaze -EA SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 2
$gl = Start-Process "$env:TEMP\min-glaze.exe" -PassThru
$g  = Start-Process "$env:TEMP\min-gowv.exe" -PassThru
$mw = Start-Process "$env:TEMP\minwails.exe" -PassThru
$mt = Start-Process "D:\temp\opencode\mintauri\src-tauri\target\release\min-tauri.exe" -PassThru

$deadline = (Get-Date).AddSeconds(40)
while ((Get-Date) -lt $deadline) {
  $gl.Refresh(); $g.Refresh(); $mw.Refresh(); $mt.Refresh()
  if ($gl.MainWindowHandle -ne 0 -and $g.MainWindowHandle -ne 0 -and $mw.MainWindowHandle -ne 0 -and $mt.MainWindowHandle -ne 0) { break }
  Start-Sleep -Milliseconds 500
}
Start-Sleep -Seconds 2

Add-Type -AssemblyName System.Windows.Forms
$frm = New-Object System.Windows.Forms.Form
$frm.StartPosition = 'Manual'
$frm.Location = New-Object System.Drawing.Point(40, 40)
$frm.Size = New-Object System.Drawing.Size(400, 300)
$frm.Show() | Out-Null
$frm.Refresh()
Start-Sleep -Milliseconds 400

Write-Output ("handles: glaze=" + $gl.MainWindowHandle + " gowv=" + $g.MainWindowHandle + " wails=" + $mw.MainWindowHandle + " tauri=" + $mt.MainWindowHandle)

for ($r = 1; $r -le 3; $r++) {
  Write-Output "--- round $r ---"
  Bench $gl.MainWindowHandle "GLAZE(purego)   "
  Bench $g.MainWindowHandle  "RAWGO(cgo)      "
  Bench $mw.MainWindowHandle "WAILS3          "
  Bench $mt.MainWindowHandle "TAURI2          "
  Bench $frm.Handle           "CONTROL         "
}

$frm.Close(); $frm.Dispose()
"matrix v3 done — four framework windows left open for feel-test"
