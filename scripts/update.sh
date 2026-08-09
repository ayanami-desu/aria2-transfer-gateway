#!/bin/sh
set -eu

remote_default=/opt/aria2-transfer-gateway
source_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
deploy_host=${DEPLOY_HOST:-}
deploy_path=${DEPLOY_PATH:-$remote_default}
deploy_port=${DEPLOY_PORT:-22}

check_path() {
  case "$1" in
    *"'"*)
      printf "DEPLOY_PATH must not contain single quotes\n" >&2
      exit 64
      ;;
  esac
}

check_port() {
  case "$1" in
    ''|*[!0-9]*)
      printf 'DEPLOY_PORT must be numeric\n' >&2
      exit 64
      ;;
  esac
}

update_compose() {
  cd "$1"
  ./deploy/prepare-runtime.sh
  docker compose pull
  docker compose up -d --no-build --force-recreate
}

check_path "$deploy_path"
check_port "$deploy_port"

if [ -z "$deploy_host" ]; then
  command -v docker >/dev/null 2>&1 || {
    printf 'docker is required\n' >&2
    exit 69
  }
  update_compose "$source_root"
  exit 0
fi

case "$deploy_path" in
  /*) ;;
  *)
    printf 'DEPLOY_PATH must be absolute for remote deployment\n' >&2
    exit 64
    ;;
esac

command -v ssh >/dev/null 2>&1 || {
  printf 'ssh is required\n' >&2
  exit 69
}

printf 'updating images and restarting services at %s:%s\n' "$deploy_host" "$deploy_path"
ssh -p "$deploy_port" "$deploy_host" "
set -eu
command -v docker >/dev/null 2>&1 || { printf 'docker is required on remote host\\n' >&2; exit 69; }
cd '$deploy_path'
./deploy/prepare-runtime.sh
docker compose pull
docker compose up -d --no-build --force-recreate
"
printf 'images updated and services restarted\n'
