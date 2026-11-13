# Prints the GitHub Release body for one version of CHANGELOG.md.
#
# The release body is not written twice: this takes the section for the tag,
# checks it has both languages in order, that every category carries at least
# one concrete item, and that nothing is left as a placeholder. It then writes
# the text with a title, so a release cannot quietly ship an empty section.
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [string]$Changelog = "CHANGELOG.md",
    # Empty means "print to stdout"; a path writes UTF-8 without a BOM.
    [string]$Out = ""
)

$ErrorActionPreference = "Stop"

$zhHeading = '### ' + [string][char]0x7B80 + [char]0x4F53 + [char]0x4E2D + [char]0x6587

if ($Tag -notmatch '^v((?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$') {
    throw "the tag '$Tag' is not vX.Y.Z"
}
$version = $Matches[1]

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

$text = "# MuxShift v$version`n`n" + (($body -join "`n").Trim()) + "`n"
if ([string]::IsNullOrWhiteSpace($Out)) {
    Write-Output $text.TrimEnd()
}
else {
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($Out, $text, $encoding)
    Write-Output "wrote $Out"
}
