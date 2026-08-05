#!/bin/sh
set -eu

gid=${1:-}
case "$gid" in
  ""|*[![:alnum:]_-]*)
    exit 64
    ;;
esac

gateway_url=${GATEWAY_URL:-http://127.0.0.1:8787}
payload=$(printf '{"gid":"%s"}' "$gid")
if [ -n "${GATEWAY_API_TOKEN:-}" ]; then
  curl -fsS --max-time 5 --retry 3 --retry-delay 1 \
    -H "Authorization: Bearer $GATEWAY_API_TOKEN" \
    -H "Content-Type: application/json" \
    -d "$payload" "$gateway_url/api/v1/hooks/aria2/stopped"
else
  curl -fsS --max-time 5 --retry 3 --retry-delay 1 \
    -H "Content-Type: application/json" \
    -d "$payload" "$gateway_url/api/v1/hooks/aria2/stopped"
fi
