param(
  [Parameter(Mandatory)] [string]$ArchiveDirectory,
  [Parameter(Mandatory)] [string]$Registry
)

$ErrorActionPreference = 'Stop'
foreach ($name in 'controller', 'agent', 'web') {
  podman load -i (Join-Path $ArchiveDirectory "$name.tar")
  podman tag "load-observatory/$name`:latest" "$Registry/load-observatory/$name`:latest"
  podman push "$Registry/load-observatory/$name`:latest"
}
