# 폐쇄망 Kubernetes 배포 가이드

이 릴리스는 `linux/amd64`(x86_64) 이미지 7개와 Harbor용 Kubernetes 매니페스트를 포함합니다.

- Namespace: `load-observatory`
- Harbor project: `harbor.kubagents-ofc.koreacb.com/load-observatory`
- Service URL: `https://load-observatory.kubagents-ofc.koreacb.com`
- 배포 파일: `k8s-harbor.yaml`

## 1. 반입 파일 검증

인터넷 연결이 가능한 빌드 PC에서 받은 두 파일을 폐쇄망으로 함께 반입합니다.

```text
load-observatory-v0.4.0-amd64.tar.gz
load-observatory-v0.4.0-amd64.tar.gz.sha256
```

폐쇄망 Linux 호스트에서 검증하고 압축을 풉니다.

```bash
sha256sum -c load-observatory-v0.4.0-amd64.tar.gz.sha256
tar -xzf load-observatory-v0.4.0-amd64.tar.gz
cd load-observatory-v0.4.0-amd64
sha256sum -c SHA256SUMS
```

## 2. Harbor 프로젝트와 Namespace 준비

Harbor에 `load-observatory` 프로젝트를 만들고 push 가능한 계정을 준비합니다. Kubernetes Namespace는 다음 이름으로 만듭니다.

```bash
kubectl create namespace load-observatory
podman login harbor.kubagents-ofc.koreacb.com
```

Harbor가 사설 CA를 사용한다면 CA 인증서를 Podman과 모든 Kubernetes 노드의 신뢰 저장소에 먼저 등록하십시오. `--tls-verify=false`는 운영 환경에서 권장하지 않습니다.

## 3. Podman load 및 Harbor push

압축된 이미지 파일은 별도 해제 없이 `podman load`로 직접 읽을 수 있습니다. 제공 스크립트는 7개 이미지를 로드하고 정해진 Harbor 경로로 tag/push합니다.

```bash
chmod +x deploy/load-images.sh
./deploy/load-images.sh ./images harbor.kubagents-ofc.koreacb.com v0.4.0
```

Kubernetes가 Harbor에서 이미지를 가져올 수 있도록 pull secret을 만듭니다.

```bash
kubectl -n load-observatory create secret docker-registry harbor-credentials \
  --docker-server=harbor.kubagents-ofc.koreacb.com \
  --docker-username='<HARBOR_USER>' \
  --docker-password='<HARBOR_PASSWORD>'
```

## 4. 애플리케이션 Secret 생성

비밀번호와 키를 셸 기록에 남기지 않도록 먼저 환경변수로 읽고, Secret을 생성한 뒤 즉시 해제합니다. PostgreSQL 비밀번호에는 URL 예약 문자를 피하거나 URL 인코딩한 값을 `DATABASE_URL`에 사용하십시오.

```bash
read -rsp 'PostgreSQL password: ' LO_POSTGRES_PASSWORD; echo
LO_ENCRYPTION_KEY="$(openssl rand -base64 32 | tr -d '\n')"
LO_CAPTURE_TOKEN="$(openssl rand -base64 32 | tr -d '\n')"
LO_SESSION_SECRET="$(openssl rand -base64 32 | tr -d '\n')"

kubectl -n load-observatory create secret generic postgres-credentials \
  --from-literal=POSTGRES_DB=load_observatory \
  --from-literal=POSTGRES_USER=load_observatory \
  --from-literal=POSTGRES_PASSWORD="$LO_POSTGRES_PASSWORD" \
  --from-literal=DATABASE_URL="postgres://load_observatory:${LO_POSTGRES_PASSWORD}@postgres:5432/load_observatory?sslmode=disable" \
  --from-literal=TARGET_API_KEY_ENCRYPTION_KEY="$LO_ENCRYPTION_KEY" \
  --from-literal=CAPTURE_PROXY_TOKEN="$LO_CAPTURE_TOKEN" \
  --from-literal=OIDC_CLIENT_SECRET='' \
  --from-literal=SESSION_SECRET="$LO_SESSION_SECRET"

unset LO_POSTGRES_PASSWORD LO_ENCRYPTION_KEY LO_CAPTURE_TOKEN LO_SESSION_SECRET
```

`CAPTURE_PROXY_TOKEN`은 최초 부팅용 값입니다. 배포 후 UI의 **실사용 캡처** 탭에서 새 토큰을 발급하면 PostgreSQL에 해시로 저장되며 Pod 재시작 없이 교체됩니다.

## 5. TLS와 DNS

사내 인증서로 TLS Secret을 만들고, DNS가 Traefik 진입점 IP를 가리키게 합니다.

```bash
kubectl -n load-observatory create secret tls load-observatory-tls \
  --cert=load-observatory.crt \
  --key=load-observatory.key
```

DNS 레코드:

```text
load-observatory.kubagents-ofc.koreacb.com -> <TRAEFIK_INGRESS_IP>
```

## 6. 배포 및 확인

`k8s-harbor.yaml`에는 예시 비밀번호 Secret이 없고, 모든 이미지는 `v0.4.0` 또는 고정된 upstream 버전으로 지정되어 있습니다.

```bash
kubectl apply -f k8s-harbor.yaml
kubectl -n load-observatory rollout status statefulset/postgres --timeout=5m
kubectl -n load-observatory rollout status deployment/controller --timeout=5m
kubectl -n load-observatory rollout status deployment/agent --timeout=5m
kubectl -n load-observatory rollout status deployment/web --timeout=5m
kubectl -n load-observatory get pods,svc,ingress
```

브라우저에서 `https://load-observatory.kubagents-ofc.koreacb.com`을 열고, **모델 등록**에서 내부 OpenAI 호환 API를 등록해 연결 확인을 수행합니다.

## 선택 설정

- Keycloak: `controller`의 `OIDC_ISSUER_URL`을 realm URL로 바꾸고 `OIDC_CLIENT_SECRET` 값을 Secret에 넣습니다. Redirect URI는 `https://load-observatory.kubagents-ofc.koreacb.com/auth/callback`입니다.
- GPU 노드만 모니터링: `dcgm-exporter` DaemonSet에 사내 GPU label 기준 `nodeSelector`/`tolerations`를 추가합니다.
- 기존 Prometheus 사용: `PROMETHEUS_URL`을 기존 주소로 바꾸고 번들 내 Prometheus 및 exporter 리소스를 제거할 수 있습니다.
- 네트워크 범위: 기본 NetworkPolicy는 RFC1918 목적지만 허용합니다. 실제 모델 서버·DNS·Keycloak 대역에 맞게 더 좁히십시오.

## 롤백

이전 버전 이미지가 Harbor에 남아 있다면 `k8s-harbor.yaml`의 controller/agent/web tag를 이전 버전으로 바꾸고 다시 적용합니다. 데이터 스키마 변경 전에는 PostgreSQL PVC 스냅샷을 별도로 생성하십시오.
