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
