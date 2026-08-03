# Load Observatory

## Cache-aware distributed workload

Each Agent claims a distinct queued run, so scale the existing Agent Deployment replicas to distribute independent load-test runs. Within one run, choose `mixed` (default: 30% varied requests), `reuse` (repeat the same request), or `bypass` (vary every request). Variation adds only Load Observatory nonce data to the requests it creates: a model prompt suffix or web query parameters. It never clears a target cache or cancels traffic from any other service.

폐쇄망 Kubernetes에서 OpenAI 호환 모델 API와 내부 웹/API의 부하를 측정하는 MVP입니다.

## 현재 제공 기능

- 내부 IP 또는 `.internal` 도메인 대상만 등록
- VU(최대 500) 또는 RPS(최대 2,000) 실행, 최대 60분
- HTTP 성공/실패, P95 전체 지연, P95 TTFT 수집
- React 실행 화면과 Agent 폴링 실행

## 로컬 실행

```powershell
go run ./cmd/controller
go run ./cmd/agent
cd web; npm.cmd install; npm.cmd run dev
```

브라우저에서 Vite URL을 열고 사설 IP 또는 `.internal` URL을 입력합니다.

## 폐쇄망 반입

인터넷 연결 환경에서 각 이미지를 `linux/amd64`로 만들고 저장합니다.

```powershell
podman build --platform linux/amd64 -f deploy/Dockerfile.controller -t load-observatory/controller:latest .
podman build --platform linux/amd64 -f deploy/Dockerfile.agent -t load-observatory/agent:latest .
podman build --platform linux/amd64 -f deploy/Dockerfile.web -t load-observatory/web:latest .
podman save -o controller.tar load-observatory/controller:latest
podman save -o agent.tar load-observatory/agent:latest
podman save -o web.tar load-observatory/web:latest
podman pull postgres:16
podman pull prom/prometheus:v2.54.1
podman pull nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04
podman pull prom/node-exporter:v1.8.2
podman save -o postgres-16.tar postgres:16
podman save -o prometheus.tar prom/prometheus:v2.54.1
podman save -o dcgm-exporter.tar nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04
podman save -o node-exporter.tar prom/node-exporter:v1.8.2
```

폐쇄망 노드에서 내부 레지스트리에 올리고 배포합니다.

```powershell
.\deploy\load-images.ps1 -ArchiveDirectory C:\images -Registry registry.internal:5000
kubectl apply -f deploy/k8s.yaml
```

`load-images.ps1` also publishes PostgreSQL, Prometheus, DCGM Exporter, and Node Exporter as `load-observatory/*` images. Before applying, replace every manifest image with the matching `$Registry/load-observatory/*` destination printed by the script; this prevents Kubernetes from attempting an internet pull.

`deploy/k8s.yaml`의 세 이미지 이름을 내부 레지스트리 주소로 바꾼 후 적용합니다.

## 알려진 MVP 범위

Kubernetes 배포에서는 Controller가 `postgres-credentials` Secret의 `DATABASE_URL`을 사용해 실행 기록·등록 모델을 PostgreSQL에 영속화합니다. 배포 전에 `POSTGRES_PASSWORD`와 `DATABASE_URL`의 비밀번호를 함께 교체해야 합니다. 로컬에서 `DATABASE_URL`을 지정하지 않은 경우에만 메모리 저장소를 사용합니다.

등록 모델의 인증키는 PostgreSQL 스냅샷에 AES-GCM으로 암호화해 저장합니다. 배포 전에 `TARGET_API_KEY_ENCRYPTION_KEY`에 아래처럼 생성한 Base64 32바이트 키를 설정해야 합니다. 이 값이 없거나 형식이 틀리면 Controller는 시작하지 않습니다.

```powershell
$key = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($key)
[Convert]::ToBase64String($key)
```
