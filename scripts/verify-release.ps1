# Builds the release from a fresh clone and checks what came out.
#
# A build that only works in the checkout it was developed in is not a release:
# this script clones the repository, builds there, and then asks the binary what
# it is. It also checks the binary carries no path from the machine that built
# it, which -trimpath is what makes true.
param(
    [string]$Ref = "HEAD",
    [string]$Version = "0.1.0-dev",
    [string]$WorkDir = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($WorkDir)) {
    $WorkDir = Join-Path ([IO.Path]::GetTempPath()) ("muxshift-verify-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
}
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$clone = Join-Path $WorkDir "clone"

Write-Output "cloning $root ($Ref) into $clone"
& git clone -q --no-hardlinks $root $clone
if ($LASTEXITCODE -ne 0) { throw "clone failed" }
& git -C $clone checkout -q $Ref
if ($LASTEXITCODE -ne 0) { throw "checkout $Ref failed" }
$commit = (& git -C $clone rev-parse --short HEAD).Trim()
Write-Output "  clone is at $commit"

& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $clone "scripts\release.ps1") -Version $Version -OutRoot "dist"
if ($LASTEXITCODE -ne 0) { throw "release build failed in the clone" }

$exe = Join-Path $clone "dist\muxshift-$Version-windows-amd64\muxshift.exe"
if (-not (Test-Path $exe)) { throw "the release directory has no binary at $exe" }

$reported = & $exe -version
if ($LASTEXITCODE -ne 0) { throw "the built binary cannot say what it is" }
if ($reported -notmatch [regex]::Escape($commit)) {
    throw "the binary names a different commit than the clone: $reported"
}
Write-Output "ok: the binary says $reported"

$info = & go version -m $exe
if ($info -notmatch "-trimpath=true") {
    throw "the binary was not built with -trimpath"
}
Write-Output "ok: the binary was built with -trimpath"

$leak = $info | Select-String -SimpleMatch -Pattern ([IO.Path]::GetFullPath($root))
if ($leak) {
    throw "the binary's build info names the building checkout: $leak"
}
Write-Output "ok: no path from this checkout is in the binary"

$sha = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
Write-Output "sha256 $sha"
Write-Output "like-for-like: build the same commit twice and this should not change"
