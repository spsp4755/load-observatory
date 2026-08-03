$ErrorActionPreference = 'Stop'
$manifest = Get-Content -Raw "$PSScriptRoot/k8s.yaml"
if ($manifest -notmatch 'kind: NetworkPolicy') { throw 'NetworkPolicy is required' }
if ($manifest -notmatch 'kind: Deployment') { throw 'Deployment is required' }

$loader = Get-Content -Raw "$PSScriptRoot/load-images.ps1"
foreach ($image in 'postgres:16', 'prom/prometheus:v2.54.1', 'nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04', 'prom/node-exporter:v1.8.2') {
  if ($loader -notmatch [regex]::Escape($image)) { throw "offline loader is missing $image" }
}

if ($manifest -match 'name: DATABASE_URL, value:') { throw 'DATABASE_URL must not be hard-coded in the controller deployment' }
if ($manifest -notmatch 'name: DATABASE_URL\s+valueFrom:') { throw 'controller DATABASE_URL must come from a Secret' }
if ($manifest -notmatch 'name: TARGET_API_KEY_ENCRYPTION_KEY\s+valueFrom:') { throw 'controller API key encryption must come from a Secret' }
