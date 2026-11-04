#!/bin/sh
set -eu

config=${ARIA2_CONFIG_PATH:-/config/aria2-next.conf}

set_value() {
  option=$1
  value=$2
  if grep -q "^${option}=" "$config"; then
    sed -i "s|^${option}=.*|${option}=${value}|" "$config"
  else
    printf '%s=%s\n' "$option" "$value" >> "$config"
  fi
}

touch "$config"

set_value rpc-listen-all true
set_value rpc-allow-origin-all true
set_value on-download-complete /hooks/aria2-complete.sh
set_value on-download-stop /hooks/aria2-stopped.sh

# When used as the container entrypoint, hand over to aria2-next.
if [ "$#" -gt 0 ]; then
  exec /usr/local/bin/aria2-next "$@"
fi
