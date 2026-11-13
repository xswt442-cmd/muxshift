# Prints the GitHub Release body for one version of CHANGELOG.md.
#
# The release body is not written twice: this takes the section for the tag,
# checks it has both languages in order, that every category carries at least
# one concrete item, and that nothing is left as a placeholder. It then adds the
# download section for the asset that was actually built, so a release cannot
# ship without saying what to download, how big it is and what its SHA-256 is.
#
# This file stays pure ASCII on purpose: Windows PowerShell 5.1 reads a .ps1
# without a BOM as ANSI, and any Chinese text in the source would be garbled and
# break the parser. The Chinese sentences below are UTF-8 base64 and decoded at
# run time, so editors cannot mangle them.
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [string]$Changelog = "CHANGELOG.md",
    # The archive that will be attached to the release. It must exist.
    [Parameter(Mandatory = $true)][string]$Asset,
    # Owner/name used in the download links, e.g. xswt442-cmd/muxshift.
    [string]$Repository = "",
    # Empty means "print to stdout"; a path writes UTF-8 without a BOM.
    [string]$Out = ""
)

$ErrorActionPreference = "Stop"

$zhHeading = '### ' + [string][char]0x7B80 + [char]0x4F53 + [char]0x4E2D + [char]0x6587

function Get-Utf8Text([string]$Base64) {
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($Base64))
}

if ($Tag -notmatch '^v((?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$') {
    throw "the tag '$Tag' is not vX.Y.Z"
}
$version = $Matches[1]

if ([string]::IsNullOrWhiteSpace($Repository)) {
    $Repository = $env:GITHUB_REPOSITORY
}
if ([string]::IsNullOrWhiteSpace($Repository)) {
    $Repository = "xswt442-cmd/muxshift"
}
if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
    throw "the repository '$Repository' is not owner/name"
}

if (-not (Test-Path $Asset)) { throw "no release asset at $Asset" }
$assetItem = Get-Item $Asset
$assetName = $assetItem.Name
$assetSize = $assetItem.Length
$assetHash = (Get-FileHash $assetItem.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
$assetUrl = "https://github.com/$Repository/releases/download/$Tag/" + [uri]::EscapeDataString($assetName)

$root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($Changelog) -or -not (Test-Path $Changelog)) {
    $Changelog = Join-Path $root "CHANGELOG.md"
}
if (-not (Test-Path $Changelog)) { throw "no changelog at $Changelog" }

$versionFile = Join-Path $root "VERSION"
if (Test-Path $versionFile) {
    $declared = (Get-Content $versionFile -Raw).Trim()
    if ($declared -ne $version) {
        throw "VERSION says '$declared' but the tag is '$Tag'; the two have to agree before a release"
    }
}

$lines = (Get-Content $Changelog -Raw -Encoding UTF8) -split "\r?\n"
$start = -1
$heading = '^## \[' + [regex]::Escape($version) + '\](?: - .+)?$'
for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match $heading) {
        if ($start -ge 0) { throw "CHANGELOG.md has more than one [$version] section" }
        $start = $i
    }
}
if ($start -lt 0) { throw "CHANGELOG.md has no [$version] section" }

$end = $lines.Count
for ($i = $start + 1; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -like '## *') { $end = $i; break }
}
$body = $lines[($start + 1)..($end - 1)]

$zhAt = -1
$enAt = -1
for ($i = 0; $i -lt $body.Count; $i++) {
    $line = $body[$i].Trim()
    if ($line -eq $zhHeading) {
        if ($zhAt -ge 0) { throw "the [$version] section has more than one Chinese block" }
        $zhAt = $i
    }
    elseif ($line -eq '### English') {
        if ($enAt -ge 0) { throw "the [$version] section has more than one English block" }
        $enAt = $i
    }
}
if ($zhAt -lt 0 -or $enAt -lt 0) { throw "the [$version] section needs both the Chinese and the English heading" }
if ($zhAt -gt $enAt) { throw "the Chinese block has to come before the English one" }

function Test-ReleaseSection {
    param([string[]]$Section, [string]$Language)

    $item = $false
    $sawCategory = $false
    $pendingCategory = $false
    foreach ($line in $Section) {
        $text = $line.Trim()
        if ($text -match '^<!--' -or $text -match '-->') {
            if ($text -ne '') { throw "$Language block still contains a template comment: $text" }
        }
        if ($text -match '\b(TODO|TBD)\b') { throw "$Language block still contains a placeholder: $text" }
        if ($text -match '^\*\*[^*]+\*\*$') {
            if ($sawCategory -and $pendingCategory) { throw "$Language block has an empty category before '$text'" }
            $sawCategory = $true
            $pendingCategory = $true
        }
        elseif ($text -match '^- ') {
            $item = $true
            $pendingCategory = $false
        }
    }
    if (-not $item) { throw "$Language block has no concrete change item" }
    if ($pendingCategory) { throw "$Language block ends with an empty category" }
}

