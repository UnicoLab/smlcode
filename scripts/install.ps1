# SLMCode Windows installer (PowerShell)
#
#   irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
#
# Pin a version (the piped form cannot take parameters, so use the env var):
#   $env:SLMCODE_VERSION = "v0.17.0"; irm …/install.ps1 | iex
#
# Uninstall (save the script first — `iex` cannot take switches either):
#   irm …/install.ps1 -OutFile install.ps1; ./install.ps1 -Uninstall

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
$MetaDir = Join-Path $env:APPDATA "slmcode"

function Ensure-UserPath([string]$Dir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $userPath) { $userPath = "" }
    $parts = $userPath -split ";" | Where-Object { $_ -and $_.Trim() -ne "" }
    if ($parts -notcontains $Dir) {
        $newPath = ($parts + $Dir) -join ";"
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        $env:Path = "$Dir;$env:Path"
        Write-Host "-> Added $Dir to your user PATH (open a new terminal if needed)"
    } else {
        Write-Host "-> $Dir is already on your user PATH"
    }
}

if ($Uninstall) {
    if (Test-Path $Target) {
        Remove-Item -Force $Target
        Write-Host "Removed $Target"
    } else {
        Write-Host "Not installed at $Target"
    }
    $metaFile = Join-Path $MetaDir "install.json"
    if (Test-Path $metaFile) {
        Remove-Item -Force $metaFile
        Write-Host "Removed $metaFile"
    }
    Write-Host ""
    Write-Host "Left in place (yours, not the installer's): per-project .slmcode\ directories,"
    Write-Host "and the $BinDir entry on your user PATH. Remove those by hand if you want a clean slate."
    exit 0
}

$arch = $env:PROCESSOR_ARCHITECTURE
switch -Regex ($arch) {
    "ARM64" { $goArch = "arm64" }
    "AMD64|X64|x86_64" { $goArch = "amd64" }
    default { throw "Unsupported architecture: $arch (SLMCode publishes windows amd64 and arm64)" }
}

if ($Version -eq "latest") {
    $api = "https://api.github.com/repos/$Repo/releases/latest"
} else {
    $tag = $Version
    if (-not $tag.StartsWith("v")) { $tag = "v$tag" }
    $api = "https://api.github.com/repos/$Repo/releases/tags/$tag"
}

Write-Host "-> Resolving release from $Repo..."
$headers = @{ "Accept" = "application/vnd.github+json"; "User-Agent" = "slmcode-installer" }
try {
    $release = Invoke-RestMethod -Uri $api -Headers $headers
} catch {
    throw "Could not read $api. Is that a real release? See https://github.com/$Repo/releases`n$_"
}
$tagName = $release.tag_name
$semver = $tagName.TrimStart("v")

# Must match the artifact names produced by .github/workflows/release.yml:
#   slmcode_<version>_windows_<arch>.exe
$assetName = "slmcode_${semver}_windows_${goArch}.exe"
$asset = $release.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
if (-not $asset) {
    throw "Asset not found: $assetName (is release $tagName built for Windows $goArch?)"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) $assetName
Write-Host "-> Downloading $assetName..."
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmp -UseBasicParsing

# ---- Checksum verification -------------------------------------------------
# The Unix installer has verified SHA256SUMS since it was written; this script
# did not verify anything at all, which made the Windows one-liner the weakest
# install path in the project. A mismatch aborts; an ABSENT SHA256SUMS is loud
# rather than silent, because "we could not check" and "we checked and it was
# fine" must never look the same to the person reading the output.
$sums = $release.assets | Where-Object { $_.name -eq "SHA256SUMS" } | Select-Object -First 1
if ($sums) {
    $sumsPath = Join-Path ([System.IO.Path]::GetTempPath()) "slmcode-SHA256SUMS"
    Invoke-WebRequest -Uri $sums.browser_download_url -OutFile $sumsPath -UseBasicParsing
    $expected = $null
    foreach ($line in Get-Content $sumsPath) {
        # "<hex>  <name>" (sha256sum/shasum), optionally "<hex> *<name>" in binary mode.
        $fields = $line -split '\s+', 2
        if ($fields.Count -eq 2 -and $fields[1].Trim().TrimStart('*') -eq $assetName) {
            $expected = $fields[0].Trim().ToLower()
            break
        }
    }
    Remove-Item -Force $sumsPath -ErrorAction SilentlyContinue
    if ($expected) {
        $got = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLower()
        if ($got -ne $expected) {
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            throw "Checksum mismatch for $assetName`n  expected $expected`n  got      $got`nRefusing to install. Retry; if it persists, a proxy or TLS-inspecting middlebox may be rewriting the download."
        }
        Write-Host "-> Checksum OK (sha256 $got)"
    } else {
        Write-Warning "Could not verify checksum: $assetName is not listed in this release's SHA256SUMS. The binary was NOT verified."
    }
} else {
    Write-Warning "Could not verify checksum: release $tagName publishes no SHA256SUMS. The binary was NOT verified."
}
# ---------------------------------------------------------------------------

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
# Windows will not let a running executable be overwritten. Move the old one
# aside first so re-running the installer while `slmcode studio` is up does not
# fail with "the process cannot access the file".
if (Test-Path $Target) {
    $old = "$Target.old"
    Remove-Item -Force $old -ErrorAction SilentlyContinue
    try { Move-Item -Force $Target $old } catch { }
}
Copy-Item -Force $tmp $Target
Remove-Item -Force $tmp
Remove-Item -Force "$Target.old" -ErrorAction SilentlyContinue
Ensure-UserPath $BinDir

New-Item -ItemType Directory -Force -Path $MetaDir | Out-Null
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
Set-Content -Path (Join-Path $MetaDir "install.json") -Value $meta -Encoding UTF8

Write-Host ""
Write-Host "SLMCode $semver installed -> $Target"
& $Target version
Write-Host ""
Write-Host "Next:"
Write-Host "  slmcode doctor"
Write-Host "  cd your-project; slmcode init; slmcode"
Write-Host ""
Write-Host "Update later:"
Write-Host "  slmcode update"
Write-Host ""
Write-Host "Made with love by UnicoLab - https://unicolab.ai"
