# Changelog

## v0.4.1

- 모델·웹 대상 호스트 허용 목록을 `TARGET_ALLOWED_HOST_SUFFIXES`로 설정할 수 있습니다.
- 허용된 도메인의 정확한 이름과 하위 도메인만 수락하며 유사 도메인 우회를 차단합니다.
- 폐쇄망 기본 매니페스트에서 `.internal`과 `.kubagents-ofc.koreacb.com`을 허용합니다.

## v0.4.0

- Qwen Code/OpenCode의 OpenAI 호환 요청을 익명 캡처하는 프록시와 세션 단위 trace replay를 추가했습니다.
- 동시 사용자의 파일 읽기·검색·코드 수정·테스트 반복을 재현하는 agent workload와 단계별 토큰/think time 설정 UI를 추가했습니다.
- 캡처 토큰을 UI에서 발급·교체하고 해시만 PostgreSQL에 저장하도록 했습니다.
- 실행 결과에 workload 구성, 시나리오별 지표, 안정 용량 근거와 비교 정보를 보강했습니다.
- 원격 모델 호출형 linux/amd64 폐쇄망 번들에 고정 버전 이미지 4개, Harbor 렌더링 매니페스트, Podman load/push 스크립트 및 SHA-256 체크섬을 포함합니다.
- 이미지 4개를 압축 해제 없이 `podman load -i` 한 번으로 불러오는 통합 다중 이미지 아카이브를 제공합니다.
- 로컬 Kubernetes 노드를 잘못 측정하던 DCGM/node exporter와 내장 Prometheus 및 ClusterRole을 제거하고, 기존 Traefik 기본 TLS와 클러스터 Harbor 인증을 사용하도록 배포를 단순화했습니다.
- chmod가 제한된 NFS/CSI PVC에서도 PostgreSQL을 초기화할 수 있도록 `PGDATA`를 볼륨 마운트 루트 아래 전용 디렉터리로 지정했습니다.
- Kubernetes 배포에 비루트 실행, health probe, resource requests/limits, Harbor imagePullSecret 연결을 추가했습니다.

## v0.3.2

- 릴리스 이미지 아카이브를 gzip 압축 상태(`images/*.tar.gz`)로 배포 — `podman load`가 압축 해제 없이 바로 읽으므로, 폐쇄망 반입 후 개별 이미지를 따로 풀 필요가 없어짐(번들 전체는 여전히 한 번 풀어야 함). 대상 아키텍처는 x86_64(linux/amd64).

## v0.3.1

- Traefik Ingress 추가 (`load-observatory.kubagents-ofc.koreacb.com`, TLS) — `web` Service가 클러스터 밖에서 도달 가능해짐. 호스트를 바꾸면 Ingress·`OIDC_REDIRECT_URL`·Keycloak Redirect URI 세 곳을 함께 바꿔야 함(문서화됨).

## v0.3.0

실무 회복력과 접근 통제.

- 대상 서버가 죽으면 무한정 재시도하는 대신 연속 실패 시 지수 백오프(jitter 포함)로 대기 — 복구되는 순간 모든 VU가 한꺼번에 몰려가서 다시 죽이는 문제 방지 (`BackoffEvents`/`BackoffSeconds`)
- HTTP 커넥션 풀 튜닝 (`MaxIdleConnsPerHost`) — Go 기본값(호스트당 2개)이 다수 VU 환경에서 불필요한 TCP 재수립을 유발하던 문제 수정
- Keycloak(OIDC) 로그인 연동 — 서명된 stateless 쿠키 세션, `OIDC_ISSUER_URL` 미설정 시 기존처럼 로그인 없이 동작(하위 호환)
- 실행 감사 로그 — 로그인한 사용자가 실행을 시작하면 `created_by`로 기록, 실행 목록·export에 노출

## v0.2.0

측정 신뢰성 고도화. "부하를 걸었다"에서 "안정 용량을 증명했다"로.

- 요청 시작·완료·시간 종료 취소·HTTP 실패·전송 오류를 분리 집계 (`Issued/Completed/Cancelled/HTTPFailures/TransportErrors`)
- 매 초 목표 부하·활성 요청·대기열·완료 RPS 실시간 표시 (`LiveProgress`)
- 종료 시 즉시 취소 대신 `drain_seconds` 유예 시간 제공 (진행 중인 요청은 완료까지 대기)
- 완료율이 `min_completion_percent` 미달이면 경고로 표시하고 안정 용량으로 판정하지 않음
- 워밍업 이후 `steady_state_seconds` 구간만 P50/P95/TTFT/TPOT 집계
- 시나리오별 완료율·지연시간·토큰 처리량 분리 (`ScenarioResult`)
- Poisson 도착 패턴 + 예정 도착 시각 기준 지연 측정 (coordinated omission 방지)
- 출력 토큰·프롬프트 길이 분포 (평균 근처 지터, 고정값 아님)
- 용량 곡선 스윕 (`AdvanceSearch`) — 이분 탐색 대신 부하 사다리를 걸어 완주 지점을 찾음
- 서버 측 지표로 지연 원인 귀속 (`server_bound`/`client_bound`/`mismatch`)
- Provenance 수집·비교 — 실행 간 `max_num_batched_tokens`, 캐시 정책, 출력 길이 고정 여부가 다르면 비교 불가 경고
- JSON/CSV 내보내기 (판정·provenance 포함, long-format CSV)

## v0.1.1 and earlier

사용자 여정, 워크로드 가이드, 실행 가시성 개선. 상세 내역은 `git log v0.1.0..v0.1.1`.

## v0.1.0

폐쇄망 부하 측정 MVP. PostgreSQL 기반 분산 에이전트, API 키 저장 시 암호화, Kubernetes 오프라인 배포.
