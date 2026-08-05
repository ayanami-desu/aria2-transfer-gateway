#!/bin/sh
set -eu

usage() {
  printf 'usage: DEPLOY_HOST=user@server [DEPLOY_PATH=/opt/aria2-transfer-gateway] [DEPLOY_PORT=22] %s [aria2|gateway|ariang ...]\n' "$0" >&2
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
  *"'")
    printf "DEPLOY_PATH must not contain single quotes\n" >&2
    exit 64
    ;;
esac

command -v ssh >/dev/null 2>&1 || {
  printf 'ssh is required\n' >&2
  exit 69
}

services=
for service in "$@"; do
  case "$service" in
    aria2|gateway|ariang)
      services="$services $service"
      ;;
    *)
      printf 'unknown service: %s\n' "$service" >&2
      usage
      ;;
  esac
done

if [ -z "$services" ]; then
  services=' aria2 gateway ariang'
fi

printf 'restarting %s on %s:%s\n' "$services" "$deploy_host" "$deploy_path"
ssh -p "$deploy_port" "$deploy_host" "cd '$deploy_path' && ./deploy/prepare-runtime.sh && docker compose up -d --no-build --force-recreate$services"
printf 'services restarted without rebuilding images\n'
