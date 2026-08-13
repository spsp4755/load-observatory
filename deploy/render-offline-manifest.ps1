param(
  [Parameter(Mandatory)][string]$Registry,
  [Parameter(Mandatory)][string]$OutputPath,
  [string]$Version = 'v0.4.0'
)

$ErrorActionPreference = 'Stop'
$manifest = Get-Content -Raw (Join-Path $PSScriptRoot 'k8s.yaml')
$manifest = [regex]::Replace($manifest, '(?ms)^apiVersion: v1\r?\nkind: Secret\r?\nmetadata:.*?^---\r?\n', '')
$replacements = @{
  'load-observatory/controller:latest' = "$Registry/load-observatory/controller:$Version"
  'load-observatory/agent:latest' = "$Registry/load-observatory/agent:$Version"
  'load-observatory/web:latest' = "$Registry/load-observatory/web:$Version"
  'postgres:16' = "$Registry/load-observatory/postgres:16"
  'prom/prometheus:v2.54.1' = "$Registry/load-observatory/prometheus:v2.54.1"
  'nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04' = "$Registry/load-observatory/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04"
  'prom/node-exporter:v1.8.2' = "$Registry/load-observatory/node-exporter:v1.8.2"
}
foreach ($source in $replacements.Keys) { $manifest = $manifest.Replace($source, $replacements[$source]) }
[System.IO.File]::WriteAllText($OutputPath, $manifest, (New-Object System.Text.UTF8Encoding($false)))
