# Load Observatory에 적용할 공개 부하 테스트 패턴

조사일: 2026-08-04. 아래 내용은 각 도구를 제품 런타임에 포함하자는 제안이 아니라, 검증된 **측정·스케줄링 의미론**을 현재 Go runner에 최소로 이식하기 위한 조사 결과다. 폐쇄망 Podman/Kubernetes 배포에서는 k6·Locust·vLLM·GenAI-Perf를 별도 런타임으로 추가하면 이미지, 버전, 결과 형식이 분리되므로 이 방식이 더 단순하다.

## 결론

다음 세 가지를 우선 적용한다.

1. **도착률 기반 단계형 부하와 측정 구간**: 현재 VU/RPS 단일 값에 `warm-up → ramp → measure → cool-down` 단계를 추가하고, RPS 모드에는 실행 중 요청 상한과 `dropped arrivals`를 기록한다.
2. **LLM 스트리밍 품질 지표**: 기존 TTFT 외에 TTFO(최종 답변 첫 토큰), ITL, TPOT, E2E, 입력/출력 토큰 처리량, SLO 충족 요청 비율(goodput)을 기록한다.
3. **현실적인 시나리오 혼합**: 프롬프트군별 가중치, 캐시 프로필, 사용자 대기시간을 단순한 입력값으로 제공한다. 임의 스크립트 실행기는 도입하지 않는다.

## 공개 도구에서 가져올 패턴

