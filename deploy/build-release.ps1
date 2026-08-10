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
  $tarPath = Join-Path $bundle "images/$($image.Name).tar"
  Invoke-ContainerEngine save --output $tarPath $image.Tag
  # gzip in place: podman load reads a gzip-compressed tar directly, so the
  # disconnected host never has to decompress an image before loading it -
  # only the outer bundle needs one extraction.
  $inStream = [System.IO.File]::OpenRead($tarPath)
  $outStream = [System.IO.File]::Create("$tarPath.gz")
  $gzipStream = New-Object System.IO.Compression.GZipStream($outStream, [System.IO.Compression.CompressionLevel]::Optimal)
  $inStream.CopyTo($gzipStream)
  $gzipStream.Dispose(); $outStream.Dispose(); $inStream.Dispose()
  Remove-Item -LiteralPath $tarPath
}

@"
# Load Observatory $Version (linux/amd64 / x86_64)

This archive contains gzip-compressed OCI image archives (images/*.tar.gz).
Load them directly on the disconnected deployment host with Podman - no
separate decompression step needed, e.g. ``podman load -i images/controller.tar.gz`` -
then follow `deploy/offline-deploy.md`.
"@ | Set-Content -LiteralPath (Join-Path $bundle 'README.md') -NoNewline

Get-ChildItem -LiteralPath (Join-Path $bundle 'images') -Filter '*.tar.gz' | ForEach-Object {
  Get-FileHash -Algorithm SHA256 $_.FullName
} | ForEach-Object { "$($_.Hash.ToLower())  $($_.Path | Split-Path -Leaf)" } | Set-Content -LiteralPath (Join-Path $bundle 'SHA256SUMS')

tar -czf $archive -C $OutputDirectory (Split-Path $bundle -Leaf)
Write-Host "Created $archive"
