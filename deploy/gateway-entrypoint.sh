#!/bin/sh
set -eu

PUID=${PUID:-1000}
PGID=${PGID:-1000}
RCLONE_CONFIG=${RCLONE_CONFIG:-/rclone/rclone.conf}
HOME=/home/gateway
TMPDIR=/tmp
export PUID PGID RCLONE_CONFIG HOME TMPDIR

fail() {
    printf 'gateway entrypoint: %s\n' "$1" >&2
    exit 1
}

validate_id() {
    case "$2" in
        ''|*[!0-9]*)
            fail "$1 must be numeric"
            ;;
    esac
}

prepare_directories() {
    mkdir -p /data /downloads /rclone /home/gateway
    chown -R "$PUID:$PGID" /data /rclone
    chown "$PUID:$PGID" /downloads /home/gateway
}

fetch() {
    curl --fail --silent --show-error --location \
        --retry 3 --retry-delay 1 --connect-timeout 10 --max-time 120 "$@"
}

rclone_arch() {
    case "$(uname -m)" in
        x86_64|amd64)
            printf 'amd64'
            ;;
        aarch64|arm64)
            printf 'arm64'
            ;;
        armv7*)
            printf 'arm-v7'
            ;;
        armv6*)
            printf 'arm-v6'
            ;;
        i?86|x86)
            printf '386'
            ;;
        *)
            fail "unsupported rclone architecture: $(uname -m)"
            ;;
    esac
}

ensure_rclone() {
    current_version=
    if [ -x /usr/bin/rclone ]; then
        current_version=$(/usr/bin/rclone version 2>/dev/null | sed -n '1p' || true)
    fi

    latest_version=
    if ! latest_version=$(fetch https://downloads.rclone.org/version.txt); then
        if [ -n "$current_version" ]; then
            printf 'gateway entrypoint: unable to check rclone updates; using %s\n' "$current_version" >&2
            return
        fi
        fail 'unable to resolve the latest rclone version'
    fi

    case "$latest_version" in
        rclone\ v[0-9]*)
            version=${latest_version#rclone }
            ;;
        *)
            fail "unexpected rclone version response: $latest_version"
            ;;
    esac

    if [ "$current_version" = "$latest_version" ]; then
        return
    fi

    arch=$(rclone_arch)
    archive="rclone-${version}-linux-${arch}.zip"
    base_url="https://downloads.rclone.org/${version}"
    tmp_dir=$(mktemp -d /tmp/rclone-install.XXXXXX)
    cleanup() {
        rm -rf "$tmp_dir"
    }
    trap cleanup EXIT HUP INT TERM

    fetch -o "$tmp_dir/$archive" "$base_url/$archive"
    fetch -o "$tmp_dir/SHA256SUMS" "$base_url/SHA256SUMS"
    expected=$(awk -v archive="$archive" '$2 == archive { print $1; exit }' "$tmp_dir/SHA256SUMS")
    [ -n "$expected" ] || fail "missing rclone checksum for $archive"
    actual=$(sha256sum "$tmp_dir/$archive" | awk '{ print $1 }')
    [ "$actual" = "$expected" ] || fail "rclone checksum mismatch for $archive"

    mkdir "$tmp_dir/extracted"
    unzip -q "$tmp_dir/$archive" -d "$tmp_dir/extracted"
    rclone_path="$tmp_dir/extracted/rclone-${version}-linux-${arch}/rclone"
    [ -x "$rclone_path" ] || fail "rclone archive did not contain an executable"

    cp "$rclone_path" /usr/bin/rclone.new
    chmod 0755 /usr/bin/rclone.new
    chown 0:0 /usr/bin/rclone.new
    mv -f /usr/bin/rclone.new /usr/bin/rclone
    trap - EXIT HUP INT TERM
    cleanup
    printf 'gateway entrypoint: installed %s\n' "$latest_version"
}

validate_id PUID "$PUID"
validate_id PGID "$PGID"
prepare_directories
ensure_rclone
exec su-exec "$PUID:$PGID" /usr/local/bin/gateway "$@"