| 출처 | 확인된 패턴 | Load Observatory 적용 |
| --- | --- | --- |
| [Grafana k6: constant arrival rate](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/constant-arrival-rate/) | 응답 완료와 무관하게 목표 도착률로 요청을 시작하는 open model이다. | 기존 RPS 모드의 의미를 명확히 `도착률`로 하고, 목표 요청을 시작하지 못한 수를 결과에 별도 표시한다. |
| [Grafana k6: arrival-rate VU allocation](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/arrival-rate-vu-allocation/) | 가용 VU가 부족하면 `dropped_iterations`를 기록한다. | `max in-flight`를 넘으면 새 요청을 무한정 goroutine으로 만들지 말고 `dropped arrivals`로 계수한다. 이 값은 부하 발생기 자체의 한계와 대상의 한계를 구분하는 근거가 된다. |
| [Grafana k6: ramping arrival rate](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/ramping-arrival-rate/) | 시간·목표 도착률 stage로 ramp-up, 유지, ramp-down을 구성한다. | 자동 탐색과 수동 실행 모두에 단계 배열(`기간`, `목표 VU/RPS`)을 추가한다. 초기 단계는 안정성 확인용 warm-up으로 결과 판정에서 제외한다. |
| [Grafana k6: thresholds](https://grafana.com/docs/k6/latest/using-k6/thresholds/) | 오류율·지연 percentile을 pass/fail SLO로 정의하고, 평가 지연 후 실패 시 중단할 수 있다. | `오류율`, `E2E P95`, `TTFT/TTFO P95`, `TPOT P95`, `최소 goodput`을 guardrail로 제공한다. 중단은 해당 Load Observatory run context만 취소한다. |
| [Locust: custom load shapes](https://docs.locust.io/en/2.43.4/custom-load-shape.html) | 시간에 따른 사용자 수·증가율을 직접 제어하고, 종료 시점도 정할 수 있다. | 고급 스크립트 대신 stage UI만 제공한다. 운영자가 spike·stress·soak을 재현하기에 충분하다. |
| [Locust: tasks, weights, wait time](https://docs.locust.io/en/2.34.0/writing-a-locustfile.html) | task/user weight와 wait time으로 사용 패턴과 think time을 모델링한다. | 모델 테스트에는 `짧은 질의`, `코딩`, `긴 출력`, `후속 질문` 등의 프롬프트군 가중치와 사용자 간 대기시간 범위를 저장한다. |
| [vLLM bench serve](https://docs.vllm.ai/en/stable/cli/bench/serve/) | 요청률과 최대 동시성을 분리하고, warm-up·선형/지수 RPS ramp·TTFT/TPOT/ITL/E2E percentile·goodput을 지원한다. | arrival pattern(균일/Poisson)은 두 번째 단계로 두고, 먼저 warm-up, in-flight 상한, stage, LLM 지표를 구현한다. |
| [NVIDIA GenAI-Perf](https://docs.nvidia.com/deeplearning/triton-inference-server/archives/triton-inference-server-2550/user-guide/docs/perf_benchmark/genai_perf.html) | 동시성 또는 요청률 부하에서 output-token throughput, TTFT, ITL, request throughput을 함께 보고 CSV/JSON 결과를 남긴다. | 스트리밍 청크 수신 시각으로 ITL/TPOT을 계산하고, 실행 결과의 JSON/CSV 내보내기와 비교 화면의 기준 지표로 사용한다. |
| [NVIDIA GenAI-Perf: multi-turn chat](https://docs.nvidia.com/deeplearning/triton-inference-server/archives/triton-inference-server-2630/user-guide/docs/perf_analyzer/genai-perf/docs/multi_turn.html) | 세션별 turn, 응답 후 delay, 입·출력 토큰 길이로 여러 채팅 세션을 재현한다. | agent 테스트는 실제 도구 실행기를 만들기 전에, turn 수·후속 요청 비율·턴 사이 delay·프롬프트 길이 분포를 갖는 멀티턴 템플릿으로 시작한다. |

## 모델 지표의 정확한 정의

| 지표 | 정의 | 판정에서 보는 이유 |
| --- | --- | --- |
| TTFT | 요청 전송부터 첫 스트리밍 토큰까지 | 사용자가 최초 반응을 기다리는 시간 |
| TTFO | 요청 전송부터 첫 최종 답변 토큰까지 | reasoning 모델에서 reasoning token만 먼저 나오는 경우의 체감 대기 시간 |
| ITL | 인접한 출력 토큰/청크 사이 시간 | 생성 중 끊김과 디코딩 병목 |
| TPOT | 첫 토큰 이후 출력 토큰 하나당 평균 시간 | 긴 코딩 출력의 체감 속도 |
| E2E | 요청 전송부터 완료까지 | 전체 작업 완료 시간 |
| output tok/s | 전체 출력 토큰 ÷ 측정 구간 | GPU가 실제로 공급한 생성량 |
| goodput | TTFT/TPOT/E2E SLO를 모두 만족한 성공 요청 비율 또는 토큰 처리량 | 단순 최고 RPS 대신 usable capacity를 보여 줌 |

TTFO는 reasoning 모델에서 특히 필요하다. NVIDIA는 reasoning token과 최종 output token의 첫 수신을 구분하는 마이그레이션 지침을 제공한다. [NVIDIA AIPerf migration guide](https://docs.nvidia.com/aiperf/getting-started/migrating-from-gen-ai-perf)

## 구현 순서

### 1. 다음 개발 단위 — 가장 큰 효과

- `warm-up / measure / cool-down` 구간과 stage형 VU·도착률 스케줄
- RPS 실행의 `max in-flight`, `dropped arrivals`, 실제 시작률·완료률 기록
- guardrail: warm-up 이후부터 오류율, P95, TTFT P95, 최소 output tok/s를 평가하고 실패 시 **해당 run만** 중지

### 2. 모델 결과의 신뢰도 보강

- SSE 청크별 시각을 수집해 TTFO·ITL·TPOT·E2E percentile과 goodput 계산
- 입력/출력 길이 bucket과 캐시 프로필별 결과를 같은 실행에 분리 표시
- JSON/CSV 내보내기 및 동일 target/profile의 실행 비교

### 3. 현실적 사용 패턴

- 프롬프트 템플릿 가중치, 변형률, 사용자 대기시간 범위, 멀티턴 세션 템플릿
- 캐시 warm/cold/mixed를 실행 설정으로 고정해 실행 간 비교 가능하게 저장

## 지금은 도입하지 않을 것

- k6/Locust를 controller에서 실행하거나 사용자가 임의 JavaScript/Python을 올리는 기능: 폐쇄망 이미지와 보안 검토 범위를 크게 늘리지만, 위의 stage·가중치·대기시간으로 현재 요구를 충족한다.
- 브라우저 렌더링 부하(k6 browser): URL HTTP 부하와 성격이 다르고 Chromium 실행 노드가 별도로 필요하다. 실제 사용자 화면 렌더링/Synthetic journey가 요구될 때만 별도 worker pool으로 추가한다.
- Poisson/Gamma 도착 패턴: 유용하지만 균일 도착률과 stage, in-flight 상한을 먼저 검증한 뒤 추가한다.
