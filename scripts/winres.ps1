# Regenerates desktop\rsrc_windows_amd64.syso from desktop\icon.ico.
#
# The object file carries two things Windows reads from the file itself rather
# than from the running program: the icon Explorer and the task bar show, and
# the version block the Properties dialog prints. It is generated once and
# committed, so a build needs neither this tool nor a network.
#
# The numeric version here is the compatibility version; the exact build and
# commit are what `muxshift.exe -version` prints. Run this again when the
# version changes, and commit the .syso together with the change.
param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$desktop = Join-Path $root "desktop"

Push-Location $desktop
try {
    & go run github.com/tc-hib/go-winres@latest simply `
        --out rsrc `
        --arch amd64 `
        --icon icon.ico `
        --product-name MuxShift `
        --file-description "MuxShift desktop" `
        --file-version $Version `
        --product-version $Version `
        --copyright "MIT licensed; see LICENSE"
    if ($LASTEXITCODE -ne 0) { throw "go-winres failed with exit code $LASTEXITCODE" }
}
finally {
    Pop-Location
}

$syso = Join-Path $desktop "rsrc_windows_amd64.syso"
if (-not (Test-Path $syso)) { throw "go-winres left no rsrc_windows_amd64.syso" }
Write-Output "wrote $syso ($((Get-Item $syso).Length) bytes, version $Version)"
