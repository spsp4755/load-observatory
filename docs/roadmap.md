# Load Observatory 고도화 계획

조사일: 2026-08-04. 대상 환경: **폐쇄망 Kubernetes**, 사내 LLM 모델 API(OpenAI 호환 `/v1/chat/completions`) 용량 측정.

이 문서는 공개 도구와의 비교를 통해 **현재 측정값이 틀리는 지점**을 먼저 확정하고, 그 다음 단계를 정한다. 기능 추가보다 "지금 보고하는 숫자가 사실인가"가 우선이다.

## 요약: 지금 가장 중요한 사실

현재 구현은 부하 발생·수집·판정 골격은 갖췄지만, **다음 5개 때문에 일부 숫자가 실제보다 낙관적으로 나온다.** 기능을 더 얹기 전에 이것부터 고쳐야 한다.

| # | 문제 | 결과 | 위치 |
| --- | --- | --- | --- |
| 1 | 캐시 우회 nonce를 프롬프트 **끝**에 붙임 | vLLM prefix caching이 앞부분을 그대로 히트 → `bypass`가 우회하지 못하고 **TTFT가 크게 낙관적** | `internal/agent/runner.go:336` |
| 2 | `ignore_eos` 미지정 | `max_tokens`가 상한일 뿐 실제 출력 길이가 매번 다름 → **TPOT/ITL을 실행 간 비교 불가**, 소요 시간 추정도 상한값 | `runner.go:338` |
| 3 | 샤드 percentile을 `max`로 병합 | 전체 P95가 아니라 "가장 나쁜 샤드의 P95". 샤드 트래픽이 불균등하면 **어느 방향으로든 틀림** | `internal/store/store.go` `mergeDistribution` |
| 4 | 대화 이력 미누적 (`messages` 항상 1건) | 턴마다 커지는 컨텍스트 = 실제 KV 캐시 압력의 주 원인이 **완전히 미모델링** | `runner.go:338` |
| 5 | 모니터링이 실행당 2회 샘플, vLLM `/metrics` 미수집 | 클라이언트 지연을 서버 큐/KV 캐시와 **대조 불가** → 병목 귀속 불가 | `internal/controller/server.go:95,294` |

### 1번이 특히 중요한 이유

vLLM은 **v0.6.0부터 automatic prefix caching(APC)이 기본 활성**이다. 블록 단위(기본 16토큰)로 앞쪽 프리픽스를 해싱해 재사용하므로:

- `reuse` 모드: 두 번째 요청부터 prefix 히트율 ≈ 100%. prefill이 사실상 사라져 **decode 성능과 스케줄러만 측정**하게 된다. LLM 부하 테스트가 거짓을 말하는 가장 흔한 경로다.
- `bypass` 모드(현재): nonce가 **끝**에 있으므로 앞쪽 전체 블록이 여전히 캐시 히트. 진짜 cold prefill이 아니다. → **nonce를 맨 앞으로 옮겨야 한다.**
- 모든 결과에 **관측된 prefix 히트율을 함께 기록**하고, 히트율이 다른 실행끼리는 비교를 거부해야 한다.

## 공개 도구 비교

### LLM 전용 도구

