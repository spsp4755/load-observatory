# 폐쇄망 Kubernetes 배포 가이드

이 릴리스는 원격 모델 API 호출에 필요한 `linux/amd64`(x86_64) 이미지 4개(controller, agent, web, PostgreSQL)와 Harbor용 Kubernetes 매니페스트를 포함합니다. GPU/노드 exporter와 내장 Prometheus는 배포하지 않습니다.

- Namespace: `load-observatory`
- Harbor project: `harbor.kubagents-ofc.koreacb.com/load-observatory`
- Service URL: `https://load-observatory.kubagents-ofc.koreacb.com`
- 배포 파일: `k8s-harbor.yaml`

## 1. 가장 간단한 단일 이미지 아카이브 방식

이미지 4개는 하나의 아카이브에 들어 있습니다. 압축을 풀지 않고 한 번에 로드합니다.

```bash
sha256sum -c load-observatory-v0.4.1-images-amd64.tar.gz.sha256
podman load -i load-observatory-v0.4.1-images-amd64.tar.gz
```

Harbor로 tag/push까지 자동 처리하려면 Release에서 받은 `load-images.sh`에 같은 파일을 전달합니다. 스크립트 내부에서도 `podman load`는 한 번만 실행됩니다.

```bash
chmod +x load-images.sh
./load-images.sh ./load-observatory-v0.4.1-images-amd64.tar.gz \
  harbor.kubagents-ofc.koreacb.com v0.4.1
```

`k8s-harbor.yaml`도 GitHub Release에서 독립 파일로 받을 수 있습니다. 아래 전체 번들은 문서와 개별 이미지까지 보관해야 할 때만 사용하십시오.

## 2. 전체 배포 번들 방식

인터넷 연결이 가능한 빌드 PC에서 받은 두 파일을 폐쇄망으로 함께 반입합니다.

```text
load-observatory-v0.4.1-amd64.tar.gz
load-observatory-v0.4.1-amd64.tar.gz.sha256
```

폐쇄망 Linux 호스트에서 검증하고 압축을 풉니다.

```bash
sha256sum -c load-observatory-v0.4.1-amd64.tar.gz.sha256
tar -xzf load-observatory-v0.4.1-amd64.tar.gz
cd load-observatory-v0.4.1-amd64
sha256sum -c SHA256SUMS
```

## 3. Harbor 프로젝트와 Namespace 준비

Harbor에 `load-observatory` 프로젝트를 만들고 push 가능한 계정을 준비합니다. Kubernetes Namespace는 다음 이름으로 만듭니다.

```bash
kubectl create namespace load-observatory
podman login harbor.kubagents-ofc.koreacb.com
```

Harbor가 사설 CA를 사용한다면 CA 인증서를 Podman과 모든 Kubernetes 노드의 신뢰 저장소에 먼저 등록하십시오. `--tls-verify=false`는 운영 환경에서 권장하지 않습니다.

## 4. Podman load 및 Harbor push

압축된 이미지 파일은 별도 해제 없이 `podman load`로 직접 읽을 수 있습니다. 제공 스크립트는 4개 이미지를 로드하고 정해진 Harbor 경로로 tag/push합니다.

```bash
chmod +x deploy/load-images.sh
./deploy/load-images.sh ./images harbor.kubagents-ofc.koreacb.com v0.4.1
```

이 매니페스트는 기존 클러스터의 Harbor 인증 설정을 사용하며 별도 `imagePullSecret`을 요구하지 않습니다.

## 5. 애플리케이션 Secret 생성

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

`TARGET_API_KEY_ENCRYPTION_KEY`에는 `openssl rand -base64 32` 결과를 그대로 넣어야 합니다. Kubernetes Secret 저장 인코딩과 애플리케이션 키 인코딩은 별개이므로 임의 문자열이나 명령문 자체를 넣으면 controller가 시작을 거부합니다. 다음 검증 결과는 정확히 `32`여야 합니다.

```bash
kubectl -n load-observatory get secret postgres-credentials \
  -o jsonpath='{.data.TARGET_API_KEY_ENCRYPTION_KEY}' | base64 -d | base64 -d | wc -c
```

## 6. Traefik과 DNS

Ingress는 기존 `traefik` IngressClass의 `websecure` entrypoint와 기본 TLS store를 사용합니다. 클러스터 정책상 이름 있는 인증서가 필요할 때만 `spec.tls[].secretName`을 추가합니다.

DNS 레코드:

```text
load-observatory.kubagents-ofc.koreacb.com -> <TRAEFIK_INGRESS_IP>
```

## 7. 배포 및 확인

`k8s-harbor.yaml`에는 예시 비밀번호 Secret이 없고, 모든 이미지는 `v0.4.1` 또는 고정된 upstream 버전으로 지정되어 있습니다.

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
- 원격 모델 서버 Prometheus 연동: 모델 서버 자체의 vLLM/SGLang/TGI 지표를 수집하는 Prometheus가 이 클러스터에서 접근 가능할 때만 controller의 `PROMETHEUS_URL`을 설정합니다. 설정하지 않아도 부하 테스트와 HTTP/토큰 지표는 정상 동작하며 서버 지표만 `미수집`으로 표시됩니다.
- 네트워크 범위: 기본 NetworkPolicy는 RFC1918 목적지만 허용합니다. 실제 모델 서버·DNS·Keycloak 대역에 맞게 더 좁히십시오.

## 롤백

이전 버전 이미지가 Harbor에 남아 있다면 `k8s-harbor.yaml`의 controller/agent/web tag를 이전 버전으로 바꾸고 다시 적용합니다. 데이터 스키마 변경 전에는 PostgreSQL PVC 스냅샷을 별도로 생성하십시오.
