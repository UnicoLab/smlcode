# SLMCode Windows installer (PowerShell)
#
#   irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
#
# Pin a version:
#   $env:SLMCODE_VERSION = "v0.5.17"; irm …/install.ps1 | iex
#
# Uninstall:
#   irm …/install.ps1 | iex -Uninstall   (or pass -Uninstall when saved locally)

param(
    [string]$Version = $(if ($env:SLMCODE_VERSION) { $env:SLMCODE_VERSION } else { "latest" }),
    [string]$Repo = $(if ($env:SLMCODE_REPO) { $env:SLMCODE_REPO } else { "UnicoLab/smlcode" }),
    [switch]$Uninstall,
    [string]$Prefix = ""
)

$ErrorActionPreference = "Stop"
$BinName = "slmcode.exe"

if (-not $Prefix) {
    $Prefix = Join-Path $env:LOCALAPPDATA "slmcode"
}
$BinDir = Join-Path $Prefix "bin"
$Target = Join-Path $BinDir $BinName

function Ensure-UserPath([string]$Dir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $userPath) { $userPath = "" }
    $parts = $userPath -split ";" | Where-Object { $_ -and $_.Trim() -ne "" }
    if ($parts -notcontains $Dir) {
        $newPath = ($parts + $Dir) -join ";"
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        $env:Path = "$Dir;$env:Path"
        Write-Host "→ Added $Dir to your user PATH (open a new terminal if needed)"
    }
}

if ($Uninstall) {
    if (Test-Path $Target) {
        Remove-Item -Force $Target
        Write-Host "Removed $Target"
    } else {
        Write-Host "Not installed at $Target"
    }
    exit 0
}

$arch = $env:PROCESSOR_ARCHITECTURE
switch -Regex ($arch) {
    "ARM64" { $goArch = "arm64" }
    "AMD64|X64|x86_64" { $goArch = "amd64" }
    default { throw "Unsupported architecture: $arch" }
}

if ($Version -eq "latest") {
    $api = "https://api.github.com/repos/$Repo/releases/latest"
} else {
    $tag = $Version
    if (-not $tag.StartsWith("v")) { $tag = "v$tag" }
    $api = "https://api.github.com/repos/$Repo/releases/tags/$tag"
}

Write-Host "→ Resolving release from $Repo…"
$release = Invoke-RestMethod -Uri $api -Headers @{ "Accept" = "application/vnd.github+json"; "User-Agent" = "slmcode-installer" }
$tagName = $release.tag_name
$semver = $tagName.TrimStart("v")
$assetName = "slmcode_${semver}_windows_${goArch}.exe"
$asset = $release.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
if (-not $asset) {
    throw "Asset not found: $assetName (is this release built for Windows?)"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) $assetName
Write-Host "→ Downloading $assetName…"
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmp -UseBasicParsing

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
Copy-Item -Force $tmp $Target
Remove-Item -Force $tmp
Ensure-UserPath $BinDir

$metaDir = Join-Path $env:APPDATA "slmcode"
New-Item -ItemType Directory -Force -Path $metaDir | Out-Null
$meta = @{
    source       = ""
    prefix       = $Prefix
    mode         = "user"
    method       = "binary"
    version      = $semver
    git_commit   = ""
    binary       = $Target
    repo         = $Repo
    installed_at = (Get-Date).ToUniversalTime().ToString("o")
} | ConvertTo-Json
Set-Content -Path (Join-Path $metaDir "install.json") -Value $meta -Encoding UTF8

Write-Host ""
Write-Host "✔ SLMCode $semver installed → $Target"
& $Target version
Write-Host ""
Write-Host "Next:"
Write-Host "  slmcode doctor"
Write-Host "  cd your-project; slmcode init; slmcode"
Write-Host ""
Write-Host "Made with love by UnicoLab — https://unicolab.ai"