| 도구 | 강점 | 폐쇄망 | 우리가 가져올 것 |
| --- | --- | --- | --- |
| [vLLM `bench serve`](https://docs.vllm.ai/en/latest/benchmarking/cli/) | 사실상의 기준. `--request-rate` + `--burstiness`(Gamma, 1.0=Poisson) + `--max-concurrency` 분리, `--ramp-up-strategy linear\|exponential`, `--goodput ttft:100 tpot:50`, `--ignore-eos`, `bench sweep`+`dashboard`로 latency-throughput Pareto | **가능** (`random`/`sonnet`/local jsonl + 로컬 tokenizer 경로) | 동시성 sweep 의미론, `ignore_eos`, goodput 정의, arrival burstiness |
| [AIPerf](https://github.com/ai-dynamo/aiperf) (GenAI-Perf 후속, GenAI-Perf는 feature-frozen) | 지표 정의가 가장 엄격. `time_to_second_token`, `output_token_throughput_per_user = 1/ITL`, **`good_request_fraction = good/(ok+errors)`**, adaptive scaling(SLA 경계 탐색), DCGM 텔레메트리 통합 | 대체로 가능 (synthetic + `--input-file` + 로컬 tokenizer) | **goodput 분모에 오류를 포함**, TTST, per-user vs aggregate 구분 |
| [GuideLLM](https://github.com/vllm-project/guidellm) (vllm-project) | `sweep` 프로파일(sync→throughput→구간 보간), **`over_saturation` 감지로 자동 중단**, warmup/cooldown을 **비율**로 지정, 자체 포함 HTML 리포트 | **최상**. 코퍼스가 패키지에 내장, [Red Hat 폐쇄망 레시피](https://developers.redhat.com/articles/2025/09/15/benchmarking-guidellm-air-gapped-openshift-clusters) 존재 | over-saturation 조기 중단, warmup 비율 지정, sweep 구조 |
| [kubernetes-sigs/inference-perf](https://github.com/kubernetes-sigs/inference-perf) | 용량 계획 지향. 다단 stage sweep, 자동 포화 감지, goodput vs SLO, **Prometheus 메트릭 노출**, QPS-latency 차트 자동 생성 | 오프라인 지원 주장 | 참조 설계로 가장 가까움 |
| [HF inference-benchmarker](https://github.com/huggingface/inference-benchmarker) | 프로파일 개념(`chat`/`code-generation`/`classification`/`fixed-length`), ISL/OSL 정규분포 샘플링 | Hub 전제 (사전 스테이징 필요) | 프로파일별 프리픽스 캐시 특성 구분 |
| [LLMPerf](https://github.com/ray-project/llmperf) | — | 2025-12-17 **archived** | 도입 안 함 |

### 범용 도구 (측정 의미론만 차용)

| 도구 | 배울 점 |
| --- | --- |
| [k6](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/) | executor 6종으로 open/closed를 명시 분리. **`dropped_iterations`** 카운터 — 발생기 포화와 대상 포화를 구분하는 근거. thresholds에 `abortOnFail`+`delayAbortEval`. 단 percentile은 **샤드 병합을 안 하기 때문에** 정확한 것이며, k6 OSS도 전역 percentile 병합은 못 한다 |
| [Vegeta](https://github.com/tsenart/vegeta) | 우리가 복사할 아키텍처: **raw 결과를 파일로 남기고 `vegeta report r1.bin r2.bin`으로 병합 후 percentile을 한 번만 계산** |
| [Gatling](https://docs.gatling.io/concepts/injection/) | open/closed 혼용 금지 규칙, HdrHistogram 채택, OSS는 `simulation.log` 병합 후 리포트 생성 |
| [Fortio](https://github.com/fortio/fortio) | `-nocatchup`/`-uniform`/`-jitter` — 느린 대상에 대한 처리를 **명시적 옵션으로** 노출. `Histogram.Transfer`/`Merge` 제공 |
| [Locust](https://docs.locust.io/en/stable/custom-load-shape.html) | percentile은 거친 고정 bin이지만, **worker 히스토그램을 counter-wise 합산 후 percentile 1회 계산** — 통계적으로 옳은 패턴 |

### Load Observatory의 위치

위 도구 중 **웹 UI + 다중 사용자 + 결과 영속화 + 대상 등록·인증키 관리 + 한국어 운영 판정**을 갖춘 것은 없다. 전부 CLI다. 즉 경쟁 지점은 측정 프리미티브가 아니라 **"사내 운영자가 근거를 가지고 용량을 결정하게 만드는 제품"**이다. 계획의 방향은 이렇다:

> 측정 의미론은 vLLM `bench serve` / AIPerf / GuideLLM에 맞춰 **신뢰성을 확보**하고, 제품 우위(웹·영속화·판정·비교)는 유지·강화한다.

이미 앞서 있는 부분: **TTFO**(reasoning 모델에서 reasoning token만 먼저 나오는 구간 분리)는 공개 도구 중 거의 없다. 사용자군 journey 혼합, 완료율 게이트, 종료 유예도 유지 가치가 있다.

## 단계별 계획

### Phase 0 — 지금 틀린 숫자 고치기 ✅ 완료

아래 4개는 구현·검증을 마쳤다. 느린 스트리밍 모델 + 2 샤드 실측으로 확인: 프롬프트 앞 8자 공유 **0/112**, `ignore_eos` **112/112**, `latency_scope=pooled_samples`, 원시 표본 API·스냅샷 미노출.

구현 중 추가로 발견해 함께 고친 것:

- **nonce의 엔트로피 위치.** 앞에 붙이는 것만으로는 부족했다. `[Load Observatory variation run=... request=N]`은 앞 ~14토큰이 상수라서 **첫 16토큰 블록이 여전히 캐시 히트**할 수 있었다. 이제 `(workloadID, sequence)`의 FNV 해시를 맨 앞에 두어(`[LO-44ba1c3e run-5-shard-3#77]`) 토큰 2번째부터 달라진다.
- **샤드 간 nonce 충돌.** 두 샤드가 각자 1부터 카운트하면서 `WorkloadID`가 동일한 run ID였기 때문에 **서로 똑같은 프롬프트를 만들어 상대 샤드의 캐시를 히트**했다. 실측에서 112건 중 14건이 이 경로였다. `ClaimRun`에서 `WorkloadID`에 샤드 ID를 붙여 해결.
- **`ignore_eos`는 기본 OFF로 결정.** 폐쇄망에서는 사내 모델 서버의 종류·버전을 통제할 수 없고, 모르는 필드를 거부하는 서버라면 **모든 요청이 400으로 실패**한다. 대신 「연결 확인」이 `supports_ignore_eos`를 탐침해 알려주고, 끈 상태의 결과에는 "TPOT·ITL 비교 불가" 경고를 남긴다.
- **percentile 병합은 원시 표본 방식(Option A) 채택.** 새 의존성 없음. ITL은 토큰당 1개라 표본이 커질 수 있어, 상한 도달 시 앞부분을 자르지 않고 **균일하게 decimate**한다(`sampleSet`).

<details>
<summary>원래 계획 (참고)</summary>

1. **prefix cache nonce를 프롬프트 맨 앞으로.** `bypass`가 실제로 cold prefill이 되게 한다. `reuse`/`mixed`/`bypass`의 의미를 문서와 UI 툴팁에 명시.
2. **`ignore_eos` 옵션 추가** (기본 on, 끌 수 있게). `max_tokens`와 함께 출력 길이를 고정해 TPOT/ITL이 실행 간 비교 가능해진다. 끄면 결과에 "출력 길이 미고정" 배지를 남긴다.
3. **percentile 병합 교체.** `mergeDistribution`의 `max(p95)`를 버린다. 두 선택지:
   - **Option A (권장, 의존성 0):** 샤드가 raw 지연 샘플을 그대로 올리고 controller가 한 번만 정렬·계산. LLM 부하는 본질적으로 저 RPS(요청당 수 초)이므로 10 RPS×10분 = 47 KB/샤드, 50 RPS×10분 = 234 KB/샤드. **새 라이브러리가 필요 없다.**
   - **Option B (상한 보장 필요 시):** `github.com/HdrHistogram/hdrhistogram-go`, `New(1, 600_000_000, 3)`(µs, 1µs–600s, 0.1% 정밀도). `Encode(2)`가 보통 1.5 KB 미만이고 bin 경계가 동일해 병합 결과가 단일 프로세스 결과와 **비트 단위로 동일**. cgo 없음 → `go mod vendor`로 폐쇄망 반입 가능.
   - Option A로 시작하고, 고 RPS 요구가 실제로 생기면 B로 옮긴다.
4. **`usage` 부재 시 경고.** 이미 `include_usage: true`를 보내는 것은 **폐쇄망에서 올바른 선택**(tokenizer 불필요, 서버 권위). 다만 `usage`가 안 오면 현재는 조용히 `completion=0`이 되어 tok/s가 0이 된다. 하드 경고를 띄우고, 청크 수 기반 추정으로 대체할 때는 **"토큰"이 아니라 "청크"로 명명**한다. (서버가 청크 하나에 여러 토큰을 담으므로 청크 수 ≠ 토큰 수.)

</details>

**Phase 0 후속 과제 완료**: decimate된 실행은 「지연 분석」에 percentile이 추정값임을 표시한다.

### Phase 1 — 판정을 신뢰할 수 있게 ✅ 완료

가짜 Prometheus(vLLM 지표 형태)로 4개 시나리오를 돌려 판정이 각각 다르게 나오는 것을 확인했다:

| 시나리오 | 포화 판정 | 신뢰성 | 병목 귀속 |
| --- | --- | --- | --- |
| 정상 (KV 91%, 대기 0, preemption 0) | `headroom` | true | — |
| 포화 (대기 11, preemption 2.4/초) | `saturated` | true | — |
| 스로틀링 (thermal violation, 클럭 1900→1200) | `headroom` | **false** — 온도 스로틀링 | — |
| 클라이언트 병목 (서버 queue 10ms + prefill 20ms) | `headroom` | true | `client_or_network_bound` |

구현 중 추가로 발견해 함께 넣은 것:

- **`metrics_not_this_run` 상태.** 서버가 보고한 `queue + prefill`이 클라이언트 TTFT보다 크면, 그 지표는 우리 요청만의 것이 아니다(같은 서버의 다른 트래픽, 또는 다른 모델·인스턴스). 원래는 이 경우도 `server_bound`로 판정하면서 "181ms = 2400ms + 300ms" 같은 말이 안 되는 분해를 출력했다. 이제 별도 상태로 분리해 Prometheus 쿼리를 좁히라고 안내한다.
- **absent ≠ 0.** `MonitoringSample`을 고정 필드에서 map으로 바꿨다. DCGM 프로파일링 필드는 드라이버·컨테이너에 따라 조용히 수집 실패하는데, 이를 0으로 보고하면 "GPU 유휴"로 읽힌다. 수집 실패는 `partial` 상태와 메시지로 표시한다.
- **absent 지표 재탐침 backoff.** 매초 샘플링에서 없는 이름을 매번 다시 물으면 초당 수십 쿼리가 된다. 1분에 한 번만 재시도한다.
- **리스트 응답에서 초당 시계열 제거.** 판정(요약)은 유지하고 원시 샘플만 뺀다. 대신 기록에서 실행을 선택하면 상세용으로 다시 가져온다.
- **`max_num_seqs`를 운영자 입력으로.** 이 값이 있으면 "하드웨어 한계"와 "설정 한계"를 구분한다. 없으면 동시 실행이 멈춘 지점을 근거로 설정 한계 가능성을 알린다.

<details>
<summary>원래 계획 (참고)</summary>

지금은 클라이언트 지연만 본다. 그래서 "모델이 느리다"와 "우리 클라이언트/LB가 느리다"를 구분할 수 없다.

5. **vLLM `/metrics`를 초당 수집**하고 실행 타임라인에 붙인다. 수집 항목:
   - 포화 3종: `vllm:num_requests_waiting`, `vllm:kv_cache_usage_perc`, `rate(vllm:num_preemptions_total)`
   - 단계 분해 히스토그램: `vllm:request_queue_time_seconds`, `vllm:request_prefill_time_seconds`, `vllm:request_decode_time_seconds`
   - prefix 캐시: `vllm:prefix_cache_queries_total`, `vllm:prefix_cache_hits_total` (**토큰 단위**, 히트율은 직접 계산)
   - 배치 구성: `vllm:iteration_tokens_total`, 동시성 상한 확인용 `vllm:num_requests_running`
   - **호환성**: 카운터는 노출 시 `_total`이 붙는다. `vllm:kv_cache_usage_perc`는 구버전 `vllm:gpu_cache_usage_perc`였으므로 **두 이름을 모두 탐침**한다. `num_requests_swapped`/`cpu_cache_usage_perc`는 V1에서 제거됐다.
   - 사내 모델이 vLLM이 아닐 수 있으므로 [SGLang](https://docs.sglang.io/references/production_metrics.html)(`sglang:num_queue_reqs`, `sglang:token_usage`, `sglang:cache_hit_rate`)과 TGI(`tgi_queue_size`, `tgi_batch_current_size`, `tgi_request_queue_duration`)도 탐침. SGLang은 최근 버전에서 구분자가 `sglang:`→`sglang_`로 바뀌었으므로 **둘 다 시도**한다.
6. **TTFT 분해 검증 (가장 가치 높은 단일 기능).** `클라이언트 TTFT ≈ queue_time + prefill_time (+ 네트워크)`인지 확인한다. 클라이언트 TTFT가 훨씬 크면 병목은 **모델 서버가 아니라** 우리 발생기·LB·연결 수립이다. 대부분의 부하 테스터가 빠뜨리는 검증이고, 이것이 판정의 신뢰도를 만든다.
7. **포화 판정 규칙화.** `kv_cache_usage_perc`만 높은 것은 **정상**이다 (vLLM은 배치 극대화를 위해 KV를 의도적으로 채운다). 포화는 **대기가 지속되고 preemption이 0이 아닐 때만** 선언한다. `num_requests_running`이 `max_num_seqs`에 붙어 있으면 **하드웨어가 아니라 설정 한계** → GPU 증설이 아니라 설정 변경이 답이다.
8. **실행 무효화 조건.** 다음이면 숫자를 보고하지 말고 "신뢰 불가"로 표시: `DCGM_FI_DEV_XID_ERRORS ≠ 0`, `DCGM_FI_DEV_{POWER,THERMAL}_VIOLATION` 증가, `DCGM_FI_DEV_SM_CLOCK` 하강(스로틀링 = 진짜 용량 한계로 오인됨), `vllm:corrupted_requests_total ≠ 0`, prefix 히트율이 설정한 캐시 정책과 불일치.
9. **DCGM 지표 교정.** 현재 `DCGM_FI_DEV_GPU_UTIL`만 본다. 이 값은 "커널이 하나라도 돌았는지"라서 **batch size 1에서도 90~100%**가 된다 → LLM 용량을 한 자릿수 배로 과소 산정하는 전형적 오류다. 추가할 것:
   - `DCGM_FI_PROF_DRAM_ACTIVE` — decode는 메모리 대역폭 바운드. **이게 실제로 포화되는 값**
   - `DCGM_FI_PROF_PIPE_TENSOR_ACTIVE` — prefill(GEMM) 우세 여부
   - `DCGM_FI_PROF_SM_OCCUPANCY`, `DCGM_FI_PROF_SM_ACTIVE`
   - 스로틀: `DCGM_FI_DEV_SM_CLOCK`, `DCGM_FI_DEV_CLOCK_THROTTLE_REASONS`, `DCGM_FI_DEV_{POWER,THERMAL}_VIOLATION` — **`default-counters.csv`에 없으므로 exporter ConfigMap에 직접 추가해야 한다**
   - 건강한 LLM 서버의 지문: **DRAM_ACTIVE 높음 + PIPE_TENSOR_ACTIVE 낮음 + GPU_UTIL ~100%** = 메모리 바운드이고 정상. 하드웨어 증설이 아니라 배치·양자화·KV 설정이 답.
   - `DCGM_FI_PROF_*`는 DCP 모듈이 필요하고 일부 환경에서 조용히 실패한다. 0으로 보고하지 말고 **수집 실패로 표시**한다. 스크레이프 간격은 5초 이상.
   - `DCGM_FI_DEV_FB_USED`는 vLLM이 `gpu_memory_utilization`만큼 선점하므로 거의 상수다. **KV 압력 지표로 쓰면 안 된다.**
10. **실행 provenance 기록.** 이게 없으면 실행 간 비교가 불가능하고 보고서는 일화(anecdote)가 된다: vLLM 버전, `max_num_seqs`, `max_num_batched_tokens`, `gpu_memory_utilization`, `block_size`, TP/PP 차수, APC on/off, chunked prefill on/off, 관측된 prefix 히트율, `ignore_eos`, ISL/OSL 분포. 일부는 `vllm:cache_config_info`에서 얻고, 나머지는 실행 설정에 입력 필드로 둔다.

</details>

**항목 10(provenance)은 아래 별도 절에서 완료했다.**

### Phase 2 — 용량 판정 방법론 ✅ 완료 (11·12·13)

동시성 8에서 지연이 꺾이도록 만든 가짜 모델(`kneeAt = 8`)에 sweep을 돌려, **정확히 8 VU를 한계로 찾아냈다.** 측정된 곡선:

| 부하 | 완료 RPS | 출력 tok/s | TTFT P95 | 판정 |
| --- | --- | --- | --- | --- |
| 2 VU | 13.2 | 40 | 60 ms | 충족 |
| 4 VU | 27.2 | 82 | 60 ms | 충족 |
| **8 VU** | **54.4** | **163** | **60 ms** | **충족 ← 한계** |
| 16 VU | 8.8 | 26 | 1020 ms | 미충족 (완료율 74.6%) |

이 곡선이 왜 중요한지 그대로 보여 준다. 2→8 VU에서 **처리량은 4배가 되는데 TTFT는 60ms에서 그대로다.** 그리고 16 VU에서 TTFT가 17배로 튀면서 처리량은 오히려 붕괴한다. 2 VU만 측정하고 "13 RPS가 한계"라고 보고하면 실제 수용량의 1/4을 보고하는 것이다.

구현 중 추가로 발견해 고친 것:

- **시작 부하가 실패하면 즉시 중단.** 원래 "무릎 다음 한 단계까지는 측정한다"는 규칙을 넣었는데, Phase 0에서 만든 테스트가 이걸 잡아냈다. 아직 통과한 단계가 없으면 **무릎이 없으므로 다음 단계를 측정할 이유도 없다** — 더 높은 부하는 확실히 더 나쁘다.
- **낮은 단계가 실패한 뒤 높은 단계가 통과하면 무릎으로 세지 않는다.** 용량은 아래에서부터 연속으로 유지돼야 한다. 아니면 그 통과는 운이다.
- **Goodput 분모 교정** (항목 12): `good / (성공 + 오류)`. 기존에는 성공만 분모여서, **부하가 걸리면 어려운 요청을 흘려버리는 서버가 오히려 높은 점수**를 받았다. 취소는 분모에서 제외한다 — 그건 완료율 게이트가 따로 본다.
- **멀티턴 컨텍스트 누적** (항목 13): 각 턴의 실제 답변 텍스트를 다음 요청에 함께 보낸다. 시나리오별 `input_tokens`로 컨텍스트 증가가 결과에 보인다. 누적 이력은 상한을 두고 오래된 턴부터 버린다. 기본은 꺼짐 — 켜면 측정 대상이 달라진다.

**항목 14(chunked prefill 인지)는 provenance 작업에서 해결했다.** 아래 참조.

### Phase 2 — 원래 계획

11. **동시성 sweep + knee 탐지로 자동 탐색 교체.** 현재는 단일 지표 이분 탐색이다. 올바른 방법:
    - **동시성**(closed)을 1,2,4,8,16,32,… 로 sweep해 곡선을 얻는다. open-loop rate sweep은 용량 초과 시 발산해 읽을 수 있는 곡선이 안 나온다.
    - ISL/OSL 분포를 고정하고 `ignore_eos`를 켠다.
    - x축 output tok/s, y축 p95 TTFT / p95 TPOT로 그린다. 곡선은 평평하다가 수직으로 꺾인다(hockey stick).
    - knee는 **눈대중이 아니라 SLO로 정의**한다: `p95 TTFT ≤ X` 및 `p95 TPOT ≤ Y`를 만족하는 최대 지속 처리량. 이것이 방어 가능한 용량 숫자다.
    - 운영 제공은 knee의 약 **70% 수준**을 권고(헤드룸).
    - **왜 이게 필요한가**: LLM은 continuous batching 때문에 중간 부하에서 *처리량은 늘고 TPOT는 거의 안 나빠진다*. 일반 웹 서버와 반대다. 그래서 단일 동시성 측정은 용량을 크게 과소평가하고, "RPS" 단독은 출력 길이 분포 없이는 무의미하다.
    - TTFT는 **admission control 지표**로 절벽형(큐 진입 순간 급증), TPOT/ITL은 **공유 자원 경합 지표**로 완만한 선형. 두 곡선을 분리해 보여준다.
12. **goodput 분모 교정.** AIPerf 정의를 따른다: `good / (completed + errors)`. 오류를 분모에 넣어야 **부하를 흘려버리는(429/timeout) 서버가 좋아 보이지 않는다.** 현재는 성공 요청만 분모다.
13. **멀티턴 대화 누적.** `messages`에 이전 턴을 쌓아 컨텍스트가 커지게 한다. 턴 수·후속 요청 비율·턴 간 delay를 설정값으로. 실제 챗/에이전트에서 TTFT 증가를 지배하는 요인이며, prefix 캐시 효과도 이때 비로소 현실적으로 나타난다.
14. **chunked prefill 인지.** V1에서 기본 활성이다. TTFT가 프롬프트 길이만의 함수가 아니라 `max_num_batched_tokens` 예산의 함수가 된다. **이 값을 기록하지 않은 TTFT는 의미가 없다.** 반대로 ITL은 매우 안정해지므로 p99 ITL은 포화 신호로서 약해진다 → p95 TTFT를 주 신호로 쓴다.

### Phase 3 — 현실성과 운영

15. **ISL/OSL 분포화.** 고정 프롬프트 대신 평균+표준편차+min/max. vLLM 스케줄러 거동은 입력 길이 분포에 크게 좌우된다.
16. **Open-loop 도착 + burstiness.** Poisson(및 Gamma 형상 파라미터)을 추가한다. 단 **의도된 도착 시각(intended send time)부터 지연을 측정**해야 한다. 그러지 않으면 우리 큐 안에서 coordinated omission을 재발명하게 된다 — 5초 스톨이 10 rps에서 5초 샘플 1건만 남기고, 실제로는 5.0/4.9/4.8…초인 50건이 사라져 p99가 자릿수 단위로 과소 보고된다. 발생기 폭주 방지용 drop 안전판 + `dropped_arrivals` 카운터는 유지한다 (k6 패턴).
17. **JSON/CSV 내보내기 + 실행 provenance 포함.** 사내 보고서 첨부용.
18. **사내 트래픽 트레이스 리플레이.** 합성 프롬프트 형태를 두고 논쟁하는 대신 실제 캡처 트래픽을 재생한다. vLLM `timed_trace`, GuideLLM `replay`, AIPerf JSONL이 모두 지원하는 방식이다. **사내 도구로서 신뢰도를 가장 크게 올릴 수 있는 기능**이다.

## 도입하지 않을 것 (폐쇄망 규율)

- **tokenizer 반입/로컬 토크나이즈.** 모델별 `tokenizer.json`을 배포·동기화해야 하고, 서빙팀이 올린 모델과 어긋나는 **영구적 드리프트 버그**가 된다. `include_usage: true`로 서버 권위 카운트를 쓰는 현재 방식이 옳다. 정확한 프롬프트 토큰 길이를 맞춰야 할 때만, GuideLLM 폐쇄망 레시피처럼 **운영자가 지정한 로컬 경로**에서 읽고 Hub repo id는 절대 해석하지 않는다.
- **k6/Locust/GuideLLM을 런타임으로 포함.** 이미지·버전·결과 형식이 갈라지고 보안 검토 범위가 커진다. 의미론만 이식한다.
- **HF Hub 데이터셋.** 전부 인터넷 의존. 합성 생성 + 사내 트레이스로 대체한다.
- **t-digest.** 병합이 순서 의존적이라 실행마다 p99가 미세하게 달라진다. 재현성이 필요한 사내 보고서에 부적합. Option A(raw) 또는 HdrHistogram을 쓴다.

## 검증되지 않은 항목

- `vllm:gpu_cache_usage_perc`가 정확히 어느 릴리스에서 제거됐는지 — 두 이름 탐침으로 대응한다.
- SGLang 구분자 변경(`sglang:`→`sglang_`)의 정확한 릴리스 — 두 형태 모두 시도한다.
- `vllm:num_requests_waiting_by_reason`의 전체 label 집합 (`capacity`만 확인) — 존재 여부부터 탐침한다.
- GuideLLM `benchmarks.json`의 정확한 키 이름 (버전 간 CLI/스키마 변동 큼) — 임포터를 만들 경우 고정한 버전의 실제 출력으로 확인한다.
- inference-perf가 어떤 데이터셋을 실제로 로컬에 포함하는지 (README 주장, 소스 미확인).

## Provenance (Phase 1 항목 10 · Phase 2 항목 14) ✅ 완료

두 Phase에서 연속으로 남겼던 항목. **`max_num_batched_tokens`를 모르면 TTFT는 실행 간 비교 근거가 없다** — chunked prefill이 기본 활성이라 TTFT는 프롬프트 길이가 아니라 이 예산의 함수이기 때문이다.

가짜 Prometheus가 `vllm:cache_config_info` 라벨을 내보내게 하고 실측 확인:

```
서버에서 탐지 : {"model":"qwen/qwen3-35b","gpu_memory_utilization":0.9,"block_size":16,
                "prefix_caching":"on","chunked_prefill":"on"}
실효 설정     : 위 + 운영자 입력 {max_num_seqs:16, tensor_parallel_size:2}
TTFT 비교가능 : false
미확인 항목   : vLLM 버전, max_num_batched_tokens
```

- **서버 라벨에서 자동 수집.** `vllm:cache_config_info`와 큐 지표의 `model_name` 라벨을 읽는다. 라벨 이름은 버전마다 다르므로, 파싱 불가·부재는 **추측하지 않고 「미확인」으로 남긴다**. Python식 `"True"/"False"`는 `on`/`off`로 정규화한다.
- **서버 보고값이 운영자 입력을 이긴다.** 서버는 자기 설정의 권위다. 다만 두 값이 **다르면 충돌로 표시**한다 — 운영자가 틀린 `max_num_seqs`로 추론하면 "하드웨어 한계 vs 설정 한계" 판정 자체가 틀린다.
- **`ttft_comparable` 플래그.** TTFT의 의미를 바꾸는 설정(`max_num_batched_tokens`, prefix caching, chunked prefill) 중 하나라도 미확인이면 false. 화면에 그 이유를 그대로 띄운다.
- **실행 비교 게이팅.** 서버 설정이나 워크로드 조건(캐시 정책, 출력 길이 고정, 이력 누적, 최대 토큰)이 다른 두 실행을 비교하면, **차이를 부하 차이로 해석하지 말라고 경고**하고 무엇이 달랐는지 나열한다. 예: `캐시 정책: bypass ↔ reuse · 출력 길이 고정: false ↔ true · max_num_batched_tokens: 미확인 ↔ 8192`.

## 결과 내보내기 (Phase 3 항목 17) ✅ 완료

`GET /api/runs/{id}/export.json`과 `export.csv`. 보고서 첨부용이므로 **숫자만이 아니라 판정과 provenance를 함께 담는다** — 그것이 없으면 읽는 사람이 숫자의 유효성을 판단할 수 없다.

CSV는 `section,key,subkey,value`의 long format이다. 실행에는 스칼라·분포·초당 시계열·시나리오별 행이 섞여 있어 하나의 wide 헤더로 만들면 데이터를 잃거나 빈 열을 대량 만들어야 한다. 섹션: `run`, `provenance`, `lifecycle`, `throughput`, `steady_state`, `distribution`, `verdict`, `scenario`, `timeline`, `server_metrics`.

## 도착 패턴과 길이 분포 (Phase 3 항목 15·16) ✅ 완료

- **Poisson 도착.** RPS 모드에 `arrival_pattern: uniform | poisson`을 추가했다. 실제 호출자는 metronome처럼 오지 않는다. 실측: 12 RPS·in-flight 6·서비스 150ms에서 **균일 간격이면 드롭이 없는 조건인데 Poisson에서 3건 드롭**됐다 — 이 burstiness가 큐를, 그리고 TTFT 폭발을 드러내는 부분이다. 간격은 카운터 해시에서 유도해 **같은 설정이면 같은 도착 패턴**이 재현된다(실행 간 비교 가능성 유지).
- **의도된 도착 시각부터 지연 측정 (coordinated omission 방지).** RPS 모드에서 지연을 «보내진 시점»이 아니라 «보내져야 했던 시점»부터 잰다. 그러지 않으면 5초 스톨이 10 rps에서 5초 샘플 1건만 남기고, 실제로는 5.0/4.9/4.8…초인 50건이 사라져 **p99가 자릿수 단위로 과소 보고**된다. steady-window 판정도 예정 시각 기준이라, 램프업에 예정된 요청이 늦게 나가도 측정 구간에 섞이지 않는다.
- **`generator_delay` 분포를 별도 보고.** 예정 시각과 실제 발송 시각의 간격이다. 이 값이 크면 병목은 대상이 아니라 **부하 발생기**다. 현재 in-flight 상한 도달 시에는 큐잉하지 않고 드롭하므로(k6 패턴, `dropped_arrivals`로 계수) 이 값은 스케줄러 자체가 밀렸을 때만 올라간다.
- **출력·입력 길이 분포.** `output_tokens_stdev`는 `max_tokens`를 요청마다 흔든다(서버가 `max_tokens`를 지키므로 **정확**). `prompt_pad_tokens`/`prompt_pad_stdev`는 프롬프트를 늘려 입력 길이 분포를 만드는데, 토크나이저를 반입하지 않으므로 문자 수 기준 **근사값이며 UI에도 그렇게 표기**한다. 두 값 모두 시퀀스 해시 기반이라 재현 가능하고, padding 텍스트도 시퀀스마다 달라 그 자체가 prefix 캐시되지 않는다.

## 남은 항목

**트레이스 리플레이 (항목 18)** 만 남았다. 사내 실제 트래픽을 캡처해 그대로 재생하는 기능으로, 합성 프롬프트 형태를 두고 논쟁하는 대신 실측 근거를 주므로 **사내 도구로서 신뢰도를 가장 크게 올릴 수 있는 항목**이다. 다만 캡처 포맷 정의·업로드 경로·보관 정책(프롬프트에 사내 정보가 포함됨)이 함께 필요하므로, 별도 설계가 선행돼야 한다.
