# Changelog

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
