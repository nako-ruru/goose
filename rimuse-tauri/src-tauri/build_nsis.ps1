# NSIS packaging for the remote runner (invoked by build_command, which must
# stay free of quotes/parens/$ because the workflow splices it into a bash
# double-quoted string — that is what broke line 11 of the Serve step).
# All the real logic lives HERE, where quotes and parentheses are harmless.
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'

function Step($m) { Write-Output "=== $m ===" }

Step "download tauri-cli (prebuilt, no cargo install)"
$zip = Join-Path (Get-Location) 'tauri-cli.zip'
$cli = 'https://github.com/tauri-apps/tauri/releases/download/tauri-cli-v2.12.0/cargo-tauri-x86_64-pc-windows-msvc.zip'
try {
    Invoke-WebRequest -Uri $cli -OutFile $zip -UseBasicParsing -TimeoutSec 300
    "downloaded: $((Get-Item $zip).Length) bytes"
} catch {
    throw "tauri-cli download failed: $($_.Exception.Message)"
}

Step "extract"
try {
    Expand-Archive -Path $zip -DestinationPath (Get-Location) -Force
} catch {
    throw "extract failed: $($_.Exception.Message)"
}
if (-not (Test-Path 'cargo-tauri.exe')) { throw 'cargo-tauri.exe missing after extract' }
'cargo-tauri.exe present'

Step "install NSIS via chocolatey"
choco install nsis -y --no-progress
"choco exit code: $LASTEXITCODE"

# Trust the actual binary, not choco's exit code (it can return 3010=reboot).
$nsisDir = $null
foreach ($d in @('C:\Program Files (x86)\NSIS', 'C:\Program Files\NSIS')) {
    if (Test-Path (Join-Path $d 'makensis.exe')) { $nsisDir = $d; break }
}
if (-not $nsisDir) { throw 'makensis.exe not found in either standard NSIS location' }
$env:PATH = "$nsisDir;$env:PATH"
"NSIS dir: $nsisDir"

Step "tauri build (compile + bundle nsis)"
& .\cargo-tauri.exe build --bundles nsis
$code = $LASTEXITCODE
"tauri build exit: $code"
if ($code -ne 0) { throw "tauri build failed with exit $code" }

Step "collect installer"
$found = @(Get-ChildItem 'target\release\bundle\nsis\*.exe' -ErrorAction SilentlyContinue)
if ($found.Count -eq 0) { throw 'no NSIS output in target\release\bundle\nsis' }
$found | ForEach-Object { "  produced: $($_.Name) ($($_.Length) bytes)" }
Copy-Item $found[0].FullName 'target\release\rimuse-installer.exe' -Force
if (-not (Test-Path 'target\release\rimuse-shell.exe')) { throw 'bare rimuse-shell.exe missing from target\release' }
"installer staged: target\release\rimuse-installer.exe"

Step "ALL DONE"
exit 0
