#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
if [ -f "$project_root/.env" ]; then
  set -a
  . "$project_root/.env"
  set +a
fi

PUID=${PUID:-1000}
PGID=${PGID:-1000}
ARIA2_CONFIG_DIR=${ARIA2_CONFIG_DIR:-./runtime/aria2/config}
ARIA2_DOWNLOAD_DIR=${ARIA2_DOWNLOAD_DIR:-./runtime/aria2/downloads}
GATEWAY_DATA_DIR=${GATEWAY_DATA_DIR:-./runtime/gateway}
RCLONE_CONFIG_DIR=${RCLONE_CONFIG_DIR:-./runtime/rclone}

mkdir -p "$ARIA2_CONFIG_DIR" "$ARIA2_DOWNLOAD_DIR" "$GATEWAY_DATA_DIR" "$RCLONE_CONFIG_DIR"
ARIA2_CONFIG_FILE=$ARIA2_CONFIG_DIR/aria2.conf
if [ -f "$ARIA2_CONFIG_FILE" ]; then
  sed -i 's/^rpc-listen-all=.*/rpc-listen-all=false/' "$ARIA2_CONFIG_FILE"
fi
chown -R "$PUID:$PGID" "$ARIA2_CONFIG_DIR" "$ARIA2_DOWNLOAD_DIR" "$GATEWAY_DATA_DIR" "$RCLONE_CONFIG_DIR"
printf 'runtime directories prepared for %s:%s\n' "$PUID" "$PGID"
