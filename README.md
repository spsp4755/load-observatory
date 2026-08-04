# Load Observatory

## Cache-aware distributed workload

Each Agent claims a distinct queued run, so scale the existing Agent Deployment replicas to distribute independent load-test runs. Within one run, choose `mixed` (default: 30% varied requests), `reuse` (repeat the same request), or `bypass` (vary every request). Variation adds only Load Observatory nonce data to the requests it creates: a model prompt suffix or web query parameters. It never clears a target cache or cancels traffic from any other service.

폐쇄망 Kubernetes에서 OpenAI 호환 모델 API와 내부 웹/API의 부하를 측정하는 MVP입니다.

## 현재 제공 기능

- 내부 IP 또는 `.internal` 도메인 대상만 등록
- VU(최대 500) 또는 RPS(최대 2,000) 실행, 최대 60분
- HTTP 성공/실패, P95 전체 지연, P95 TTFT 수집
- React 실행 화면과 Agent 폴링 실행

## 현업 판단을 위한 측정 규칙

부하 결과를 운영 판단에 쓰려면 "요청을 몇 개 보냈는지"가 아니라 "몇 개가 끝났는지"가 기준이어야 합니다.

- **요청 생애주기 분리 집계**: 발행(`issued`) · 완료(`completed`) · 시간 종료 취소(`cancelled`) · HTTP 실패(`http_failures`) · 연결·전송 오류(`transport_errors`)를 각각 집계합니다. `issued = successes + http_failures + transport_errors + cancelled`가 항상 성립하므로, 끝나지 않은 요청이 집계에서 조용히 사라지지 않습니다.
- **완료율 게이트**: `min_completion_percent`(기본 95%) 미만이면 `IsRunStable`이 거짓을 반환하고 자동 탐색도 부하를 올리지 않습니다. "30 VU 중 10개만 완료"는 안정 용량으로 판정되지 않습니다.
- **종료 유예 (graceful drain)**: 실행 시간이 끝나면 새 요청 발행만 멈추고, 진행 중인 요청은 `drain_seconds` 동안 완료를 기다립니다. 사용자 취소와 가드레일 중단은 유예 없이 즉시 종료합니다.
- **안정 구간만 평가**: `steady_state_seconds` 이전에 **시작된** 요청은 P50/P95/TTFT/TTFO/ITL/TPOT에서 제외합니다. 워밍업과 램프업이 백분위를 왜곡하지 않습니다. 평가에 쓰인 표본 수는 `steady_state_samples`로 보고합니다.
- **매 초 관측**: 실행 중 Agent가 초당 `목표 부하 / 실제 활성 요청 / 대기 / 완료 RPS / 단계(warmup·load·drain·cooldown)`를 보고합니다. 완료된 실행에서는 초당 추세 표에 같은 값이 남습니다.
- **시나리오별 분리**: 단계마다 완료율·지연 분포·출력 토큰 처리량을 따로 집계하므로, 느린 단계가 전체 평균에 묻히지 않습니다.

### 캐시 정책과 측정 유효성

vLLM은 v0.6.0부터 automatic prefix caching이 기본 활성이고, 블록(기본 16토큰) 단위로 **앞쪽** 프리픽스를 재사용합니다.

- `reuse`: 두 번째 요청부터 prefix 히트율이 100%에 가까워져 prefill이 사실상 사라집니다. **decode 성능과 스케줄러만 측정**하는 설정입니다.
- `bypass`: 요청마다 프롬프트 맨 앞에 `[LO-<hash> <run>#<seq>]` nonce를 넣습니다. 해시를 맨 앞에 두는 이유는, 상수 문구가 앞에 오면 첫 16토큰 블록이 그대로 캐시 히트하기 때문입니다. nonce는 샤드 ID를 포함하므로 여러 Agent가 서로의 캐시를 히트하지 않습니다.
- `mixed`: 위 둘을 `variation_percent` 비율로 섞습니다. 운영에 가장 가깝습니다.

### 출력 길이 고정 (`ignore_eos`)

기본은 **꺼짐**입니다. `max_tokens`는 상한일 뿐이라 응답마다 생성 토큰 수가 달라지고, 그러면 TPOT·ITL을 실행 간 비교할 수 없습니다. 비교가 필요하면 켜세요. 단 `ignore_eos`는 vLLM·SGLang 확장이므로 모르는 필드를 거부하는 서버에서는 모든 요청이 실패합니다. 「모델 등록 → 연결 확인」이 `supports_ignore_eos`로 지원 여부를 먼저 알려줍니다.

### 토큰 수는 서버 값을 신뢰합니다

`stream_options.include_usage: true`로 요청해 서버의 `usage`를 그대로 씁니다. 폐쇄망에 모델별 tokenizer를 반입·동기화하지 않아도 되고, 서버 자신의 집계와 일치합니다. `usage`가 없는 응답은 **토큰 수를 알 수 없음으로 표시**합니다. 이때 함께 보여주는 스트림 청크 수는 참고값이며, 서버가 청크 하나에 여러 토큰을 담을 수 있으므로 **토큰 수가 아닙니다.**

### 분산 실행의 percentile

percentile은 선형 통계가 아니어서 샤드별 P95로 전체 P95를 계산할 수 없습니다. 각 Agent가 원시 지연 표본을 Controller로 올리고, Controller가 전체를 합산해 **한 번만** 계산합니다(`latency_scope: pooled_samples`). 단일 프로세스 실행과 동일한 값입니다. 표본이 상한에 닿으면 앞부분을 버리지 않고 균일하게 솎아냅니다.

