# Builds the release from a fresh clone and checks what came out.
#
# A build that only works in the checkout it was developed in is not a release:
# this script clones the repository, builds there, and then asks the binary what
# it is. It also checks the binary carries no path from the machine that built
# it, which -trimpath is what makes true.
param(
    [string]$Ref = "HEAD",
    # Empty means "read VERSION at the repository root".
    [string]$Version = "",
    [string]$WorkDir = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($Version)) {
    $versionFile = Join-Path $root "VERSION"
    if (-not (Test-Path $versionFile)) { throw "no VERSION file at $versionFile" }
    $Version = (Get-Content $versionFile -Raw).Trim()
}
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

# The icon and the version block are resources Windows reads from the file, not
# from the running program: a build without them still runs and still reports
# the right version, so nothing else would notice. Read the resource directory
# itself rather than scanning the bytes, and ask Windows for the version block.
function Get-PeResourceTypes([string]$path) {
    $bytes = [IO.File]::ReadAllBytes($path)
    $lfanew = [BitConverter]::ToInt32($bytes, 0x3C)
    if ([BitConverter]::ToUInt32($bytes, $lfanew) -ne 0x00004550) { throw "$path is not a PE file" }
    $sections = [BitConverter]::ToUInt16($bytes, $lfanew + 6)
    $optionalSize = [BitConverter]::ToUInt16($bytes, $lfanew + 20)
    $optional = $lfanew + 24
    if ([BitConverter]::ToUInt16($bytes, $optional) -ne 0x20B) { throw "$path is not PE32+" }
    $directories = $optional + 112
    $resourceRva = [BitConverter]::ToUInt32($bytes, $directories + 2 * 8)
    if ($resourceRva -eq 0) { return @() }
    $table = $optional + $optionalSize
    $offset = -1
    for ($i = 0; $i -lt $sections; $i++) {
        $section = $table + $i * 40
        $virtualSize = [BitConverter]::ToUInt32($bytes, $section + 8)
        $virtualAddress = [BitConverter]::ToUInt32($bytes, $section + 12)
        $raw = [BitConverter]::ToUInt32($bytes, $section + 20)
        if ($resourceRva -ge $virtualAddress -and $resourceRva -lt $virtualAddress + $virtualSize) {
            $offset = $raw + ($resourceRva - $virtualAddress)
            break
        }
    }
    if ($offset -lt 0) { throw "the resource directory of $path is outside every section" }
    $named = [BitConverter]::ToUInt16($bytes, $offset + 12)
    $numbered = [BitConverter]::ToUInt16($bytes, $offset + 14)
    $types = @()
    for ($i = 0; $i -lt ($named + $numbered); $i++) {
        $entry = $offset + 16 + $i * 8
        $name = [BitConverter]::ToUInt32($bytes, $entry)
        if (($name -band 0x80000000) -ne 0) { continue }   # a named type, not one Windows numbers
        $types += [int]$name
    }
    return $types
}

$types = Get-PeResourceTypes $exe
foreach ($wanted in @(@{ Id = 3; Name = "icons" }, @{ Id = 14; Name = "icon group" }, @{ Id = 16; Name = "version block" })) {
    if ($types -notcontains $wanted.Id) {
        throw "the binary has no $($wanted.Name) resource; the release would show the toolchain's defaults (types: $($types -join ', '))"
    }
}
Write-Output "ok: the binary carries icons, an icon group and a version block (types $($types -join ', '))"

$info = (Get-Item $exe).VersionInfo
if ($info.ProductName -ne "MuxShift") { throw "the version block names '$($info.ProductName)' as the product" }
if ([string]::IsNullOrWhiteSpace($info.FileVersion)) { throw "the version block carries no file version" }
Write-Output "ok: the version block says $($info.ProductName) $($info.FileVersion)"
$sha = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
Write-Output "sha256 $sha"

# The same commit built in two different directories has to give the same
# bytes, or a checksum in VERSION.txt says nothing about what a person
# downloaded. A second clone at another path is the actual claim: rebuilding in
# the same directory would not catch a path baked into the binary.
$clone2 = Join-Path $WorkDir "clone-elsewhere"
Write-Output "cloning again into $clone2"
& git clone -q --no-hardlinks $root $clone2
if ($LASTEXITCODE -ne 0) { throw "the second clone failed" }
& git -C $clone2 checkout -q $Ref
if ($LASTEXITCODE -ne 0) { throw "checkout $Ref in the second clone failed" }
& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $clone2 "scripts\release.ps1") -Version $Version -OutRoot "dist" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "the build in the second clone failed" }
$again = (Get-FileHash -Algorithm SHA256 (Join-Path $clone2 "dist\muxshift-$Version-windows-amd64\muxshift.exe")).Hash.ToLower()
if ($again -ne $sha) {
    throw "the same commit built in two directories differs: $sha then $again"
}
Write-Output "ok: the same commit built in two directories gives the same bytes"
