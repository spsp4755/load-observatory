# 공개 부하 테스트 도구 검토와 적용 우선순위

조사일: 2026-08-13. 공식 문서와 원본 GitHub 저장소만 확인했다. 목표는 외부 도구를 Load Observatory 런타임에 그대로 끼워 넣는 것이 아니라, 검증된 측정 의미론과 사용자 경험을 현재 Go runner와 React UI에 이식하는 것이다. 이 방식은 폐쇄망 Kubernetes 배포에서 추가 이미지, Python/JavaScript 실행 환경, 원격 패키지 다운로드와 임의 스크립트 실행을 피할 수 있다.

## 결론

가장 가치가 큰 다음 작업은 세 가지다.

1. **로컬 트래픽 트레이스 재생**: 실제 도착 시각과 입력·출력 길이를 익명화한 JSONL로 반입해 운영 트래픽을 재현한다. GuideLLM과 vLLM 모두 타임스탬프 기반 trace replay를 지원한다. [GuideLLM trace replay](https://github.com/vllm-project/guidellm/blob/main/docs/getting-started/benchmark.md#trace-replay-benchmarking), [vLLM `timed_trace`](https://docs.vllm.ai/en/latest/cli/bench/serve/#arguments)
2. **반복 실행과 변동성 표시**: 같은 조건을 여러 번 실행해 중앙값과 실행 간 편차를 보여 준다. 단 한 번의 P95나 최대 처리량만으로 용량을 확정하지 않도록 한다. AIPerf는 multi-run confidence와 반복 실행 비교를 제품 기능으로 제공한다. [AIPerf README](https://github.com/ai-dynamo/aiperf#analysis-and-monitoring)
3. **웹 테스트의 검증 규칙 강화**: 상태 코드뿐 아니라 응답 본문·헤더 검사, 단계별 SLO, 그룹별 결과를 제공한다. k6의 checks, tags/groups, thresholds가 이 모델을 검증한다. [k6 checks](https://grafana.com/docs/k6/latest/using-k6/checks/), [k6 tags and groups](https://grafana.com/docs/k6/latest/using-k6/tags-and-groups/), [k6 thresholds](https://grafana.com/docs/k6/latest/using-k6/thresholds/)

현재 Load Observatory에는 open/closed 부하, Poisson 도착, 누락 도착, warm-up/steady/drain, TTFT·TTFO·ITL·TPOT, goodput, 동시성 sweep, 멀티턴과 가중 사용자 journey, 서버·GPU 메트릭, 결과 export가 이미 있다. 따라서 외부 실행기를 새로 포함하는 것보다 위 세 기능을 기존 데이터 모델에 추가하는 편이 중복이 적고 폐쇄망 운영도 단순하다.

## 도구별 검토

### 1. vLLM `bench serve`

**핵심 기능.** OpenAI 호환 endpoint를 대상으로 요청률과 최대 동시성을 분리하고, Poisson/Gamma 도착 간격, warm-up, 선형·지수 RPS ramp, TTFT·TPOT·ITL·E2E percentile, goodput SLO, 상세 요청 결과와 타임라인을 지원한다. `timed_trace` 데이터셋은 타임스탬프와 입력·출력 길이를 사용해 원래 호출 간격을 재생한다. [vLLM CLI](https://docs.vllm.ai/en/latest/cli/bench/serve/)

**가져올 가치.** Load Observatory에 이미 이식한 지표와 도착률 의미론은 유지한다. 다음으로 가져올 것은 별도의 짧은 probe 요청으로 다른 트래픽의 stall을 측정하는 방식과 trace replay다. vLLM의 `--probe-request-rate`는 주 부하와 별도로 단일 토큰 요청을 보내 지연을 보고한다. 이는 “코딩 작업 100명 때문에 짧은 일반 질의가 얼마나 느려지는가”를 직접 보여 주는 데 적합하다. [vLLM `--probe-request-rate`](https://docs.vllm.ai/en/latest/cli/bench/serve/#probe-request-rate)

**폐쇄망 적합성.** 실행 자체는 로컬 endpoint, 로컬 데이터셋 경로와 로컬 tokenizer 경로를 사용할 수 있다. 그러나 `bench serve`는 vLLM 배포물의 일부이고 모델·tokenizer ID나 Hugging Face 데이터셋을 지정하면 외부 저장소가 필요할 수 있다. 폐쇄망에서는 버전이 고정된 vLLM 이미지, tokenizer 디렉터리, JSONL 데이터셋을 사전 반입해야 한다. Load Observatory의 경량 Go Agent 이미지에 vLLM 전체를 합치는 것은 피하고, 교차 검증이 필요할 때만 별도 도구 이미지로 사용한다. [vLLM dataset/tokenizer options](https://docs.vllm.ai/en/latest/cli/bench/serve/#arguments), [vLLM repository](https://github.com/vllm-project/vllm)

**적용 우선순위: P0(의미론 이식), P2(별도 검증 이미지).**

### 2. NVIDIA AIPerf

**핵심 기능.** OpenAI chat completions를 포함한 여러 endpoint, 동시성·요청률·최대 동시성 결합·trace replay, TTFT/ITL/token throughput, goodput, parameter sweep, adaptive SLA boundary 탐색, 반복 실행 신뢰 구간, Prometheus 서버 메트릭과 DCGM GPU telemetry를 제공한다. [AIPerf README](https://github.com/ai-dynamo/aiperf)

**가져올 가치.** 첫째, 같은 설정을 N회 반복해 실행 간 편차와 신뢰도를 표시한다. 둘째, 단일 SLO 경계만 찾는 대신 처리량과 TTFT/TPOT의 Pareto 후보를 보여 준다. 셋째, 서버가 제공한 token count를 우선하는 현재 정책을 유지한다. AIPerf도 서버 token count를 사용해 클라이언트 tokenizer 차이를 피하는 기능을 제공한다. [AIPerf release notes](https://github.com/ai-dynamo/aiperf/releases)

**폐쇄망 적합성.** AIPerf는 Python 기반 다중 프로세스 구조이며 endpoint·dataset·transport·metric plugin과 여러 선택 의존성을 가진다. 공개 데이터셋, tokenizer 자동 탐색, W&B·MLflow 같은 exporter는 외부 연결을 만들 수 있으므로 폐쇄망 기본값에서 모두 사용하지 않아야 한다. 도입한다면 릴리스 태그와 Python wheel을 고정한 amd64 이미지, 로컬 JSONL, 로컬 tokenizer 또는 서버 token count, 내부 Prometheus/DCGM endpoint만 허용하고 exporter는 비활성화한다. 공식 로컬 tokenizer 절차처럼 `HF_HUB_OFFLINE=1`과 `TRANSFORMERS_OFFLINE=1`도 고정한다. 현재 Go Agent에 라이브러리로 포함하지 않고 별도 검증 Job으로만 운영한다. [AIPerf architecture/features](https://github.com/ai-dynamo/aiperf), [AIPerf local tokenizer](https://github.com/ai-dynamo/aiperf/blob/main/docs/tutorials/local-tokenizer.md), [AIPerf GPU telemetry](https://github.com/ai-dynamo/aiperf/blob/main/docs/tutorials/gpu-telemetry.md)

**적용 우선순위: P0(반복 실행·변동성 UX), P1(Pareto 표시), P2(별도 검증 이미지).**

### 3. NVIDIA GenAI-Perf

**핵심 기능.** 동시성 또는 요청률 부하, 합성·파일 입력, TTFT·두 번째 토큰 시간·ITL·output-token throughput, JSON/CSV 산출물과 sweep을 제공한다. [GenAI-Perf README](https://github.com/triton-inference-server/perf_analyzer/blob/main/genai-perf/README.md)

**가져올 가치.** 기존 결과 형식과 지표를 AIPerf로 교차 확인할 때만 참고한다. 신규 기능의 설계 기준으로 사용하지 않는다.

**폐쇄망 적합성.** NVIDIA는 GenAI-Perf의 신규 기능 개발을 중단하고 AIPerf 사용을 권장한다. 설치에는 Python 패키지 또는 NVIDIA Triton SDK 이미지와 CUDA가 필요하다. 폐쇄망에 이미 고정된 GenAI-Perf 운영 절차가 없다면 새 이미지를 반입할 이유가 없다. [GenAI-Perf phase-out notice](https://github.com/triton-inference-server/perf_analyzer/blob/main/genai-perf/README.md), [NVIDIA migration guide](https://docs.nvidia.com/aiperf/dev/getting-started/migrating-from-gen-ai-perf)

**적용 우선순위: 제외.**

### 4. GuideLLM

**핵심 기능.** OpenAI 호환 HTTP backend, synchronous/concurrent/throughput/constant/Poisson/sweep profile, warm-up·cooldown, 오류·과포화 중단 조건, 합성·로컬 파일 데이터, 멀티턴과 tool calling, JSON/CSV/HTML 결과를 제공한다. 타임스탬프와 토큰 길이만 가진 trace 파일을 정렬해 원래 간격 또는 배속으로 재생할 수 있다. [GuideLLM README](https://github.com/vllm-project/guidellm), [GuideLLM benchmark guide](https://github.com/vllm-project/guidellm/blob/main/docs/getting-started/benchmark.md)

**가져올 가치.** 실제 프롬프트 원문을 저장하지 않고 `timestamp`, `input_length`, `output_length`, 선택적 session ID만으로 재생하는 **익명 trace profile**을 우선 적용한다. 또한 자동 탐색에서 오류율뿐 아니라 지연이 계속 악화되는 과포화 상태를 감지해 남은 고부하 단계를 건너뛰는 UX를 참고한다. [GuideLLM over-saturation constraint](https://github.com/vllm-project/guidellm/blob/main/docs/getting-started/benchmark.md#constraints), [GuideLLM trace replay](https://github.com/vllm-project/guidellm/blob/main/docs/getting-started/benchmark.md#trace-replay-benchmarking)

**폐쇄망 적합성.** 공식 컨테이너 또는 Python 3.10–3.13 환경으로 실행할 수 있고, dataset과 tokenizer 모두 로컬 경로를 사용할 수 있다. 반대로 Hugging Face source나 모델 ID를 사용하면 외부 다운로드가 발생할 수 있다. 폐쇄망에서는 버전 고정 컨테이너를 tar로 반입하고, 로컬 trace/dataset과 로컬 tokenizer만 허용해야 한다. Load Observatory 런타임에는 포함하지 않는다. [GuideLLM installation](https://github.com/vllm-project/guidellm#quick-start), [GuideLLM datasets](https://github.com/vllm-project/guidellm/blob/main/docs/guides/datasets.md)

**적용 우선순위: P0(trace schema와 UX), P1(과포화 중단).**

### 5. Grafana k6

**핵심 기능.** closed VU와 open arrival-rate executor를 구분하고, ramp·graceful stop·dropped iteration, checks, tags/groups, threshold 기반 pass/fail과 지연 후 abort를 제공한다. 단일 바이너리 또는 공식 컨테이너로 로컬 실행하고, Kubernetes에서는 k6 Operator의 `TestRun`으로 분산 실행할 수 있다. [k6 scenario concepts](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/), [k6 local/distributed execution](https://grafana.com/docs/k6/latest/get-started/running-k6/)

**가져올 가치.** 웹 부하 테스트에 상태 코드 외 `본문 포함`, `JSON 경로 값`, `응답 헤더`, `최대 응답 크기` 검증을 추가하고, 요청을 그룹/태그로 묶어 endpoint별 성공률과 P95를 보여 준다. threshold는 현재 전역 guardrail을 endpoint·scenario별 SLO로 확장하는 기준이 된다. [k6 checks](https://grafana.com/docs/k6/latest/using-k6/checks/), [k6 thresholds](https://grafana.com/docs/k6/latest/using-k6/thresholds/)

**폐쇄망 적합성.** k6는 로컬 바이너리와 공식 컨테이너로 완전한 로컬 실행이 가능하지만 기본적으로 익명 사용 보고를 외부 HTTPS endpoint로 보낸다. 폐쇄망 이미지에는 `K6_NO_USAGE_REPORT=true`를 고정하고, remote JavaScript import를 금지하며 모든 모듈을 로컬 파일로 번들해야 한다. extension은 실행 시 자동 다운로드하지 말고 외부망에서 미리 빌드한 버전 고정 바이너리를 반입해야 한다. [k6 usage collection](https://grafana.com/docs/k6/latest/set-up/usage-collection/), [k6 modules](https://grafana.com/docs/k6/latest/using-k6/modules/), [k6 installation](https://grafana.com/docs/k6/latest/set-up/install-k6/)

**적용 우선순위: P0(웹 assertion·그룹별 SLO), P2(선택형 외부 executor).**

### 6. Locust

**핵심 기능.** Python 코드로 사용자 행동, task weight, wait time과 custom load shape를 표현하며, web UI/headless 실행, CSV 기록, master-worker 분산 실행을 지원한다. [Locust documentation](https://docs.locust.io/en/stable/), [Locust configuration and load shapes](https://docs.locust.io/en/stable/configuration.html), [Locust distributed runs](https://docs.locust.io/en/stable/running-distributed.html)

**가져올 가치.** 현재 가중 사용자 journey에 `사용자군별 대기시간 분포`, 로그인 또는 준비 단계, 반복 task 흐름을 추가하는 모델을 참고한다. 운영자가 Python을 작성하게 하지 않고 UI에서 검증된 필드만 입력하게 한다.

**폐쇄망 적합성.** 공식 `locustio/locust` 이미지로 로컬 또는 master-worker 실행이 가능하다. 하지만 locustfile은 임의 Python 코드이고 서드파티 패키지를 설치할 수 있어, 다중 사용자가 공유하는 폐쇄망 Controller에서 실행하면 보안 검토와 이미지 재빌드가 필요하다. 따라서 내장 executor로 넣지 않고, 필요할 때 별도 승인된 이미지와 고정 locustfile을 사용하는 편이 안전하다. [Locust Docker](https://docs.locust.io/en/latest/running-in-docker.html), [Locust event and custom arguments](https://docs.locust.io/en/stable/extending-locust.html)

**적용 우선순위: P1(journey 모델), P3(외부 executor).**

### 7. Vegeta

**핵심 기능.** Go 기반 HTTP 도구·라이브러리로 constant request rate, coordinated omission 방지, raw 결과 저장, 여러 분산 결과 파일을 함께 읽어 timestamp 순으로 합친 percentile 보고를 제공한다. 정적 바이너리로도 배포할 수 있다. [Vegeta repository](https://github.com/tsenart/vegeta)

**가져올 가치.** Load Observatory가 이미 원시 표본을 Controller에서 합쳐 percentile을 한 번 계산하므로 추가 구현은 필요 없다. 다만 분산 결과의 증거 보존과 사후 재계산을 위해 선택적으로 per-request raw sample export를 제공할 때 형식을 참고할 수 있다.

**폐쇄망 적합성.** 정적 amd64 바이너리 또는 현재 Go 코드에 라이브러리로 포함할 수 있어 단순하지만, 지금의 Go runner와 기능이 크게 겹친다. 새 의존성을 넣지 않는다.

**적용 우선순위: P3.**

## 적용 계획

| 우선순위 | 기능 | 제품 적용 방식 | 완료 조건 |
| --- | --- | --- | --- |
| P0 · 완료 | 익명 트래픽 trace replay | 로컬 JSON/JSONL 업로드: `timestamp`, `input_length`, `output_length`, `scenario`; 원문 `prompt`는 선택 입력이고 민감하면 길이만 반입 | 외부 연결 없이 trace의 도착 순서·간격·길이가 재현되고, 원본/배속 실행과 조건 비교 가능 |
| P0 | 반복 실행과 변동성 | 같은 run config를 3회 이상 실행하는 campaign과 중앙값·최솟값·최댓값·변동계수 표시 | 단일 실행 결과와 반복 결과를 구분하고, 편차가 큰 지표에 경고 표시 |
| P0 | 웹 응답 assertion | 상태 코드, 본문 문자열, JSON 경로, 헤더, 응답 크기 조건을 scenario별 저장 | HTTP 200이어도 내용이 틀리면 assertion failure로 분리 집계 |
| P0 | scenario별 SLO | endpoint/journey별 오류율·P95 threshold와 전체 run 중단 조건 | 어떤 endpoint가 실패 판정을 만들었는지 결과에 명시 |
| P1 | 짧은 probe 동시 실행 | 코딩·장문 workload 중 저비용 probe를 별도 rate로 보내 TTFT/E2E 분리 | 주 workload가 일반 질의 품질에 준 영향을 같은 시간축에서 표시 |
| P1 | 자동 탐색 과포화 중단 | 처리량 증가 정체와 TTFT/queue 지속 증가가 연속 단계에서 확인되면 남은 단계 생략 | 중단 원인과 마지막 정상 단계, 첫 과포화 단계를 보존 |
| P1 | 사용자 journey 강화 | 사용자군별 준비 단계, task 반복 횟수, 대기시간 범위 추가 | 단순 질의와 에이전트 코딩 사용자를 한 실행에서 분리 보고 |
| P2 | 공식 도구 교차 검증 Job | AIPerf 또는 GuideLLM 버전 고정 이미지를 수동 K8s Job으로 제공 | 제품 기본 배포와 분리되고 결과 import만 허용 |

## 폐쇄망 반입 규칙

외부 도구를 선택형 검증 Job으로 추가할 때는 다음을 배포 조건으로 둔다.

- 이미지 태그가 아니라 digest를 고정하고 `linux/amd64`로 pull/build한 뒤 `podman save` 아카이브와 SHA-256 checksum을 함께 반입한다.
- Python wheel, tokenizer, dataset, JavaScript module을 모두 사전 반입하고 실행 중 `pip`, Hugging Face Hub, GitHub, CDN 접속을 금지한다.
- k6는 `K6_NO_USAGE_REPORT=true`, remote import 금지, 확장 포함 바이너리 사전 빌드를 적용한다. [k6 usage collection](https://grafana.com/docs/k6/latest/set-up/usage-collection/), [k6 modules](https://grafana.com/docs/k6/latest/using-k6/modules/)
- AIPerf·GuideLLM은 공개 dataset ID 대신 로컬 JSONL/CSV와 로컬 tokenizer 디렉터리만 받는다. W&B·MLflow·클라우드 exporter는 구성에서 제외한다. [AIPerf features](https://github.com/ai-dynamo/aiperf), [GuideLLM datasets](https://github.com/vllm-project/guidellm/blob/main/docs/guides/datasets.md)
- Job의 egress는 모델 endpoint, Controller callback, 내부 Prometheus/DCGM만 허용한다. API key는 파일이나 command line이 아니라 Kubernetes Secret으로 주입하고 결과 export에서 제거한다.
- 외부 executor 이미지는 기본 Load Observatory release tar에 넣지 않는다. 검증이 필요한 운영자만 별도 아카이브로 반입해 공격 표면과 용량을 분리한다.

## 도입하지 않을 것

- **GenAI-Perf 신규 통합**: 공식적으로 AIPerf로 전환 중이다.
- **임의 k6 JavaScript/Locust Python 업로드와 실행**: 폐쇄망이라고 해도 Controller에서 임의 코드를 실행하는 기능은 원격 코드 실행 표면이 된다.
- **런타임 Hub 다운로드**: tokenizer와 dataset을 자동으로 받으면 재현성과 폐쇄망 보장이 동시에 깨진다.
- **외부 도구의 결과 숫자를 그대로 병합**: 지표 정의와 warm-up 범위가 다르므로, 외부 도구는 같은 endpoint를 독립 검증하는 용도로만 사용하고 Load Observatory 내부 run과 한 percentile로 합치지 않는다.

이 원칙대로라면 제품 기본 실행 경로는 계속 Go·React·PostgreSQL·Prometheus/DCGM만으로 동작하고, 공개 도구에서 검증된 trace replay·반복 신뢰도·웹 assertion만 데이터 모델과 UI에 흡수할 수 있다.