### 서버측 지표와의 상관 (판정 근거)

`PROMETHEUS_URL`이 설정되면 실행 중 매 초 모델 서버와 GPU 상태를 함께 수집합니다. vLLM·SGLang·TGI를 자동 탐침하고, 버전에 따라 이름이 바뀐 지표는 양쪽을 모두 시도합니다(`vllm:kv_cache_usage_perc` ↔ `vllm:gpu_cache_usage_perc`, `sglang:` ↔ `sglang_`). 수집하지 못한 지표는 **0이 아니라 「미수집」으로 표시**합니다.

- **포화 판정**: KV 캐시 사용률이 높은 것만으로는 포화가 아닙니다. vLLM은 배치를 키우려고 캐시를 의도적으로 채웁니다. 포화는 **대기 요청이 지속되고 preemption이 발생할 때**입니다. 대기가 있는데 preemption이 없고 동시 실행이 `max_num_seqs`에 붙어 있으면 하드웨어가 아니라 **설정 한계**입니다.
- **병목 귀속**: `클라이언트 TTFT ≈ 서버 큐 대기 + prefill`인지 확인합니다. 설명되지 않는 비율이 30%를 넘으면 병목은 모델 서버가 아니라 부하 발생기·로드밸런서·연결 수립입니다. 반대로 서버가 보고한 시간이 클라이언트 TTFT보다 크면 그 지표는 이 실행의 요청만을 나타내지 않으므로(다른 트래픽·다른 인스턴스) 귀속을 거부합니다.
- **실행 무효화**: GPU XID 오류, 전력·온도 스로틀링, SM 클럭 하강, 손상된 출력, 캐시 정책과 모순되는 prefix 히트율 중 하나라도 있으면 「신뢰할 수 없음」으로 표시합니다. 스로틀링된 GPU가 보고하는 한계는 모델의 한계가 아닙니다.
- **GPU 병목 성격**: `DCGM_FI_DEV_GPU_UTIL`은 「커널이 하나라도 실행됐는지」를 재므로 동시 요청 1건에서도 100%에 가깝습니다. 이 값만 보면 수용량을 한 자릿수 배로 과소평가합니다. `DCGM_FI_PROF_DRAM_ACTIVE`(decode는 메모리 대역폭 바운드)와 `DCGM_FI_PROF_PIPE_TENSOR_ACTIVE`(prefill 우세)로 병목 성격을 분류합니다. 이 프로파일링 필드는 DCGM DCP 모듈이 필요하고 `default-counters.csv`에 없는 스로틀 필드(`DCGM_FI_DEV_{POWER,THERMAL}_VIOLATION`, `DCGM_FI_DEV_SM_CLOCK`)는 exporter ConfigMap에 직접 추가해야 합니다.

### 자동 용량 탐색은 곡선을 측정합니다

동시성을 배수로 올려 가며 각 단계를 실제로 측정하고, **SLO를 충족하는 최대 부하**를 한계로 보고합니다. 이진 탐색으로 경계만 찾지 않는 이유는 continuous batching 때문입니다: 부하를 올려도 처리량은 늘고 지연은 거의 그대로인 구간이 이어지다가, 한계를 지나면 지연이 급격히 꺾입니다. 일반 웹 서버와 반대이므로 **단일 동시성 측정은 수용량을 크게 과소평가합니다.**

한계는 눈대중이 아니라 SLO(`허용 P95`, `TTFT P95 제한`, `최소 완료율` 등)로 정의합니다. 낮은 단계가 실패한 뒤 높은 단계가 통과하면 한계로 세지 않습니다 — 용량은 아래에서부터 연속으로 유지돼야 합니다. 운영 제공은 한계의 70% 수준을 권장합니다.

### Goodput은 오류를 분모에 포함합니다

`SLO를 충족한 요청 / (성공 + 오류)`입니다. 성공만 분모로 삼으면 부하가 걸릴 때 어려운 요청을 흘려버리는 서버가 오히려 높은 점수를 받습니다. 시간 종료로 취소된 요청은 분모에서 제외하고, 완료율 게이트가 따로 판정합니다.

### 대화 이력 누적 (멀티턴)

기본은 **꺼짐**입니다. 켜면 에이전트·멀티턴 시나리오에서 각 턴의 실제 답변을 다음 요청에 함께 보내, 실제 챗·에이전트처럼 프롬프트가 턴마다 커집니다. 이 증가가 KV 캐시 압력과 TTFT 상승의 주된 원인이므로, 끈 상태로는 그 부하를 측정하지 않습니다. 시나리오별 `입력 토큰`으로 컨텍스트 증가를 확인할 수 있습니다.

### 출력 토큰이 크면 실행 시간을 늘려야 합니다

20,480 토큰을 초당 약 110 토큰으로 생성하면 단일 요청에 약 3분이 걸립니다. 60초 실행은 응답 한 건도 끝내지 못하므로 "몇 명 수용 가능"을 증명하지 못합니다. 실행 화면이 설정된 토큰 예산으로 소요 시간을 추정해 경고하고, 실행 시간·측정 시작 지점·종료 유예 권장값을 한 번에 적용할 수 있습니다. 코딩·RAG 워크로드는 최소 5분 이상의 측정 시간과 응답 1건 이상의 종료 유예를 두세요.

`예상 생성 속도(tok/s)`는 추정에만 쓰는 보정값입니다. 실제 측정된 출력 속도로 갱신하세요.

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
