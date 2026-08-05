# 폐쇄망 Kubernetes 배포

이 릴리스는 `linux/amd64` 이미지 아카이브입니다. 인터넷 연결이 가능한 빌드 호스트에서 `build-release.ps1`를 실행해 만들고, 생성된 `load-observatory-v0.1.0-amd64.tar.gz`를 폐쇄망으로 반입합니다.

## 1. 이미지 반입과 내부 레지스트리 게시

폐쇄망의 Podman 호스트에서 아카이브를 풉니다. `SHA256SUMS`로 반입 파일을 먼저 검증합니다.

```powershell
tar -xzf .\load-observatory-v0.1.0-amd64.tar.gz
Set-Location .\load-observatory-v0.1.0-amd64
Get-FileHash .\images\*.tar -Algorithm SHA256
powershell -ExecutionPolicy Bypass -File .\deploy\load-images.ps1 -ArchiveDirectory .\images -Registry registry.internal:5000 -Version v0.1.0
```

`registry.internal:5000`은 클러스터 노드가 접근할 수 있는 폐쇄망 레지스트리 주소로 바꿉니다. Kubernetes 런타임과 Podman 저장소가 공유되지 않는 경우가 일반적이므로, 모든 노드에 직접 `podman load`만 하는 대신 내부 레지스트리로 게시하는 방식을 권장합니다.

## 2. Secret 생성

기본 매니페스트에 비밀 값을 남기지 마십시오. 다음 값으로 별도 Secret을 생성합니다.

```powershell
$password = Read-Host 'PostgreSQL password'
$key = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($key)
$encryptionKey = [Convert]::ToBase64String($key)

kubectl create namespace load-observatory
kubectl -n load-observatory create secret generic postgres-credentials `
  --from-literal=POSTGRES_DB=load_observatory `
  --from-literal=POSTGRES_USER=load_observatory `
  --from-literal=POSTGRES_PASSWORD=$password `
  --from-literal=DATABASE_URL="postgres://load_observatory:$password@postgres:5432/load_observatory?sslmode=disable" `
  --from-literal=TARGET_API_KEY_ENCRYPTION_KEY=$encryptionKey
```

Secret 생성이 실패하면서 이미 존재한다고 나오면, 기존 Secret의 값이 올바른지 확인한 뒤 재사용하거나 명시적으로 교체합니다.

## 3. 이미지 주소 렌더링 및 배포

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\render-offline-manifest.ps1 -Registry registry.internal:5000 -Version v0.1.0 -OutputPath .\k8s-offline.yaml
kubectl apply -f .\k8s-offline.yaml
kubectl -n load-observatory rollout status deployment/controller
kubectl -n load-observatory rollout status deployment/agent
kubectl -n load-observatory rollout status deployment/web
```

렌더링 스크립트는 예시 `postgres-credentials` Secret 문서를 자동 제거합니다. 클러스터에 NVIDIA GPU가 없는 노드에는 `dcgm-exporter`가 정상 기동하지 않을 수 있으므로 GPU 노드 셀렉터 또는 toleration을 운영 환경에 맞게 추가합니다.

## 4. 반입 후 폐쇄망에서 직접 수정해야 하는 지점

기본 매니페스트(`k8s.yaml`)는 예시 값입니다. 아래는 코드를 고치는 게 아니라, 배포 시점에 환경에 맞게 값만 바꿔주면 되는 지점입니다.

| 지점 | 파일 | 기본값 | 바꿔야 하는 이유 |
|---|---|---|---|
| 내부 레지스트리 주소 | `render-offline-manifest.ps1 -Registry` | `registry.internal:5000` | 실제 폐쇄망 레지스트리 호스트로 교체 |
| PostgreSQL 비밀번호 · 암호화 키 | `k8s.yaml`의 `postgres-credentials` Secret (오프라인 절차는 2단계에서 별도 생성) | `change-this-before-deploying` | 예시 값 그대로 두면 안 됨 — 2단계 스크립트로 실제 값 생성 |
| Prometheus 주소 | `controller` Deployment의 `PROMETHEUS_URL` env | `http://prometheus:9090` | 사내에 이미 운영 중인 Prometheus/DCGM이 있으면 그쪽을 가리키고 번들 내 `prometheus`/`dcgm-exporter`/`node-exporter`는 제거 |
| GPU 노드 셀렉터/toleration | `dcgm-exporter` DaemonSet | 없음 (모든 노드에서 시도) | GPU가 없는 노드에서는 기동 실패가 정상이나, `nodeSelector`로 GPU 노드만 지정하면 불필요한 실패 로그가 없어짐 |
| Egress 허용 대역 | `limit-egress` NetworkPolicy | `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (RFC1918 전체) | 실제 내부망이 이보다 좁으면(예: 특정 VLAN만) 그 대역으로 좁히는 것을 권장 — 기본값은 "사설 대역이면 어디든 허용"이라 폐쇄망 안에서도 과도하게 넓음 |
| 대상 LLM 서버 호스트명 | 코드 아님, 등록 시 입력값 | — | `internal/core/target.go`의 `ValidateTarget`이 사설 IP 또는 `.internal` 접미사만 허용. 사내 LLM 서버 DNS가 `.internal`로 끝나지 않으면 사설 IP로 등록하거나 인프라 팀에 DNS 접미사 확인 필요 |
| 이미지 버전 태그 | `render-offline-manifest.ps1 -Version`, `load-images.ps1 -Version` | `v0.1.0` | 배포하는 릴리스 버전과 일치시켜야 함 (예: `v0.2.0`) |

Ingress/TLS는 번들에 포함하지 않았습니다. 폐쇄망 내부 접근만 필요하면 `web` Service를 `NodePort`나 사내 Ingress 컨트롤러로 노출하는 방식을 클러스터 관례에 맞춰 추가합니다.
