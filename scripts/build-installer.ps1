# Builds the per-user MuxShift installer around an already built release
# directory. The release directory is not rebuilt here: scripts\release.ps1 owns
# the binary, while this script owns the setup wrapper and its shortcuts.
param(
    [string]$Version = "",
    [string]$ReleaseDir = "",
    [string]$OutDir = "dist",
    [string]$Iscc = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($Version)) {
    $versionFile = Join-Path $root "VERSION"
    if (-not (Test-Path $versionFile)) { throw "no VERSION file at $versionFile" }
    $Version = (Get-Content $versionFile -Raw).Trim()
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw "the version '$Version' is not x.y.z"
}
if ([string]::IsNullOrWhiteSpace($ReleaseDir)) {
    $ReleaseDir = Join-Path $root "dist\muxshift-$Version-windows-amd64"
}
if (-not (Test-Path (Join-Path $ReleaseDir "muxshift.exe"))) {
    throw "no muxshift.exe in release directory $ReleaseDir; run scripts\release.ps1 first"
}
$source = (Resolve-Path $ReleaseDir).Path
$out = $OutDir
if (-not [IO.Path]::IsPathRooted($out)) { $out = Join-Path $root $out }
New-Item -ItemType Directory -Force -Path $out | Out-Null
$out = (Resolve-Path $out).Path

if ([string]::IsNullOrWhiteSpace($Iscc)) {
    $candidates = @(
        (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"),
        "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "C:\Program Files\Inno Setup 6\ISCC.exe"
    )
    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) { $Iscc = $candidate; break }
    }
}
if ([string]::IsNullOrWhiteSpace($Iscc) -or -not (Test-Path $Iscc)) {
    throw "ISCC.exe was not found; install Inno Setup 6 or pass -Iscc"
}

$iss = Join-Path $root "installer\muxshift.iss"
& $Iscc "/DMyAppVersion=$Version" "/DMySourceDir=$source" "/DMyOutputDir=$out" $iss
if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed with exit code $LASTEXITCODE" }

$setup = Join-Path $out "muxshift-$Version-windows-amd64-setup.exe"
if (-not (Test-Path $setup)) { throw "Inno Setup produced no $setup" }
$size = (Get-Item $setup).Length
$hash = (Get-FileHash -Algorithm SHA256 $setup).Hash.ToLower()
Write-Output "built $setup"
Write-Output "  size $size"
Write-Output "  sha256 $hash"
