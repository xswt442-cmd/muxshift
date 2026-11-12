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

$info = (& go version -m $exe) -join "`n"
if ($info -notmatch "-trimpath=true") {
    throw "the binary was not built with -trimpath"
}
Write-Output "ok: the binary was built with -trimpath"

$leak = $info | Select-String -SimpleMatch -Pattern ([IO.Path]::GetFullPath($root))
if ($leak) {
    throw "the binary's build info names the building checkout: $leak"
}
Write-Output "ok: no path from this checkout is in the binary"

# The executable's own file icon comes from the resource file beside the
# sources; a build without it still runs and still says the right version, so
# this is the only check that would notice.
$iconBytes = [IO.File]::ReadAllBytes((Join-Path $clone "desktop\icon.ico"))
$exeBytes = [IO.File]::ReadAllBytes($exe)
$start = [int]($iconBytes.Length / 2)
$needle = $iconBytes[$start..($start + 63)]
$found = $false
for ($i = 0; $i -le $exeBytes.Length - $needle.Length; $i++) {
    if ($exeBytes[$i] -ne $needle[0] -or $exeBytes[$i + 1] -ne $needle[1]) { continue }
    $same = $true
    for ($j = 0; $j -lt $needle.Length; $j++) {
        if ($exeBytes[$i + $j] -ne $needle[$j]) { $same = $false; break }
    }
    if ($same) { $found = $true; break }
}
if (-not $found) { throw "the icon payload is not in the binary, so its file icon is the toolchain's default" }
Write-Output "ok: the icon is part of the executable"
$sha = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
Write-Output "sha256 $sha"

# The same commit built twice has to give the same bytes, or a checksum in a
# VERSION.txt file says nothing about what a person downloaded.
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $clone "scripts\release.ps1") -Version $Version -OutRoot "dist2" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "the second build failed" }
$again = (Get-FileHash -Algorithm SHA256 (Join-Path $clone "dist2\muxshift-$Version-windows-amd64\muxshift.exe")).Hash.ToLower()
if ($again -ne $sha) {
    throw "two builds of the same commit differ: $sha then $again"
}
Write-Output "ok: the same commit built twice gives the same bytes"