$zh = $body[($zhAt + 1)..($enAt - 1)]
$en = $body[($enAt + 1)..($body.Count - 1)]
Test-ReleaseSection -Section $zh -Language 'Chinese'
Test-ReleaseSection -Section $en -Language 'English'

# Base64 of the Chinese wording, kept out of the source so PS 5.1 can parse the
# file as ASCII. Decode them here, next to the section they belong to.
$zhDownloadIntro = Get-Utf8Text 'V2luZG93cyAxMC8xMSB4NjTvvIzpnIDopoHns7vnu5/mj5DkvpvnmoQgTWVkaWEgRm91bmRhdGlvbiBILjI2NCDkuI4gQUFDIOino+eggeOAguino+WOi+WQjuebtOaOpei/kOihjCBtdXhzaGlmdC5leGXvvIzkuI3pnIDopoHlronoo4XjgII='
$zhTableHead = Get-Utf8Text 'fCDmk43kvZzns7vnu58gfCDmoLzlvI8gfCDnlKjpgJQgfCDkuIvovb0gfAp8IC0tLSB8IC0tLSB8IC0tLSB8IC0tLSB8'
$zhTableRow = Get-Utf8Text 'fCBXaW5kb3dzIHg2NCB8IFpJUO+8iOino+WOi+WNs+eUqO+8iSB8IOino+WOi+WQjui/kOihjCB8IFvkuIvovb1dKHswfSkgfA=='
$zhChecks = Get-Utf8Text '5Y+R5biD6ICF5rKh5pyJ5o+Q5L6b5pWw5a2X562+5ZCN77yb55So5LiL6Z2i55qEIFNIQS0yNTYg5qCh6aqM5paH5Lu25a6M5pW05oCn77yMVkVSU0lPTi50eHQg6K6w5b2V54mI5pys44CB5p6E5bu65o+Q5Lqk5LiOIGV4ZSDoh6rouqvnmoQgU0hBLTI1NuOAgg=='
$zhSize = Get-Utf8Text '5aSn5bCP'
$zhSum = Get-Utf8Text '5qCh6aqM5YC8'

function Format-Bytes([long]$Bytes) {
    return $Bytes.ToString("N0", [Globalization.CultureInfo]::InvariantCulture)
}

$zhSection = @(
    (""),
    ("#### " + (Get-Utf8Text '5LiL6L29')),
    (""),
    ($zhDownloadIntro),
    (""),
    ($zhTableHead),
    ($zhTableRow -replace '\{0\}', $assetUrl),
    (""),
    ("- " + $zhSize + ": " + (Format-Bytes $assetSize) + " bytes (" + $assetName + ")"),
    ("- " + $zhSum + ": " + $assetHash),
    (""),
    ("#### " + (Get-Utf8Text '5qCh6aqM5LiL6L29')),
    (""),
    ($zhChecks)
)

$enSection = @(
    (""),
    ("#### Downloads"),
    (""),
    ('Windows 10/11 x64. The system''s Media Foundation decoders must provide H.264 and AAC. Unpack the archive and run muxshift.exe; nothing is installed.'),
    (""),
    ("| OS | Package | Use | Download |"),
    ("| --- | --- | --- | --- |"),
    ("| Windows x64 | ZIP (no installer) | Unpack and run | [Download]($assetUrl) |"),
    (""),
    ("- Size: " + (Format-Bytes $assetSize) + " bytes (" + $assetName + ")"),
    ("- SHA-256: " + $assetHash),
    (""),
    ("#### Verify the download"),
    (""),
    ('This release carries no publisher signature. Check the file against the SHA-256 above; VERSION.txt records the version, the build commit and the SHA-256 of the executable itself.')
)

$zhBody = (($body[$zhAt..($enAt - 1)] + $zhSection) -join "`n").TrimEnd()
$enBody = (('### English' + "`n" + ($en -join "`n") + "`n" + ($enSection -join "`n"))).TrimEnd()
$text = "# MuxShift v$version`n`n" + $zhBody + "`n`n" + $enBody + "`n"

if ([string]::IsNullOrWhiteSpace($Out)) {
    Write-Output $text.TrimEnd()
}
else {
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($Out, $text, $encoding)
    Write-Output "wrote $Out"
}
