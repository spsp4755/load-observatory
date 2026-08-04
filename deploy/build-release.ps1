param(
  [string]$Version = 'v0.1.0',
  [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\release'),
  [ValidateSet('podman', 'docker')][string]$Engine = 'podman'
)

$ErrorActionPreference = 'Stop'

function Invoke-ContainerEngine {
  param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
  & $Engine @Arguments
  if ($LASTEXITCODE -ne 0) { throw "$Engine $($Arguments -join ' ') failed" }
}

$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$bundle = Join-Path $OutputDirectory "load-observatory-$Version-amd64"
$archive = "$bundle.tar.gz"
$images = @(
  @{ Name = 'controller'; Tag = "load-observatory/controller:$Version"; Dockerfile = 'deploy/Dockerfile.controller' },
  @{ Name = 'agent'; Tag = "load-observatory/agent:$Version"; Dockerfile = 'deploy/Dockerfile.agent' },
  @{ Name = 'web'; Tag = "load-observatory/web:$Version"; Dockerfile = 'deploy/Dockerfile.web' },
  @{ Name = 'postgres-16'; Tag = 'postgres:16' },
  @{ Name = 'prometheus'; Tag = 'prom/prometheus:v2.54.1' },
  @{ Name = 'dcgm-exporter'; Tag = 'nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04' },
  @{ Name = 'node-exporter'; Tag = 'prom/node-exporter:v1.8.2' }
)

Remove-Item -LiteralPath $bundle -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path (Join-Path $bundle 'images') -Force | Out-Null
Copy-Item (Join-Path $root 'deploy') (Join-Path $bundle 'deploy') -Recurse

foreach ($image in $images) {
  if ($image.Dockerfile) {
    Invoke-ContainerEngine build --platform linux/amd64 -f (Join-Path $root $image.Dockerfile) -t $image.Tag $root
  } else {
    Invoke-ContainerEngine pull --platform linux/amd64 $image.Tag
  }
  Invoke-ContainerEngine save --output (Join-Path $bundle "images/$($image.Name).tar") $image.Tag
}

@"
# Load Observatory $Version (linux/amd64)

This archive contains OCI-compatible image archives. Load them on the disconnected deployment host with Podman, then follow `deploy/offline-deploy.md`.
"@ | Set-Content -LiteralPath (Join-Path $bundle 'README.md') -NoNewline

Get-ChildItem -LiteralPath (Join-Path $bundle 'images') -Filter '*.tar' | ForEach-Object {
  Get-FileHash -Algorithm SHA256 $_.FullName
} | ForEach-Object { "$($_.Hash.ToLower())  $($_.Path | Split-Path -Leaf)" } | Set-Content -LiteralPath (Join-Path $bundle 'SHA256SUMS')

tar -czf $archive -C $OutputDirectory (Split-Path $bundle -Leaf)
Write-Host "Created $archive"
