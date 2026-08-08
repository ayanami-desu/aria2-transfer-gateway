#!/bin/sh
set -eu

usage() {
  printf 'usage: GHCR_USERNAME=... GHCR_TOKEN=... DEPLOY_HOST=user@server [DEPLOY_PATH=/opt/aria2-transfer-gateway] [DEPLOY_PORT=22] %s\n' "$0" >&2
  exit 64
}

deploy_host=${DEPLOY_HOST:-}
deploy_path=${DEPLOY_PATH:-/opt/aria2-transfer-gateway}
deploy_port=${DEPLOY_PORT:-22}
ghcr_username=${GHCR_USERNAME:-}
ghcr_token=${GHCR_TOKEN:-}

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

case "$ghcr_username" in
  *"'"*)
    printf "GHCR_USERNAME must not contain single quotes\n" >&2
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

login_ghcr() {
  if [ -z "$ghcr_username" ] && [ -z "$ghcr_token" ]; then
    return 0
  fi
  if [ -z "$ghcr_username" ] || [ -z "$ghcr_token" ]; then
    printf 'GHCR_USERNAME and GHCR_TOKEN must be set together\n' >&2
    exit 64
  fi
  printf '%s' "$ghcr_token" |
    ssh -p "$deploy_port" "$deploy_host" \
      "docker login ghcr.io --username '$ghcr_username' --password-stdin"
}

printf 'preparing %s:%s\n' "$deploy_host" "$deploy_path"
ssh -p "$deploy_port" "$deploy_host" \
  "mkdir -p -- '$deploy_path' '$deploy_path/hooks' '$deploy_path/deploy'"
login_ghcr

printf 'syncing deployment files\n'
rsync -azP -e "ssh -p $deploy_port" \
  "$project_root/docker-compose.yml" \
  "$deploy_host:$deploy_path/"
rsync -azP -e "ssh -p $deploy_port" \
  "$project_root/hooks/" \
  "$deploy_host:$deploy_path/hooks/"
rsync -azP -e "ssh -p $deploy_port" \
  "$project_root/deploy/prepare-runtime.sh" \
  "$project_root/deploy/gateway.yaml" \
  "$deploy_host:$deploy_path/deploy/"

printf 'pulling and restarting GHCR images\n'
ssh -p "$deploy_port" "$deploy_host" \
  "cd '$deploy_path' && ./deploy/prepare-runtime.sh && docker compose pull && docker compose up -d --no-build --force-recreate"
printf 'deployment complete\n'
