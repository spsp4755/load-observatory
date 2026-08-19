#!/bin/sh
set -eu

ARCHIVE_INPUT="${1:-./images}"
REGISTRY="${2:-harbor.kubagents-ofc.koreacb.com}"
VERSION="${3:-v0.4.2}"

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

echo "All four linux/amd64 images were pushed to $REGISTRY/load-observatory"
