<#
  MorgTweaker bootstrap installer.

  One-liner:
    irm https://raw.githubusercontent.com/UberMorgott/MorgTweaker/main/install.ps1 | iex

  Downloads the latest release's MorgTweaker.exe to %TEMP% and launches it.
  The EXE self-elevates via its embedded manifest (requireAdministrator), so
  Windows shows the UAC prompt on start.
#>

$ErrorActionPreference = 'Stop'

# --- Configuration ----------------------------------------------------------
$repo  = 'UberMorgott/MorgTweaker'
$asset = 'MorgTweaker.exe'
# ----------------------------------------------------------------------------

# GitHub requires TLS 1.2 for API calls on older PowerShell hosts.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

Write-Host "Resolving latest MorgTweaker release for $repo ..."

try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" `
        -Headers @{ 'User-Agent' = 'morgtweaker-installer' }
} catch {
    Write-Error "Could not query GitHub releases for '$repo'. Check the repo name and your network. ($_)"
    return
}

$dl = $release.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1
if (-not $dl) {
    Write-Error "No '$asset' asset found in the latest release of '$repo'. Has a release been published yet?"
    return
}

$dest = Join-Path $env:TEMP $asset
Write-Host "Downloading $($dl.name) ($([math]::Round($dl.size/1MB,1)) MB) ..."
Invoke-WebRequest -Uri $dl.browser_download_url -OutFile $dest -UseBasicParsing

Write-Host "Launching $dest (UAC prompt will appear) ..."
Start-Process -FilePath $dest
