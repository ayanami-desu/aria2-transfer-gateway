#!/bin/sh
set -eu

repo_url=https://github.com/ayanami-desu/aria2-transfer-gateway.git
branch=main
remote_default=/opt/aria2-transfer-gateway
local_default=${HOME:-/tmp}/.local/share/aria2-transfer-gateway

deploy_host=${DEPLOY_HOST:-}
if [ -n "$deploy_host" ]; then
  deploy_path=${DEPLOY_PATH:-$remote_default}
else
  deploy_path=${DEPLOY_PATH:-$local_default}
fi

usage() {
  printf 'usage: [DEPLOY_HOST=user@server] [DEPLOY_PATH=/path/to/deploy] %s\n' "$0" >&2
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

sync_checkout() {
  mkdir -p -- "$deploy_path"
  if [ ! -e "$deploy_path/.git" ]; then
    git -C "$deploy_path" init
  fi
  if git -C "$deploy_path" remote get-url origin >/dev/null 2>&1; then
    git -C "$deploy_path" remote set-url origin "$repo_url"
  else
    git -C "$deploy_path" remote add origin "$repo_url"
  fi
  git -C "$deploy_path" fetch --prune origin "$branch"
  git -C "$deploy_path" checkout -B "$branch" "origin/$branch"
  git -C "$deploy_path" reset --hard "origin/$branch"
}

check_runtime_files() {
  if [ ! -f "$deploy_path/.env" ]; then
    printf '.env is required at %s\n' "$deploy_path" >&2
    exit 64
  fi
  if [ ! -f "$deploy_path/config.yaml" ]; then
    printf 'config.yaml is required at %s\n' "$deploy_path" >&2
    exit 64
  fi
}

check_path "$deploy_path"

if [ -z "$deploy_host" ]; then
  command -v git >/dev/null 2>&1 || {
    printf 'git is required\n' >&2
    exit 69
  }
  command -v docker >/dev/null 2>&1 || {
    printf 'docker is required\n' >&2
    exit 69
  }
  sync_checkout
  check_runtime_files
  cd "$deploy_path"
  ./deploy/prepare-runtime.sh
  docker compose pull
  docker compose up -d --no-build --force-recreate
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

printf 'deploying %s to %s:%s\n' "$repo_url" "$deploy_host" "$deploy_path"
ssh "$deploy_host" "
set -eu
command -v git >/dev/null 2>&1 || { printf 'git is required on remote host\\n' >&2; exit 69; }
command -v docker >/dev/null 2>&1 || { printf 'docker is required on remote host\\n' >&2; exit 69; }
mkdir -p -- '$deploy_path'
if [ ! -e '$deploy_path/.git' ]; then
  git -C '$deploy_path' init
fi
if git -C '$deploy_path' remote get-url origin >/dev/null 2>&1; then
  git -C '$deploy_path' remote set-url origin '$repo_url'
else
  git -C '$deploy_path' remote add origin '$repo_url'
fi
git -C '$deploy_path' fetch --prune origin '$branch'
git -C '$deploy_path' checkout -B '$branch' 'origin/$branch'
git -C '$deploy_path' reset --hard 'origin/$branch'
test -f '$deploy_path/.env'
test -f '$deploy_path/config.yaml'
cd '$deploy_path'
./deploy/prepare-runtime.sh
docker compose pull
docker compose up -d --no-build --force-recreate
"
printf 'deployment complete\n'
