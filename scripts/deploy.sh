#!/bin/sh
set -eu

release_url=https://github.com/ayanami-desu/aria2-transfer-gateway/releases/download/runtime/gateway-runtime.tar.gz
remote_default=/opt/aria2-transfer-gateway
source_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
deploy_host=${DEPLOY_HOST:-}
deploy_path=${DEPLOY_PATH:-$remote_default}
deploy_port=${DEPLOY_PORT:-22}

usage() {
  printf 'usage: [DEPLOY_HOST=user@server] [DEPLOY_PATH=/path/to/deploy] [DEPLOY_PORT=22] %s\n' "$0" >&2
  exit 64
}

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


run_compose() {
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
  run_compose "$source_root"
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

printf 'deploying runtime bundle to %s:%s\n' "$deploy_host" "$deploy_path"
ssh -p "$deploy_port" "$deploy_host" "
set -eu
command -v curl >/dev/null 2>&1 || { printf 'curl is required on remote host\\n' >&2; exit 69; }
command -v tar >/dev/null 2>&1 || { printf 'tar is required on remote host\\n' >&2; exit 69; }
command -v docker >/dev/null 2>&1 || { printf 'docker is required on remote host\\n' >&2; exit 69; }
mkdir -p -- '$deploy_path'
for item in '$deploy_path'/* '$deploy_path'/.[!.]* '$deploy_path'/..?*; do
  [ -e \"\$item\" ] || continue
  case \"\$item\" in
    '$deploy_path'/.env|'$deploy_path'/config.yaml|'$deploy_path'/runtime|'$deploy_path'/data|'$deploy_path'/logs) continue ;;
  esac
  rm -rf -- \"\$item\"
done
tmp_dir=\$(mktemp -d /tmp/aria2-transfer-gateway.XXXXXX)
cleanup() { rm -rf -- \"\$tmp_dir\"; }
trap cleanup EXIT HUP INT TERM
if [ -f '$deploy_path/config.yaml' ]; then
  cp '$deploy_path/config.yaml' \"\$tmp_dir/config.yaml.backup\"
fi
curl -fsSL '$release_url' -o \"\$tmp_dir/gateway-runtime.tar.gz\"
tar -xzf \"\$tmp_dir/gateway-runtime.tar.gz\" -C '$deploy_path'
if [ -f \"\$tmp_dir/config.yaml.backup\" ]; then
  cp \"\$tmp_dir/config.yaml.backup\" '$deploy_path/config.yaml'
fi
cd '$deploy_path'
./deploy/prepare-runtime.sh
docker compose pull
docker compose up -d --no-build --force-recreate
"
printf 'deployment complete\n'
