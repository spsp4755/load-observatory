param(
  [Parameter(Mandatory)] [string]$ArchiveDirectory,
  [Parameter(Mandatory)] [string]$Registry
)

$ErrorActionPreference = 'Stop'
$images = @(
  @{ Archive = 'controller.tar'; Source = 'load-observatory/controller:latest'; Destination = 'load-observatory/controller:latest' },
  @{ Archive = 'agent.tar'; Source = 'load-observatory/agent:latest'; Destination = 'load-observatory/agent:latest' },
  @{ Archive = 'web.tar'; Source = 'load-observatory/web:latest'; Destination = 'load-observatory/web:latest' },
  @{ Archive = 'postgres-16.tar'; Source = 'postgres:16'; Destination = 'load-observatory/postgres:16' },
  @{ Archive = 'prometheus.tar'; Source = 'prom/prometheus:v2.54.1'; Destination = 'load-observatory/prometheus:v2.54.1' },
  @{ Archive = 'dcgm-exporter.tar'; Source = 'nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04'; Destination = 'load-observatory/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04' },
  @{ Archive = 'node-exporter.tar'; Source = 'prom/node-exporter:v1.8.2'; Destination = 'load-observatory/node-exporter:v1.8.2' }
)

foreach ($image in $images) {
  podman load -i (Join-Path $ArchiveDirectory $image.Archive)
  $destination = "$Registry/$($image.Destination)"
  podman tag $image.Source $destination
  podman push $destination
}
