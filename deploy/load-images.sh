#!/bin/sh
set -eu

ARCHIVE_INPUT="${1:-./images}"
REGISTRY="${2:-harbor.kubagents-ofc.koreacb.com}"
VERSION="${3:-v0.4.0}"

if [ -f "$ARCHIVE_INPUT" ]; then
  echo "[load all] $ARCHIVE_INPUT"
  podman load -i "$ARCHIVE_INPUT"
  LOAD_SEPARATELY=false
elif [ -d "$ARCHIVE_INPUT" ]; then
  LOAD_SEPARATELY=true
else
  echo "Archive file or image directory not found: $ARCHIVE_INPUT" >&2
  exit 1
fi

load_and_push() {
  archive="$1"
  source="$2"
  destination="$3"
  if [ "$LOAD_SEPARATELY" = true ]; then
    echo "[load] $archive"
    podman load -i "$ARCHIVE_INPUT/$archive"
  fi
  echo "[push] $REGISTRY/$destination"
  podman tag "$source" "$REGISTRY/$destination"
  podman push "$REGISTRY/$destination"
}

load_and_push controller.tar.gz "load-observatory/controller:$VERSION" "load-observatory/controller:$VERSION"
load_and_push agent.tar.gz "load-observatory/agent:$VERSION" "load-observatory/agent:$VERSION"
load_and_push web.tar.gz "load-observatory/web:$VERSION" "load-observatory/web:$VERSION"
load_and_push postgres-16.tar.gz "postgres:16" "load-observatory/postgres:16"
load_and_push prometheus.tar.gz "prom/prometheus:v2.54.1" "load-observatory/prometheus:v2.54.1"
load_and_push dcgm-exporter.tar.gz "nvcr.io/nvidia/k8s/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04" "load-observatory/dcgm-exporter:3.3.8-3.6.0-ubuntu22.04"
load_and_push node-exporter.tar.gz "prom/node-exporter:v1.8.2" "load-observatory/node-exporter:v1.8.2"

echo "All linux/amd64 images were pushed to $REGISTRY/load-observatory"
