param(
  [string]$Version = 'v0.4.0',
  [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\release'),
  [ValidateSet('podman', 'docker')][string]$Engine = 'podman',
  [string]$Registry = 'harbor.kubagents-ofc.koreacb.com',
  [string]$HostName = 'load-observatory.kubagents-ofc.koreacb.com'
)

$ErrorActionPreference = 'Stop'

function Invoke-ContainerEngine {
  param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
  & $Engine @Arguments
  if ($LASTEXITCODE -ne 0) { throw "$Engine $($Arguments -join ' ') failed" }
}

function Write-Utf8NoBom {
  param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$Content)
  [System.IO.File]::WriteAllText($Path, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$bundle = Join-Path $OutputDirectory "load-observatory-$Version-amd64"
$archive = "$bundle.tar.gz"
$allImagesArchive = Join-Path $OutputDirectory "load-observatory-$Version-images-amd64.tar.gz"
$images = @(
  @{ Name = 'controller'; Tag = "load-observatory/controller:$Version"; Dockerfile = 'deploy/Dockerfile.controller' },
  @{ Name = 'agent'; Tag = "load-observatory/agent:$Version"; Dockerfile = 'deploy/Dockerfile.agent' },
  @{ Name = 'web'; Tag = "load-observatory/web:$Version"; Dockerfile = 'deploy/Dockerfile.web' },
  @{ Name = 'postgres-16'; Tag = 'postgres:16' }
)

Remove-Item -LiteralPath $bundle -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $allImagesArchive -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath "$allImagesArchive.sha256" -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path (Join-Path $bundle 'images') -Force | Out-Null
Copy-Item (Join-Path $root 'deploy') (Join-Path $bundle 'deploy') -Recurse

# The checked-in manifest already uses the selected hostname. Fail closed if a
# caller asks for another one without updating all OIDC/Ingress references.
$manifest = Get-Content -Raw (Join-Path $root 'deploy/k8s.yaml')
if ($manifest -notmatch [regex]::Escape($HostName)) { throw "deploy/k8s.yaml does not contain host $HostName" }

foreach ($image in $images) {
  if ($image.Dockerfile) {
    Invoke-ContainerEngine build --platform linux/amd64 -f (Join-Path $root $image.Dockerfile) -t $image.Tag $root
  } else {
    Invoke-ContainerEngine pull --platform linux/amd64 $image.Tag
  }
  $platform = (& $Engine image inspect --format '{{.Os}}/{{.Architecture}}' $image.Tag).Trim()
  if ($LASTEXITCODE -ne 0 -or $platform -ne 'linux/amd64') {
    throw "Expected linux/amd64 image for $($image.Tag), got '$platform'"
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

# A Docker archive can contain multiple images and Podman can read its gzip
# stream directly. This standalone artifact is the fastest closed-network
# path: one `podman load -i` loads every required image without extraction.
$allImagesTar = $allImagesArchive.Substring(0, $allImagesArchive.Length - 3)
$imageTags = @($images | ForEach-Object { $_.Tag })
Invoke-ContainerEngine save --output $allImagesTar @imageTags
$inStream = [System.IO.File]::OpenRead($allImagesTar)
$outStream = [System.IO.File]::Create($allImagesArchive)
$gzipStream = New-Object System.IO.Compression.GZipStream($outStream, [System.IO.Compression.CompressionLevel]::Optimal)
$inStream.CopyTo($gzipStream)
$gzipStream.Dispose(); $outStream.Dispose(); $inStream.Dispose()
Remove-Item -LiteralPath $allImagesTar
$allImagesHash = (Get-FileHash -Algorithm SHA256 $allImagesArchive).Hash.ToLower()
Write-Utf8NoBom -Path "$allImagesArchive.sha256" -Content "$allImagesHash  $(Split-Path $allImagesArchive -Leaf)`n"

powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $root 'deploy/render-offline-manifest.ps1') `
  -Registry $Registry -Version $Version -OutputPath (Join-Path $bundle 'k8s-harbor.yaml')

$bundleReadme = @"
# Load Observatory $Version (linux/amd64 / x86_64)

This archive contains gzip-compressed OCI image archives (images/*.tar.gz).
Load them directly on the disconnected deployment host with Podman - no
separate decompression step needed, e.g. ``podman load -i images/controller.tar.gz`` -
then follow `deploy/offline-deploy.md`.

Deployment defaults:
- Namespace: load-observatory
- Harbor: $Registry/load-observatory
- URL: https://$HostName
- Manifest: k8s-harbor.yaml
"@
Write-Utf8NoBom -Path (Join-Path $bundle 'README.md') -Content $bundleReadme

$imageChecksums = Get-ChildItem -LiteralPath (Join-Path $bundle 'images') -Filter '*.tar.gz' | ForEach-Object {
  Get-FileHash -Algorithm SHA256 $_.FullName
} | ForEach-Object { "$($_.Hash.ToLower())  images/$($_.Path | Split-Path -Leaf)" }
Write-Utf8NoBom -Path (Join-Path $bundle 'SHA256SUMS') -Content (($imageChecksums -join "`n") + "`n")

tar -czf $archive -C $OutputDirectory (Split-Path $bundle -Leaf)
if ($LASTEXITCODE -ne 0) { throw "Failed to create $archive" }
$archiveHash = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLower()
Write-Utf8NoBom -Path "$archive.sha256" -Content "$archiveHash  $(Split-Path $archive -Leaf)`n"
Write-Host "Created $archive"
Write-Host "Created $archive.sha256"
Write-Host "Created $allImagesArchive"
Write-Host "Created $allImagesArchive.sha256"
