#!/usr/bin/with-contenv bash
set -eu

config=/config/aria2.conf

set_hook() {
  option=$1
  hook=$2
  if [ -n "$(sed -n "s|^${option}=.*|configured|p" "$config")" ]; then
    sed -i "s|^${option}=.*|${option}=${hook}|" "$config"
  else
    printf '%s=%s\n' "$option" "$hook" >> "$config"
  fi
}

set_option() {
  option=$1
  value=$2
  if [ -n "$(sed -n "s|^${option}=.*|configured|p" "$config")" ]; then
    sed -i "s|^${option}=.*|${option}=${value}|" "$config"
  else
    printf '%s=%s\n' "$option" "$value" >> "$config"
  fi
}

set_option rpc-listen-all true
set_option rpc-allow-origin-all true

set_hook on-download-complete /hooks/aria2-complete.sh
set_hook on-download-stop /hooks/aria2-stopped.sh
