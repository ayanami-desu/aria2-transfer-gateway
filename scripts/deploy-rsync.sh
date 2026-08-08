#!/bin/sh
set -eu

usage() {
  printf 'usage: DEPLOY_HOST=user@server [DEPLOY_PATH=/opt/aria2-transfer-gateway] [DEPLOY_PORT=22] %s\n' "$0" >&2
  exit 64
}

deploy_host=${DEPLOY_HOST:-}
deploy_path=${DEPLOY_PATH:-/opt/aria2-transfer-gateway}
deploy_port=${DEPLOY_PORT:-22}

if [ -z "$deploy_host" ]; then
  usage
fi

case "$deploy_port" in
  ''|*[!0-9]*)
    printf 'DEPLOY_PORT must be a number\n' >&2
    exit 64
    ;;
esac

case "$deploy_path" in
  /*) ;;
  *)
    printf 'DEPLOY_PATH must be absolute\n' >&2
    exit 64
    ;;
esac

case "$deploy_path" in
  *"'"*)
    printf "DEPLOY_PATH must not contain single quotes\n" >&2
    exit 64
    ;;
esac

command -v rsync >/dev/null 2>&1 || {
  printf 'rsync is required\n' >&2
  exit 69
}
command -v ssh >/dev/null 2>&1 || {
  printf 'ssh is required\n' >&2
  exit 69
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

printf 'preparing %s:%s\n' "$deploy_host" "$deploy_path"
ssh -p "$deploy_port" "$deploy_host" "mkdir -p -- '$deploy_path'"

printf 'syncing project files\n'
rsync -azP \
  -e "ssh -p $deploy_port" \
  --exclude '/.git/' \
  --exclude '/.codegraph/' \
  --exclude '/.env.local' \
  --exclude '/runtime/' \
  --exclude '/data/' \
  --exclude '/tmp/' \
  --exclude '/bin/' \
  --exclude '/gateway' \
  --exclude '/third_party/ariang/node_modules/' \
  --exclude '/third_party/ariang/dist/' \
  --exclude '*.log' \
  "$project_root/" "$deploy_host:$deploy_path/"

printf 'building and restarting services\n'
ssh -p "$deploy_port" "$deploy_host" "cd '$deploy_path' && ./deploy/prepare-runtime.sh && docker compose up -d --build"
printf 'cleaning unused project images\n'
ssh -p "$deploy_port" "$deploy_host" "docker image prune -af --filter label=com.docker.compose.project=aria2-transfer-gateway"
printf 'deployment complete\n'
