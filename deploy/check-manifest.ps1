$ErrorActionPreference = 'Stop'
$manifest = Get-Content -Raw "$PSScriptRoot/k8s.yaml"
if ($manifest -notmatch 'kind: NetworkPolicy') { throw 'NetworkPolicy is required' }
if ($manifest -notmatch 'kind: Deployment') { throw 'Deployment is required' }
