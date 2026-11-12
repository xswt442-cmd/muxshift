# Builds the desktop release directory.
#
# The output is self-describing: the binary says which version and commit it was
# built from, the directory carries the same facts as text, and the third-party
# notice is generated from what the binary actually links rather than by hand.
#
# Nothing here needs a network beyond the Go modules the build already uses, and
# nothing reads a path from this working copy: -trimpath keeps the checkout out
# of the binary.
param(
    [string]$Version = "0.1.0-dev",
    [string]$OutRoot = "dist",
    [switch]$AllowDirty
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$commit = (& git -C $root rev-parse --short HEAD).Trim()
$dirty = (& git -C $root status --porcelain)
if ($dirty -and -not $AllowDirty) {
    throw "the working tree has changes; commit them first, or pass -AllowDirty to build a marked build"
}
$stamp = $Version
if ($dirty) {
    $stamp = "$Version+dirty"
}

$name = "muxshift-$stamp-windows-amd64"
$out = Join-Path $root (Join-Path $OutRoot $name)
New-Item -ItemType Directory -Force -Path $out | Out-Null

Push-Location (Join-Path $root "desktop")
try {
    & go build -trimpath -ldflags "-X main.buildVersion=$stamp -X main.buildCommit=$commit" -o (Join-Path $out "muxshift.exe") .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
}
finally {
    Pop-Location
}

$exe = Join-Path $out "muxshift.exe"
$reported = & $exe -version
if ($LASTEXITCODE -ne 0) { throw "the built binary cannot say what it is" }
if ($reported -notmatch [regex]::Escape($commit)) {
    throw "the binary does not name the commit it was built from: $reported"
}

$sha = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
$commitLine = "commit $commit"
if ($dirty) { $commitLine = "$commitLine (working tree had changes)" }
Set-Content -Path (Join-Path $out "VERSION.txt") -Encoding utf8 -Value @(
    "MuxShift $stamp",
    $commitLine,
    "built $([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ'))",
    "sha256 $sha",
    $reported
)

Copy-Item (Join-Path $root "LICENSE") (Join-Path $out "LICENSE") -Force
Copy-Item (Join-Path $root "README.md") (Join-Path $out "README.md") -Force
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $root "scripts\thirdparty.ps1") -Out (Join-Path $OutRoot "$name\THIRD-PARTY.md") | Out-Null
if ($LASTEXITCODE -ne 0) {
    # A release without an accurate notice is not a release: the generator
    # names the modules the binary links, and a failure there leaves the file
    # half-written.
    throw "thirdparty.ps1 failed with exit code $LASTEXITCODE, so the release has no trustworthy THIRD-PARTY.md"
}

$readme = @'
MuxShift desktop build
======================

Run muxshift.exe with an address or a local file:

    muxshift.exe -media https://example.invalid/stream.m3u8
    muxshift.exe -media "C:\videos\sample.mp4"

The window opens on the stage. Sources, Diagnostics, Settings and Mixer are the
other pages; Ctrl+1..5 jump between them, Ctrl+S shows the selected source,
Ctrl+H hears it, Ctrl+R retries a source that failed.

What this build needs from the machine
--------------------------------------
* Windows 10 or 11.
* ffmpeg and ffprobe on PATH for alignment and for measuring a recording's first
  timestamp. Everything else runs without them; those two commands are reported
  as missing rather than guessed at.
* The Microsoft Edge WebView2 runtime only if the optional web control panel is
  started (not part of a normal start).

What it does not ship
---------------------
* No media, no sample streams, and no vocal-separation weights.
* The third-party notice beside this file lists the Go modules the binary links
  and the licences of the copies it was built from.
'@
[IO.File]::WriteAllText((Join-Path $out "RUNNING.txt"), $readme, (New-Object Text.UTF8Encoding($false)))

Write-Output "built $out"
Write-Output "  $reported"
Write-Output "  sha256 $sha"
