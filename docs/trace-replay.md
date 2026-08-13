# 실제 트래픽 트레이스 재생

Load Observatory는 GuideLLM과 vLLM의 trace replay 방식을 참고해 사내 요청 간격과 길이 분포를 재생한다. 별도 Python 도구나 외부 데이터셋을 설치하지 않으며 기존 Go Agent가 실행한다.

## 사용 방법

1. 부하 테스트 화면의 `실제 트래픽 파일 재생`에서 JSON 또는 JSONL 파일을 선택한다.
2. 재생 속도를 정한다. `1`은 원래 간격, `2`는 두 배 빠른 간격이다.
3. 실행 시간과 최대 진행 요청을 확인한 뒤 수동 테스트를 시작한다.

파일을 가져오면 RPS 모드, 단일 샤드, 워밍업 0회로 전환된다. 트레이스는 고유한 시간축이 있으므로 단계형 부하와 자동 용량 탐색에는 사용할 수 없다.

## 파일 형식

JSON 배열, `{ "events": [...] }`, JSONL을 지원한다. 이벤트는 시간순이어야 하며 최대 10,000건이다.

```json
{"timestamp_ms":0,"name":"chat","prompt":"간단히 요약해 주세요","prompt_tokens":512,"max_tokens":256}
{"timestamp_ms":850,"name":"coding","prompt_tokens":4096,"max_tokens":8192}
```

GuideLLM 계열 필드인 `timestamp`, `input_length`, `output_length`도 각각 `timestamp_ms`, `prompt_tokens`, `max_tokens`로 읽는다. ISO 8601 timestamp도 사용할 수 있다. 첫 이벤트 시각을 0ms로 정규화하므로 운영 로그의 절대 시각은 저장하지 않는다.

- `prompt`가 있으면 그 내용을 요청한다.
- `prompt`가 없으면 화면의 기본 프롬프트를 사용한다.
- `prompt_tokens`는 토크나이저 없이 문자 수로 근사한 패딩이다. 정확한 입력 토큰 수가 필요하면 실제 프롬프트를 넣는다.
- `max_tokens`는 출력 상한이다. 실제 출력 길이를 고정하려면 대상 서버가 지원하는 경우에만 `ignore_eos`를 켠다.
- `name`은 결과의 시나리오별 분석에 사용한다.

## 폐쇄망과 민감정보

브라우저는 파일을 직접 읽고 파싱한 이벤트를 폐쇄망 내부 Controller API로 전송한다. 외부 SaaS, Hugging Face Hub, CDN, 원격 tokenizer에 연결하지 않는다. 트레이스는 브라우저 localStorage에는 저장하지 않지만 실행 재현과 결과 비교를 위해 Controller 저장소의 실행 설정에는 포함된다.

실제 프롬프트가 민감하면 원문을 제거하고 `prompt_tokens`만 남긴 익명 트레이스를 사용한다. Kubernetes NetworkPolicy는 Controller, Agent, 모델 API, 내부 Prometheus/DCGM 이외의 egress를 차단하는 구성을 권장한다.
