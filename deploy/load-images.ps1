param(
  [Parameter(Mandatory)] [string]$ArchiveDirectory,
  [Parameter(Mandatory)] [string]$Registry,
  [string]$Version = 'v0.4.1'
)

$ErrorActionPreference = 'Stop'
$images = @(
  @{ Archive = 'controller.tar.gz'; Source = "load-observatory/controller:$Version"; Destination = "load-observatory/controller:$Version" },
  @{ Archive = 'agent.tar.gz'; Source = "load-observatory/agent:$Version"; Destination = "load-observatory/agent:$Version" },
  @{ Archive = 'web.tar.gz'; Source = "load-observatory/web:$Version"; Destination = "load-observatory/web:$Version" },
  @{ Archive = 'postgres-16.tar.gz'; Source = 'postgres:16'; Destination = 'load-observatory/postgres:16' }
)

$loadSeparately = Test-Path -LiteralPath $ArchiveDirectory -PathType Container
if (-not $loadSeparately) {
  if (-not (Test-Path -LiteralPath $ArchiveDirectory -PathType Leaf)) {
    throw "Archive file or image directory not found: $ArchiveDirectory"
  }
  podman load -i $ArchiveDirectory
  if ($LASTEXITCODE -ne 0) { throw "podman load failed: $ArchiveDirectory" }
}

foreach ($image in $images) {
  if ($loadSeparately) {
    podman load -i (Join-Path $ArchiveDirectory $image.Archive)
    if ($LASTEXITCODE -ne 0) { throw "podman load failed: $($image.Archive)" }
  }
  $destination = "$Registry/$($image.Destination)"
  podman tag $image.Source $destination
  podman push $destination
}
