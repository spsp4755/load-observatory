$ErrorActionPreference = 'Stop'
$manifest = Get-Content -Raw "$PSScriptRoot/k8s.yaml"
if ($manifest -notmatch 'kind: NetworkPolicy') { throw 'NetworkPolicy is required' }
if ($manifest -notmatch 'kind: Deployment') { throw 'Deployment is required' }
if ($manifest -match 'change-this-before-deploying|replace-with-base64|replace-with-a-random') { throw 'example credentials must not be committed to the manifest' }
if ($manifest -match 'harbor-credentials') { throw 'cluster-managed registry auth must not require an app-specific pull Secret' }
if ($manifest -match 'image: .*dcgm-exporter|image: .*node-exporter|image: .*prometheus|kind: ClusterRole') { throw 'remote-model deployment must not install local-node monitoring workloads or cluster RBAC' }
if ($manifest -notmatch 'name: PROMETHEUS_URL, value: ""') { throw 'remote-model deployment must leave Prometheus disabled by default' }
if ($manifest -notmatch 'name: TARGET_ALLOWED_HOST_SUFFIXES, value: "\.internal,\.kubagents-ofc\.koreacb\.com"') { throw 'the approved model gateway domain must be present in the target allowlist' }
if ($manifest -notmatch 'runAsNonRoot: true') { throw 'non-root workload security context is required' }
if ($manifest -notmatch 'readinessProbe:') { throw 'readiness probes are required' }
if ($manifest -notmatch 'resources:') { throw 'resource requests and limits are required' }
if ($manifest -notmatch 'name: PGDATA, value: "/var/lib/postgresql/data/pgdata"') { throw 'PostgreSQL must initialize below the mount root for restricted CSI/NFS volumes' }

$loader = Get-Content -Raw "$PSScriptRoot/load-images.ps1"
foreach ($image in 'load-observatory/controller', 'load-observatory/agent', 'load-observatory/web', 'postgres:16') {
  if ($loader -notmatch [regex]::Escape($image)) { throw "offline loader is missing $image" }
}

if ($manifest -match 'name: DATABASE_URL, value:') { throw 'DATABASE_URL must not be hard-coded in the controller deployment' }
if ($manifest -notmatch 'name: DATABASE_URL\s+valueFrom:') { throw 'controller DATABASE_URL must come from a Secret' }
if ($manifest -notmatch 'name: TARGET_API_KEY_ENCRYPTION_KEY\s+valueFrom:') { throw 'controller API key encryption must come from a Secret' }
if ($manifest -notmatch 'name: CAPTURE_PROXY_TOKEN\s+valueFrom:') { throw 'capture proxy token must come from a Secret' }
