#!/bin/sh
set -eu

template=${ARIA2_CONF_TEMPLATE:-/etc/aria2/aria2-next.conf}
config=${ARIA2_CONFIG_PATH:-/config/aria2-next.conf}

if [ ! -f "$config" ]; then
  if [ ! -f "$template" ]; then
    printf 'aria2 config template not found: %s\n' "$template" >&2
    exit 1
  fi
  cp "$template" "$config"
fi

# When used as the container entrypoint, hand over to aria2-next.
if [ "$#" -gt 0 ]; then
  exec /usr/local/bin/aria2-next "$@"
fi
