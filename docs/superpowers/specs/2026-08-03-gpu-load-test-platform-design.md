# GPU 및 웹 부하 테스트 플랫폼 설계

## 목표

폐쇄망 Kubernetes 환경에서 OpenAI 호환 모델 API와 내부 웹/API 서비스의 처리 한계를 측정한다. 운영자는 동시 사용자 또는 요청률을 점진적으로 올리고, 지연 시간·오류율·처리량·GPU 지표를 한 화면에서 확인한다.

## 범위

- 모델 대상: OpenAI 호환 `POST /v1/chat/completions` HTTP API
- 웹 대상: 등록한 내부 HTTP/HTTPS URL
- 부하 방식: 동시 사용자(VU), 목표 RPS, 램프업, 고정 시간 실행
- 결과: 평균/P50/P95/P99 지연, 오류율, RPS, TTFT, 토큰 처리량, GPU/호스트 지표
- 배포: `linux/amd64` OCI 이미지, Podman 반입, 폐쇄망 Kubernetes

## 제외 범위

- gRPC, Triton 전용 프로토콜, 브라우저 렌더링 기반 부하 테스트
- 외부 SaaS, 외부 인증, 인터넷 의존 CDN
- Redis/NATS 등 별도 작업 큐

## 구성

### Controller (Go)

HTTP API를 제공한다. 대상 등록과 허용 목록 검증, 테스트 생성·중지, Agent 할당, 결과 집계, 실행 이력을 맡는다. PostgreSQL을 영속 저장소로 사용한다.

### Load Agent (Go)

Controller에서 할당된 작업을 폴링한다. 지정한 VU/RPS/램프업에 맞춰 HTTP 요청을 발생시키고, 모델 응답은 첫 바이트 도착 시점으로 TTFT를 측정한다. 실행 중 집계 지표를 Controller와 Prometheus에 보낸다.

### Web UI (React + TypeScript)

대상 등록, 테스트 작성, 실행 상태, 결과 그래프 및 한계점 요약을 제공한다. Controller API만 호출하며 외부 자산이나 API에 의존하지 않는다.

### 관측

Prometheus는 Controller와 Agent의 애플리케이션 지표를 수집한다. GPU 서버에는 NVIDIA DCGM Exporter가 별도 설치되어 있다고 가정하며, UI는 Prometheus 쿼리 결과로 GPU 사용률과 VRAM 사용량을 함께 표시한다.

## 데이터 흐름

1. 운영자가 UI에서 등록된 대상과 부하 프로필을 골라 실행한다.
2. Controller는 대상 주소가 허용된 호스트/CIDR인지 검증하고 실행 레코드를 만든다.
3. 유휴 Agent는 폴링으로 실행을 가져가고 부하를 발생시킨다.
4. Agent는 주기적 요약과 종료 결과를 Controller에 제출하고 Prometheus 지표를 노출한다.
5. UI는 Controller의 실행 요약과 Prometheus 지표를 폴링해 표시한다.

## 안전 제약

- 대상 URL은 사전에 등록된 호스트명 또는 private CIDR만 허용한다.
- 리다이렉트 후 주소도 다시 검증한다.
- Controller는 최대 VU, 최대 RPS, 최대 실행 시간을 서버 측에서 강제한다.
- 모든 생성·중지·실행 요청을 감사 로그에 남긴다.
- Agent에는 필요한 내부 대상 네트워크로만 나가는 Kubernetes NetworkPolicy를 적용한다.

## 핵심 API

- `POST /api/targets`: 모델 또는 웹 대상 등록
- `POST /api/runs`: 부하 테스트 실행 생성
- `POST /api/runs/{id}/stop`: 실행 중지
- `GET /api/runs/{id}`: 실행·요약·최근 시계열 조회
- `POST /api/agent/claim`: Agent 작업 할당
- `POST /api/agent/runs/{id}/samples`: Agent 지표 보고

## 한계점 판정

실행 설정의 SLO를 기준으로 최초 위반 구간을 한계점으로 기록한다. 기본값은 P95 지연 2초 초과 또는 오류율 2% 초과다. 모델 테스트는 TTFT P95도 별도로 적용할 수 있다.

## 테스트 기준

- URL 허용 목록과 리다이렉트 재검증 단위 테스트
- 부하 프로필의 VU/RPS·램프업 계산 단위 테스트
- OpenAI 스트리밍·비스트리밍 응답의 TTFT 측정 단위 테스트
- Controller와 Agent의 작업 할당·결과 집계 통합 테스트

## 배포

각 구성요소는 `linux/amd64` 멀티 스테이지 OCI 이미지로 만든다. 인터넷 연결 환경에서 이미지를 빌드한 뒤 `podman save`로 tarball을 만들고, 폐쇄망에서 `podman load` 후 내부 레지스트리로 push한다. Kubernetes에는 Deployment, Service, ConfigMap, Secret, NetworkPolicy, PostgreSQL StatefulSet을 배포한다. Prometheus와 DCGM Exporter는 기존 사내 관측 스택을 우선 사용한다.

## 구현 순서

1. Go 모듈과 데이터 모델, 허용 대상 검증
2. Controller API와 PostgreSQL 저장
3. Agent의 HTTP 모델·웹 요청 실행과 결과 보고
4. React 실행 생성·결과 화면
5. Prometheus 연동 및 Kubernetes 매니페스트
