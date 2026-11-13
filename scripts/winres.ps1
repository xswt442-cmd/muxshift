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
    # Empty means "read VERSION at the repository root".
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($Version)) {
    $versionFile = Join-Path $root "VERSION"
    if (-not (Test-Path $versionFile)) { throw "no VERSION file at $versionFile" }
    $Version = (Get-Content $versionFile -Raw).Trim()
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw "the version '$Version' is not x.y.z; the Windows version block takes numbers only"
}
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
