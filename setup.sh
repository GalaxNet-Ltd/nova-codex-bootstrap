#!/bin/sh
set -eu

APP_NAME="NovaScale Codex"
SERVICE_LABEL="dev.galaxnet.novascale.codex"
SERVICE_FILE="novascale-codex.service"
AGENT_SERVICE_LABEL="dev.galaxnet.novascale.agent"
AGENT_SERVICE_FILE="novascale-agent.service"
DEFAULT_PORT="14500"
DEFAULT_SCHEME="ws"
DEFAULT_AGENT_VERSION="0.1.3"
DEFAULT_NOTIFICATION_ENDPOINT="https://nova-push.galaxnet.dev"
AGENT_RELEASE_BASE_URL="https://github.com/GalaxNet-Ltd/nova-codex-bootstrap/releases/download"
CONFIG_DIR="${HOME}/.codex"
CONFIG_FILE="${CONFIG_DIR}/novascale-codex-host.env"
TOKEN_FILE="${CONFIG_DIR}/novascale-app-server-token"
HELPER_DIR="${HOME}/.local/bin"
HELPER_PATH="${HELPER_DIR}/novascale-codex"
AGENT_PATH="${HELPER_DIR}/novascale-agent"
AGENT_CONFIG_DIR="${HOME}/.config/novascale-agent"
AGENT_CONFIG_FILE="${AGENT_CONFIG_DIR}/config.json"
AGENT_STATE_DIR="${HOME}/.local/state/novascale-agent"
CODEX_HOOKS_FILE="${CONFIG_DIR}/hooks.json"
LEGACY_SERVICE_LABEL="dev.galaxnet.novaaccess.codex"
LEGACY_SERVICE_FILE="novaaccess-codex.service"
LEGACY_CONFIG_FILE="${CONFIG_DIR}/novaaccess-codex-host.env"
LEGACY_TOKEN_FILE="${CONFIG_DIR}/novaaccess-app-server-token"
LEGACY_HELPER_PATH="${HELPER_DIR}/novaaccess-codex"
codex_bin=""
codex_dir=""
agent_binary_source=""
dev_agent=0
agent_release_version="$DEFAULT_AGENT_VERSION"
agent_version_set=0
resolved_agent_binary=""
agent_download_dir=""
agent_incoming_version=""
agent_previous_binary_version=""
agent_previous_live_version=""
agent_binary_changed=0
agent_restart_required=0

listen=""
listen_set=0
port=""
host=""
name=""
rotate_token=0
no_start=0
print_only=0
no_qr=0
assume_yes=0
notification_requested=1
notification_setup_explicit=0
notification_disabled=0
notification_endpoint="$DEFAULT_NOTIFICATION_ENDPOINT"
notification_endpoint_set=0
no_hook_install=0

cleanup_agent_download() {
  [ -n "$agent_download_dir" ] || return 0
  [ -d "$agent_download_dir" ] || return 0
  [ -f "$agent_download_dir/.novascale-agent-download" ] || return 0
  rm -rf -- "$agent_download_dir"
}

trap cleanup_agent_download EXIT
trap 'exit 1' HUP INT TERM

usage() {
  cat <<'EOF'
NovaScale Codex host setup

Usage:
  setup.sh [options]

Options:
  --listen <addr>       Listen address. Defaults to a detected Tailscale-range IP.
  --port <port>         Listen port. Default: 14500
  --host <host>         Host value embedded in pairing URI.
  --name <name>         Display name. Default: system hostname.
  --rotate-token        Replace existing token.
  --no-start            Configure service but do not start it.
  --print-only          Do not configure service, only print current pairing info.
  --no-qr               Print the pairing URI without using qrencode.
  --yes                 Reconfigure an existing setup without prompting.
  --enable-notifications
                        Install and configure the notification agent. This is
                        the default.
  --notification-endpoint <url>
                        Notification backend URL. Default:
                        https://nova-push.galaxnet.dev
  --dev-agent           Install an unsigned local agent build from notifications/dist.
  --agent-binary <path> Install this prebuilt novascale-agent binary.
  --agent-version <ver> Download this pinned agent release. Default: 0.1.3.
  --no-hook-install     Install the agent without modifying Codex hooks.json.
  --no-notifications    Skip notification-agent setup and leave any existing
                        notification-agent installation unchanged.
  --help                Show this help.

When a 100.64.0.0/10 interface address is detected, the setup offers an
interactive menu and prefers binding only to that address. You can still pick
0.0.0.0 for hosts reachable through Tailscale subnet routing.
EOF
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

notice() {
  printf '%s\n' "$*"
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

find_codex_bin() {
  if command_exists codex; then
    command -v codex
    return 0
  fi

  for candidate in \
    "$HOME"/.local/bin/codex \
    "$HOME"/.npm-global/bin/codex \
    "$HOME"/bin/codex \
    /opt/homebrew/bin/codex \
    /usr/local/bin/codex
  do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done

  nvm_node_root="$HOME/.nvm/versions/node"
  if [ -d "$nvm_node_root" ]; then
    candidate="$(find "$nvm_node_root" -path '*/bin/codex' -print 2>/dev/null | head -n 1)"
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  fi

  return 1
}

has_tty() {
  [ -r /dev/tty ] && [ -w /dev/tty ]
}

os_name() {
  uname -s 2>/dev/null || printf 'unknown'
}

hostname_value() {
  hostname 2>/dev/null || uname -n 2>/dev/null || printf 'codex-host'
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

env_quote() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\$/\\$/g; s/`/\\`/g'
}

base64url_encode() {
  if base64 --help 2>&1 | grep -q -- '-w'; then
    base64 -w 0
  else
    base64 | tr -d '\n'
  fi | tr '+/' '-_' | tr -d '='
}

read_token() {
  [ -f "$TOKEN_FILE" ] || die "Token file does not exist: $TOKEN_FILE"
  tr -d '\r\n' < "$TOKEN_FILE"
}

read_notification_host_id() {
  [ -x "$AGENT_PATH" ] || return 1
  notification_host_id="$("$AGENT_PATH" host-id 2>/dev/null || true)"
  if [ -z "$notification_host_id" ]; then
    notification_host_id="$("$AGENT_PATH" status 2>/dev/null | awk '$1 == "Host" && $2 == "ID:" { print $3; exit }' || true)"
  fi
  printf '%s\n' "$notification_host_id" | grep -Eq '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$' || return 1
  printf '%s' "$notification_host_id"
}

make_pairing_uri() {
  token="$(read_token)"
  esc_name="$(json_escape "$name")"
  esc_host="$(json_escape "$host")"
  esc_token="$(json_escape "$token")"
  notification_field=""
  if notification_host_id="$(read_notification_host_id)"; then
    notification_field=",\"notification\":{\"hostId\":\"$(json_escape "$notification_host_id")\"}"
  fi
  payload="$(printf '{"type":"novascale-codex-host","version":1,"name":"%s","host":"%s","port":%s,"scheme":"%s","auth":{"mode":"capability-token","token":"%s"}%s}' "$esc_name" "$esc_host" "$port" "$DEFAULT_SCHEME" "$esc_token" "$notification_field" | base64url_encode)"
  printf 'novascale-codex://import?payload=%s\n' "$payload"
}

print_pairing() {
  pairing_uri="$(make_pairing_uri)"
  if [ "$no_qr" -eq 0 ] && command_exists qrencode; then
    qrencode -t ANSIUTF8 "$pairing_uri"
    printf '\nPairing URI:\n\n  %s\n' "$pairing_uri"
  else
    if [ "$no_qr" -eq 1 ]; then
      printf 'QR output skipped (--no-qr).\n\n'
    else
    cat <<EOF
QR tool not found.

To show a QR code, install qrencode:

  macOS:         brew install qrencode
  Debian/Ubuntu: sudo apt install qrencode
  Fedora:        sudo dnf install qrencode
  Arch:          sudo pacman -S qrencode

EOF
    fi
    cat <<EOF
Pairing URI:

  ${pairing_uri}

Copy this URI to your phone, then import it from inside the NovaScale app.
EOF
  fi
}

is_tailscale_cgnat_ip() {
  ip="$1"
  first="${ip%%.*}"
  rest="${ip#*.}"
  second="${rest%%.*}"
  [ "$first" = "100" ] || return 1
  [ "$second" -ge 64 ] 2>/dev/null && [ "$second" -le 127 ] 2>/dev/null
}

detect_tailscale_ip() {
  addrs=""
  if command_exists ip; then
    addrs="$(ip -o -4 addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 || true)"
  elif command_exists ifconfig; then
    addrs="$(ifconfig 2>/dev/null | awk '/inet / {print $2}' | sed 's/^addr://' || true)"
  fi

  for addr in $addrs; do
    if is_tailscale_cgnat_ip "$addr"; then
      printf '%s\n' "$addr"
      return 0
    fi
  done
  return 1
}

tty_read() {
  prompt="$1"
  default="$2"
  answer=""
  if has_tty; then
    printf '%s' "$prompt" > /dev/tty
    IFS= read -r answer < /dev/tty || answer=""
  fi
  [ -n "$answer" ] || answer="$default"
  printf '%s\n' "$answer"
}

service_path() {
  case "$(os_name)" in
    Darwin) printf '%s/Library/LaunchAgents/%s.plist\n' "$HOME" "$SERVICE_LABEL" ;;
    Linux) printf '%s/.config/systemd/user/%s\n' "$HOME" "$SERVICE_FILE" ;;
    *) return 1 ;;
  esac
}

agent_service_path() {
  case "$(os_name)" in
    Darwin) printf '%s/Library/LaunchAgents/%s.plist\n' "$HOME" "$AGENT_SERVICE_LABEL" ;;
    Linux) printf '%s/.config/systemd/user/%s\n' "$HOME" "$AGENT_SERVICE_FILE" ;;
    *) return 1 ;;
  esac
}

legacy_service_path() {
  case "$(os_name)" in
    Darwin) printf '%s/Library/LaunchAgents/%s.plist\n' "$HOME" "$LEGACY_SERVICE_LABEL" ;;
    Linux) printf '%s/.config/systemd/user/%s\n' "$HOME" "$LEGACY_SERVICE_FILE" ;;
    *) return 1 ;;
  esac
}

existing_setup_present() {
  service="$(service_path 2>/dev/null || true)"
  legacy_service="$(legacy_service_path 2>/dev/null || true)"
  [ -f "$CONFIG_FILE" ] && return 0
  [ -f "$LEGACY_CONFIG_FILE" ] && return 0
  [ -n "$service" ] && [ -f "$service" ] && return 0
  [ -n "$legacy_service" ] && [ -f "$legacy_service" ] && return 0
  [ -f "$HELPER_PATH" ] && return 0
  [ -f "$LEGACY_HELPER_PATH" ] && return 0
  [ -f "$TOKEN_FILE" ] && return 0
  [ -f "$LEGACY_TOKEN_FILE" ] && return 0
  [ -f "$AGENT_CONFIG_FILE" ] && return 0
  [ -x "$AGENT_PATH" ] && return 0
  agent_service="$(agent_service_path 2>/dev/null || true)"
  [ -n "$agent_service" ] && [ -f "$agent_service" ] && return 0
  return 1
}

stop_existing_service_quietly() {
  case "$(os_name)" in
    Darwin)
      plist="$(service_path)"
      legacy_plist="$(legacy_service_path)"
      launchctl stop "$SERVICE_LABEL" >/dev/null 2>&1 || true
      launchctl unload "$plist" >/dev/null 2>&1 || true
      launchctl stop "$LEGACY_SERVICE_LABEL" >/dev/null 2>&1 || true
      launchctl unload "$legacy_plist" >/dev/null 2>&1 || true
      ;;
    Linux)
      systemctl --user stop "$SERVICE_FILE" >/dev/null 2>&1 || true
      systemctl --user stop "$LEGACY_SERVICE_FILE" >/dev/null 2>&1 || true
      ;;
    *) ;;
  esac
}

cleanup_legacy_files() {
  legacy_service="$(legacy_service_path 2>/dev/null || true)"
  [ -n "$legacy_service" ] && rm -f "$legacy_service"
  rm -f "$LEGACY_HELPER_PATH" "$LEGACY_CONFIG_FILE" "$LEGACY_TOKEN_FILE"
}

confirm_reconfigure_existing_setup() {
  existing_setup_present || return 0

  if [ "$assume_yes" -eq 1 ]; then
    notice "Existing NovaScale Codex setup detected; reconfiguring because --yes was provided."
    stop_existing_service_quietly
    return 0
  fi

  if ! has_tty; then
    die "Existing setup detected. Rerun interactively or pass --yes to reconfigure."
  fi

  service="$(service_path 2>/dev/null || true)"
  cat > /dev/tty <<EOF

Existing NovaScale Codex setup detected.

EOF
  if [ -f "$CONFIG_FILE" ]; then
    (
      # shellcheck disable=SC1090
      . "$CONFIG_FILE"
      cat > /dev/tty <<EOF
Current config:
  Listen: ${NOVASCALE_CODEX_LISTEN:-unknown}:${NOVASCALE_CODEX_PORT:-unknown}
  Pairing host: ${NOVASCALE_CODEX_HOST:-unknown}:${NOVASCALE_CODEX_PORT:-unknown}
  Name: ${NOVASCALE_CODEX_NAME:-unknown}
  Config: ${CONFIG_FILE}

EOF
    )
  elif [ -f "$LEGACY_CONFIG_FILE" ]; then
    (
      # shellcheck disable=SC1090
      . "$LEGACY_CONFIG_FILE"
      cat > /dev/tty <<EOF
Current legacy NovaAccess config:
  Listen: ${NOVA_CODEX_LISTEN:-unknown}:${NOVA_CODEX_PORT:-unknown}
  Pairing host: ${NOVA_CODEX_HOST:-unknown}:${NOVA_CODEX_PORT:-unknown}
  Name: ${NOVA_CODEX_NAME:-unknown}
  Config: ${LEGACY_CONFIG_FILE}

EOF
    )
  fi
  if [ -n "$service" ] && [ -f "$service" ]; then
    printf 'Service: %s\n\n' "$service" > /dev/tty
  fi
  legacy_service="$(legacy_service_path 2>/dev/null || true)"
  if [ -n "$legacy_service" ] && [ -f "$legacy_service" ]; then
    printf 'Legacy service: %s\n\n' "$legacy_service" > /dev/tty
  fi

  answer="$(tty_read "Reconfigure existing setup? [y/N]: " "n")"
  case "$answer" in
    y|Y|yes|YES)
      stop_existing_service_quietly
      ;;
    *)
      notice "Leaving existing setup unchanged."
      notice "Use ${HELPER_PATH} print-pairing to show the current pairing URI."
      exit 0
      ;;
  esac
}

choose_listen_interactively() {
  tailscale_ip="$1"
  if ! has_tty; then
    listen="$tailscale_ip"
    host="${host:-$tailscale_ip}"
    return
  fi

  cat > /dev/tty <<EOF

Detected a Tailscale-range address: ${tailscale_ip}

Choose how ${APP_NAME} should listen:

  1) ${tailscale_ip} only (recommended for direct Tailscale access)
  2) 0.0.0.0 (behind a Tailscale subnet router)
  3) Custom address

EOF
  choice="$(tty_read "Select [1]: " "1")"
  case "$choice" in
    1|"")
      listen="$tailscale_ip"
      host="${host:-$tailscale_ip}"
      ;;
    2)
      listen="0.0.0.0"
      if [ -z "$host" ]; then
        host="$(tty_read "Reachable host/IP for NovaScale: " "")"
        [ -n "$host" ] || die "--host is required when choosing 0.0.0.0 interactively"
      fi
      ;;
    3)
      listen="$(tty_read "Listen address: " "$tailscale_ip")"
      host="${host:-$listen}"
      ;;
    *)
      die "Unknown selection: $choice"
      ;;
  esac
}

warn_network_listen() {
  if [ "$listen" = "0.0.0.0" ]; then
    cat <<'EOF'

WARNING: This exposes Codex app-server on the network.

Only use this on a trusted private network or behind Tailscale subnet routing.
A capability token is required, but network exposure still increases risk.

EOF
  fi
}

validate_port() {
  case "$port" in
    *[!0-9]*|"") die "Invalid port: $port" ;;
  esac
  [ "$port" -ge 1 ] 2>/dev/null && [ "$port" -le 65535 ] 2>/dev/null || die "Invalid port: $port"
}

check_port_free() {
  if command_exists lsof; then
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      die "Port $port is already in use. Use --port <another-port>."
    fi
  elif command_exists ss; then
    if ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]${port}$"; then
      die "Port $port is already in use. Use --port <another-port>."
    fi
  fi
}

ensure_token() {
  mkdir -p "$CONFIG_DIR"
  umask 077
  if [ ! -s "$TOKEN_FILE" ] && [ -s "$LEGACY_TOKEN_FILE" ] && [ "$rotate_token" -eq 0 ]; then
    cp "$LEGACY_TOKEN_FILE" "$TOKEN_FILE"
  fi
  if [ "$rotate_token" -eq 1 ] || [ ! -s "$TOKEN_FILE" ]; then
    if command_exists openssl; then
      openssl rand -base64 32 > "$TOKEN_FILE"
    else
      head -c 32 /dev/urandom | base64 > "$TOKEN_FILE"
    fi
  fi
  chmod 600 "$TOKEN_FILE"
}

write_config() {
  mkdir -p "$CONFIG_DIR"
  tmp="${CONFIG_FILE}.$$"
  {
    printf 'NOVASCALE_CODEX_LISTEN="%s"\n' "$(env_quote "$listen")"
    printf 'NOVASCALE_CODEX_PORT="%s"\n' "$(env_quote "$port")"
    printf 'NOVASCALE_CODEX_HOST="%s"\n' "$(env_quote "$host")"
    printf 'NOVASCALE_CODEX_NAME="%s"\n' "$(env_quote "$name")"
    printf 'NOVASCALE_CODEX_SCHEME="%s"\n' "$(env_quote "$DEFAULT_SCHEME")"
    printf 'NOVASCALE_CODEX_TOKEN_FILE="%s"\n' "$(env_quote "$TOKEN_FILE")"
    printf 'NOVASCALE_CODEX_BIN="%s"\n' "$(env_quote "$codex_bin")"
    printf 'NOVASCALE_CODEX_BIN_DIR="%s"\n' "$(env_quote "$codex_dir")"
    printf 'NOVASCALE_CODEX_NO_QR="%s"\n' "$(env_quote "$no_qr")"
  } > "$tmp"
  chmod 600 "$tmp"
  mv "$tmp" "$CONFIG_FILE"
}

install_helper() {
  mkdir -p "$HELPER_DIR"
  src_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
  if [ -f "${src_dir}/bin/novascale-codex" ]; then
    cp "${src_dir}/bin/novascale-codex" "$HELPER_PATH"
  else
    write_embedded_helper "$HELPER_PATH"
  fi
  chmod 755 "$HELPER_PATH"
  case ":$PATH:" in
    *":$HELPER_DIR:"*) ;;
    *) notice "Note: $HELPER_DIR is not in PATH. Run helper as $HELPER_PATH or add it to PATH." ;;
  esac
}

agent_platform() {
  agent_os="$(os_name)"
  agent_arch="$(uname -m 2>/dev/null || true)"
  case "$agent_os" in
    Darwin) agent_os=darwin ;;
    Linux) agent_os=linux ;;
    *) return 1 ;;
  esac
  case "$agent_arch" in
    arm64|aarch64) agent_arch=arm64 ;;
    x86_64|amd64) agent_arch=amd64 ;;
    *) return 1 ;;
  esac
  printf '%s-%s\n' "$agent_os" "$agent_arch"
}

validate_agent_release_version() {
  case "$agent_release_version" in
    ''|*[!0-9A-Za-z._-]*) die "Invalid notification-agent release version" ;;
  esac
}

download_file() {
  source_url="$1"
  destination="$2"
  case "$source_url" in
    https://*) ;;
    *) die "Refusing non-HTTPS notification-agent download" ;;
  esac
  if command_exists curl; then
    curl --fail --location --silent --show-error \
      --proto '=https' --proto-redir '=https' --tlsv1.2 \
      --retry 3 --connect-timeout 15 \
      --output "$destination" "$source_url"
  elif command_exists wget; then
    wget --quiet --https-only --secure-protocol=TLSv1_2 \
      --output-document="$destination" "$source_url"
  else
    die "curl or wget is required to download the notification agent"
  fi
}

calculate_sha256() {
  file_path="$1"
  if command_exists shasum; then
    shasum -a 256 "$file_path" | awk '{print tolower($1)}'
  elif command_exists sha256sum; then
    sha256sum "$file_path" | awk '{print tolower($1)}'
  else
    die "shasum or sha256sum is required to verify the notification agent"
  fi
}

validate_archive_entries() {
  awk '
    {
      sub(/\r$/, "")
      count++
      if ($0 ~ /^\// || $0 ~ /(^|\/)\.\.($|\/)/) unsafe = 1
      if ($0 != "novascale-agent" && $0 != "LICENSE" && $0 !~ /^THIRD_PARTY_LICENSES\//) unsafe = 1
    }
    END { exit (unsafe || count == 0) ? 1 : 0 }
  '
}

download_agent_release() {
  validate_agent_release_version
  platform="$(agent_platform || true)"
  [ -n "$platform" ] || die "Unsupported notification-agent platform"
  agent_os="${platform%-*}"
  agent_arch="${platform#*-}"
  case "$agent_os" in
    darwin) archive_name="novascale-agent_${agent_release_version}_darwin_universal.zip" ;;
    linux) archive_name="novascale-agent_${agent_release_version}_linux_${agent_arch}.tar.gz" ;;
    *) die "Unsupported notification-agent platform: $platform" ;;
  esac

  if [ -z "$agent_download_dir" ]; then
    agent_download_dir="$(mktemp -d "${TMPDIR:-/tmp}/novascale-agent-download.XXXXXX")"
    chmod 700 "$agent_download_dir"
    : >"${agent_download_dir}/.novascale-agent-download"
  fi
  archive_path="${agent_download_dir}/${archive_name}"
  checksums_path="${agent_download_dir}/SHA256SUMS"
  package_dir="${agent_download_dir}/package"
  release_url="${AGENT_RELEASE_BASE_URL}/agent-v${agent_release_version}"

  notice "Downloading signed NovaScale notification agent ${agent_release_version} for ${platform}."
  download_file "${release_url}/SHA256SUMS" "$checksums_path"
  download_file "${release_url}/${archive_name}" "$archive_path"
  checksums_size="$(wc -c <"$checksums_path" | tr -d ' ')"
  archive_size="$(wc -c <"$archive_path" | tr -d ' ')"
  [ "$checksums_size" -le 65536 ] 2>/dev/null || die "Release checksum file is unexpectedly large"
  [ "$archive_size" -le 104857600 ] 2>/dev/null || die "Notification-agent archive is unexpectedly large"

  expected_hash="$(awk -v target="$archive_name" '
    {
      name = $2
      sub(/^\*/, "", name)
      if (name == target) { count++; hash = tolower($1) }
    }
    END { if (count == 1) print hash }
  ' "$checksums_path")"
  case "$expected_hash" in
    *[!0-9a-f]*|'') die "Release checksum entry is missing or malformed for ${archive_name}" ;;
  esac
  [ "${#expected_hash}" -eq 64 ] || die "Release checksum entry has an invalid length for ${archive_name}"
  actual_hash="$(calculate_sha256 "$archive_path")"
  [ "$actual_hash" = "$expected_hash" ] || die "Notification-agent release checksum mismatch"

  mkdir -p "$package_dir"
  case "$agent_os" in
    darwin)
      command_exists zipinfo || die "zipinfo is required to inspect the macOS notification-agent archive"
      zipinfo -1 "$archive_path" | validate_archive_entries || die "Notification-agent archive contains an unsafe path"
      if zipinfo -l "$archive_path" | awk '$1 ~ /^l/ { found = 1 } END { exit found ? 0 : 1 }'; then
        die "Notification-agent archive contains a symbolic link"
      fi
      if command_exists ditto; then
        ditto -x -k "$archive_path" "$package_dir"
      elif command_exists unzip; then
        unzip -q "$archive_path" -d "$package_dir"
      else
        die "ditto or unzip is required to install the macOS notification agent"
      fi
      ;;
    linux)
      command_exists tar || die "tar is required to install the Linux notification agent"
      tar -tzf "$archive_path" | validate_archive_entries || die "Notification-agent archive contains an unsafe path"
      if tar -tvzf "$archive_path" | awk '$1 ~ /^l/ || / link to / { found = 1 } END { exit found ? 0 : 1 }'; then
        die "Notification-agent archive contains a link"
      fi
      tar -xzf "$archive_path" -C "$package_dir"
      ;;
  esac

  candidate="${package_dir}/novascale-agent"
  [ -f "$candidate" ] && [ ! -L "$candidate" ] || die "Notification-agent archive is missing its regular executable"
  chmod 755 "$candidate"
  if [ "$agent_os" = "darwin" ]; then
    command_exists codesign || die "codesign is required to verify the macOS notification agent"
    codesign --verify --strict --verbose=2 "$candidate"
    codesign -vvvv -R="notarized" --check-notarization "$candidate"
    command_exists lipo || die "lipo is required to verify the universal macOS notification agent"
    lipo "$candidate" -verify_arch arm64
    lipo "$candidate" -verify_arch x86_64
  fi
  downloaded_version="$("$candidate" version 2>/dev/null || true)"
  [ "$downloaded_version" = "$agent_release_version" ] || die "Downloaded notification-agent version does not match ${agent_release_version}"
  resolved_agent_binary="$candidate"
}

find_agent_binary() {
  resolved_agent_binary=""
  if [ -n "$agent_binary_source" ]; then
    [ -x "$agent_binary_source" ] || die "Notification agent binary is not executable: $agent_binary_source"
    resolved_agent_binary="$agent_binary_source"
    return 0
  fi

  if [ "$dev_agent" -eq 1 ]; then
    setup_source_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
    platform="$(agent_platform || true)"
    [ -n "$platform" ] || return 1
    for candidate in \
      "${setup_source_dir}/notifications/dist/${platform}/novascale-agent" \
      "${setup_source_dir}/dist/${platform}/novascale-agent"
    do
      if [ -x "$candidate" ]; then
        resolved_agent_binary="$candidate"
        return 0
      fi
    done
    die "--dev-agent was provided, but no local agent build exists for ${platform}"
  fi
  if [ -x "$AGENT_PATH" ]; then
    installed_agent_version="$("$AGENT_PATH" version 2>/dev/null || true)"
    if [ "$installed_agent_version" = "$agent_release_version" ]; then
      resolved_agent_binary="$AGENT_PATH"
      return 0
    fi
  fi
  download_agent_release
}

install_agent_binary() {
  source_path="$1"
  mkdir -p "$HELPER_DIR"
  if [ "$source_path" != "$AGENT_PATH" ]; then
    temporary_agent="${AGENT_PATH}.tmp.$$"
    cp "$source_path" "$temporary_agent"
    chmod 755 "$temporary_agent"
    mv "$temporary_agent" "$AGENT_PATH"
  else
    chmod 755 "$AGENT_PATH"
  fi
  "$AGENT_PATH" version >/dev/null
}

prepare_agent_upgrade() {
  source_path="$1"
  agent_incoming_version="$("$source_path" version 2>/dev/null || true)"
  [ -n "$agent_incoming_version" ] || die "Notification agent did not report its version: $source_path"
  agent_previous_binary_version=""
  agent_previous_live_version=""
  agent_binary_changed=0
  agent_restart_required=0

  if [ -x "$AGENT_PATH" ]; then
    agent_previous_binary_version="$("$AGENT_PATH" version 2>/dev/null || true)"
    agent_previous_live_version="$("$AGENT_PATH" daemon-version 2>/dev/null || true)"
    if [ "$source_path" != "$AGENT_PATH" ] && ! cmp -s "$source_path" "$AGENT_PATH"; then
      agent_binary_changed=1
    fi
  fi

  if [ "$agent_binary_changed" -eq 1 ]; then
    agent_restart_required=1
  elif [ -n "$agent_previous_live_version" ]; then
    if [ "$agent_previous_live_version" != "$agent_incoming_version" ]; then
      agent_restart_required=1
    fi
  elif [ -S "${AGENT_STATE_DIR}/agent.sock" ]; then
    # Agents released before live-version reporting require one upgrade
    # restart so future setup runs can compare the actual daemon version.
    agent_restart_required=1
  elif [ -n "$agent_previous_binary_version" ] && \
       [ "$agent_previous_binary_version" != "$agent_incoming_version" ]; then
    agent_restart_required=1
  fi
}

stage_notification_agent_enrollment() {
  if [ -f "$AGENT_CONFIG_FILE" ]; then
    registration_state="$("$AGENT_PATH" registration-state)"
    current_notification_endpoint="$("$AGENT_PATH" endpoint)"
    if [ "$notification_endpoint_set" -eq 0 ]; then
      notification_endpoint="$current_notification_endpoint"
    elif [ "${current_notification_endpoint%/}" != "${notification_endpoint%/}" ]; then
      notice "Existing notification backend differs from the requested backend; preserving it."
      notice "Run novascale-agent switch-backend explicitly before using a different notification backend."
      return 0
    fi
    if [ "$registration_state" = "active" ]; then
      notice "Existing active notification-agent identity detected; preserving its host ID and private key."
      return 0
    fi
    "$AGENT_PATH" init --endpoint "$notification_endpoint"
    return 0
  fi
  "$AGENT_PATH" init --endpoint "$notification_endpoint"
}

install_agent_hooks() {
  if [ "$no_hook_install" -eq 1 ]; then
    notice "Skipping Codex hook installation because --no-hook-install was provided."
    return 0
  fi
  "$AGENT_PATH" hooks install --agent-path "$AGENT_PATH" --hooks-file "$CODEX_HOOKS_FILE"
}

migrate_development_agent_hooks() {
  source_path="$1"
  [ "$source_path" != "$AGENT_PATH" ] || return 0
  "$AGENT_PATH" hooks uninstall --agent-path "$source_path" --hooks-file "$CODEX_HOOKS_FILE" >/dev/null
}

install_macos_agent_service() {
  launch_dir="${HOME}/Library/LaunchAgents"
  plist="${launch_dir}/${AGENT_SERVICE_LABEL}.plist"
  mkdir -p "$launch_dir" "$AGENT_STATE_DIR"
  chmod 700 "$AGENT_STATE_DIR"
  cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>${AGENT_SERVICE_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
      <string>${AGENT_PATH}</string>
      <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>StandardOutPath</key>
    <string>${AGENT_STATE_DIR}/agent.log</string>
    <key>StandardErrorPath</key>
    <string>${AGENT_STATE_DIR}/agent.err.log</string>
  </dict>
</plist>
EOF
  chmod 600 "$plist"
  if [ "$no_start" -eq 0 ]; then
    if [ "$agent_restart_required" -eq 1 ]; then
      notice "Notification agent ${agent_previous_live_version:-unknown} -> ${agent_incoming_version}; restarting its daemon now that setup is complete."
      launchctl unload "$plist" >/dev/null 2>&1 || true
      launchctl load "$plist"
      launchctl start "$AGENT_SERVICE_LABEL" || true
    elif ! launchctl print "gui/$(id -u)/${AGENT_SERVICE_LABEL}" >/dev/null 2>&1; then
      launchctl load "$plist"
      launchctl start "$AGENT_SERVICE_LABEL" || true
    fi
  elif [ "$agent_restart_required" -eq 1 ]; then
    notice "Notification agent update installed; --no-start left the existing daemon unchanged until its next start."
  fi
}

install_linux_agent_service() {
  systemd_dir="${HOME}/.config/systemd/user"
  service="${systemd_dir}/${AGENT_SERVICE_FILE}"
  mkdir -p "$systemd_dir" "$AGENT_STATE_DIR"
  chmod 700 "$AGENT_STATE_DIR"
  cat > "$service" <<EOF
[Unit]
Description=NovaScale Notification Agent
After=network-online.target

[Service]
Type=simple
ExecStart=${AGENT_PATH} serve
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF
  chmod 600 "$service"
  systemctl --user daemon-reload
  if [ "$no_start" -eq 0 ]; then
    systemctl --user enable "$AGENT_SERVICE_FILE" >/dev/null
    if [ "$agent_restart_required" -eq 1 ]; then
      notice "Notification agent ${agent_previous_live_version:-unknown} -> ${agent_incoming_version}; restarting its daemon now that setup is complete."
      systemctl --user restart "$AGENT_SERVICE_FILE"
    else
      systemctl --user start "$AGENT_SERVICE_FILE"
    fi
  elif [ "$agent_restart_required" -eq 1 ]; then
    notice "Notification agent update installed; --no-start left the existing daemon unchanged until its next start."
  fi
}

install_agent_service() {
  case "$(os_name)" in
    Darwin) install_macos_agent_service ;;
    Linux) install_linux_agent_service ;;
    *) die "Unsupported OS: $(os_name). macOS and Linux are supported." ;;
  esac
}

configure_notification_agent() {
  [ "$notification_disabled" -eq 0 ] || return 0
  if [ "$notification_requested" -eq 0 ] && [ ! -f "$AGENT_CONFIG_FILE" ]; then
    return 0
  fi
  if [ -z "$resolved_agent_binary" ]; then
    find_agent_binary
  fi
  source_path="$resolved_agent_binary"
  [ -n "$source_path" ] || die "Notification-agent release could not be resolved"
  if [ "$dev_agent" -eq 1 ] || [ -n "$agent_binary_source" ]; then
    notice "Development mode: installing an unsigned local notification-agent binary."
  fi
  prepare_agent_upgrade "$source_path"
  install_agent_binary "$source_path"
  stage_notification_agent_enrollment
  migrate_development_agent_hooks "$source_path"
  install_agent_hooks
  install_agent_service
  if [ "$no_start" -eq 1 ]; then
    notice "Notification enrollment is staged; it will begin when the agent daemon starts."
  elif [ "$("$AGENT_PATH" registration-state)" != "active" ]; then
    notice "Notification enrollment is running in the agent daemon and will retry automatically."
  fi
}

preflight_notification_agent() {
  [ "$notification_disabled" -eq 0 ] || return 0
  if [ "$notification_requested" -eq 0 ] && [ ! -f "$AGENT_CONFIG_FILE" ]; then
    return 0
  fi
  find_agent_binary
  source_path="$resolved_agent_binary"
  [ -n "$source_path" ] || die "Notification-agent release could not be resolved"
}

write_embedded_helper() {
  helper_target="$1"
  cat > "$helper_target" <<'NOVASCALE_CODEX_HELPER'
#!/bin/sh
set -eu

SERVICE_LABEL="dev.galaxnet.novascale.codex"
SERVICE_FILE="novascale-codex.service"
AGENT_SERVICE_LABEL="dev.galaxnet.novascale.agent"
AGENT_SERVICE_FILE="novascale-agent.service"
CONFIG_DIR="${HOME}/.codex"
CONFIG_FILE="${CONFIG_DIR}/novascale-codex-host.env"
TOKEN_FILE_DEFAULT="${CONFIG_DIR}/novascale-app-server-token"
HELPER_PATH="${HOME}/.local/bin/novascale-codex"
AGENT_PATH="${HOME}/.local/bin/novascale-agent"
AGENT_CONFIG_DIR="${HOME}/.config/novascale-agent"
AGENT_STATE_DIR="${HOME}/.local/state/novascale-agent"
CODEX_HOOKS_FILE="${HOME}/.codex/hooks.json"
LEGACY_SERVICE_LABEL="dev.galaxnet.novaaccess.codex"
LEGACY_SERVICE_FILE="novaaccess-codex.service"
LEGACY_CONFIG_FILE="${CONFIG_DIR}/novaaccess-codex-host.env"
LEGACY_TOKEN_FILE="${CONFIG_DIR}/novaaccess-app-server-token"
LEGACY_HELPER_PATH="${HOME}/.local/bin/novaaccess-codex"
DEFAULT_SCHEME="ws"
no_qr=0

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

has_tty() {
  [ -r /dev/tty ] && [ -w /dev/tty ]
}

os_name() {
  uname -s 2>/dev/null || printf 'unknown'
}

load_config() {
  [ -f "$CONFIG_FILE" ] || die "Config file not found: $CONFIG_FILE"
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  listen="${NOVASCALE_CODEX_LISTEN:-}"
  port="${NOVASCALE_CODEX_PORT:-14500}"
  host="${NOVASCALE_CODEX_HOST:-$listen}"
  name="${NOVASCALE_CODEX_NAME:-$(hostname 2>/dev/null || printf 'codex-host')}"
  scheme="${NOVASCALE_CODEX_SCHEME:-$DEFAULT_SCHEME}"
  token_file="${NOVASCALE_CODEX_TOKEN_FILE:-$TOKEN_FILE_DEFAULT}"
  config_no_qr="${NOVASCALE_CODEX_NO_QR:-0}"
  [ "$no_qr" -eq 1 ] || no_qr="$config_no_qr"
  [ -n "$listen" ] || die "Config does not include NOVASCALE_CODEX_LISTEN. Rerun setup.sh with --listen <tailscale-ip> or --listen 0.0.0.0 --host <reachable-host>."
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

base64url_encode() {
  if base64 --help 2>&1 | grep -q -- '-w'; then
    base64 -w 0
  else
    base64 | tr -d '\n'
  fi | tr '+/' '-_' | tr -d '='
}

read_token() {
  [ -f "$token_file" ] || die "Token file does not exist: $token_file"
  tr -d '\r\n' < "$token_file"
}

read_notification_host_id() {
  [ -x "$AGENT_PATH" ] || return 1
  notification_host_id="$("$AGENT_PATH" host-id 2>/dev/null || true)"
  if [ -z "$notification_host_id" ]; then
    notification_host_id="$("$AGENT_PATH" status 2>/dev/null | awk '$1 == "Host" && $2 == "ID:" { print $3; exit }' || true)"
  fi
  printf '%s\n' "$notification_host_id" | grep -Eq '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$' || return 1
  printf '%s' "$notification_host_id"
}

make_pairing_uri() {
  token="$(read_token)"
  esc_name="$(json_escape "$name")"
  esc_host="$(json_escape "$host")"
  esc_token="$(json_escape "$token")"
  notification_field=""
  if notification_host_id="$(read_notification_host_id)"; then
    notification_field=",\"notification\":{\"hostId\":\"$(json_escape "$notification_host_id")\"}"
  fi
  payload="$(printf '{"type":"novascale-codex-host","version":1,"name":"%s","host":"%s","port":%s,"scheme":"%s","auth":{"mode":"capability-token","token":"%s"}%s}' "$esc_name" "$esc_host" "$port" "$scheme" "$esc_token" "$notification_field" | base64url_encode)"
  printf 'novascale-codex://import?payload=%s\n' "$payload"
}

print_pairing() {
  load_config
  pairing_uri="$(make_pairing_uri)"
  if [ "$no_qr" -eq 0 ] && command_exists qrencode; then
    qrencode -t ANSIUTF8 "$pairing_uri"
    printf '\nPairing URI:\n\n  %s\n' "$pairing_uri"
  else
    if [ "$no_qr" -eq 1 ]; then
      printf 'QR output skipped (--no-qr).\n\n'
    else
    cat <<EOF
QR tool not found.

EOF
    fi
    cat <<EOF
Pairing URI:

  ${pairing_uri}

Copy this URI to your phone, then import it from inside the NovaScale app.
EOF
  fi
}

restart_service() {
  case "$(os_name)" in
    Darwin)
      plist="${HOME}/Library/LaunchAgents/${SERVICE_LABEL}.plist"
      launchctl unload "$plist" >/dev/null 2>&1 || true
      launchctl load "$plist"
      launchctl start "$SERVICE_LABEL" || true
      ;;
    Linux)
      systemctl --user daemon-reload
      systemctl --user restart "$SERVICE_FILE"
      ;;
    *) die "Unsupported OS: $(os_name)" ;;
  esac
}

stop_service() {
  case "$(os_name)" in
    Darwin)
      launchctl stop "$SERVICE_LABEL" >/dev/null 2>&1 || true
      launchctl unload "${HOME}/Library/LaunchAgents/${SERVICE_LABEL}.plist" >/dev/null 2>&1 || true
      launchctl stop "$LEGACY_SERVICE_LABEL" >/dev/null 2>&1 || true
      launchctl unload "${HOME}/Library/LaunchAgents/${LEGACY_SERVICE_LABEL}.plist" >/dev/null 2>&1 || true
      ;;
    Linux)
      systemctl --user stop "$SERVICE_FILE"
      systemctl --user stop "$LEGACY_SERVICE_FILE" >/dev/null 2>&1 || true
      ;;
    *) die "Unsupported OS: $(os_name)" ;;
  esac
}

status_service() {
  case "$(os_name)" in
    Darwin) launchctl print "gui/$(id -u)/${SERVICE_LABEL}" ;;
    Linux) systemctl --user status "$SERVICE_FILE" ;;
    *) die "Unsupported OS: $(os_name)" ;;
  esac
}

app_server_update_status() {
  [ -x "$AGENT_PATH" ] || die "Conditional Codex update checks require novascale-agent; no service was restarted."
  "$AGENT_PATH" app-server update-status
}

restart_service_if_updated() {
  [ -x "$AGENT_PATH" ] || die "Conditional Codex update checks require novascale-agent; no service was restarted."
  "$AGENT_PATH" app-server restart-if-updated
}

restart_notification_service() {
  [ -x "$AGENT_PATH" ] || die "Notification agent is not installed: $AGENT_PATH"
  case "$(os_name)" in
    Darwin)
      plist="${HOME}/Library/LaunchAgents/${AGENT_SERVICE_LABEL}.plist"
      [ -f "$plist" ] || die "Notification LaunchAgent not found: $plist"
      launchctl unload "$plist" >/dev/null 2>&1 || true
      launchctl load "$plist"
      launchctl start "$AGENT_SERVICE_LABEL" || true
      ;;
    Linux)
      systemctl --user daemon-reload
      systemctl --user restart "$AGENT_SERVICE_FILE"
      ;;
    *) die "Unsupported OS: $(os_name)" ;;
  esac
}

stop_notification_service() {
  case "$(os_name)" in
    Darwin)
      launchctl stop "$AGENT_SERVICE_LABEL" >/dev/null 2>&1 || true
      launchctl unload "${HOME}/Library/LaunchAgents/${AGENT_SERVICE_LABEL}.plist" >/dev/null 2>&1 || true
      ;;
    Linux) systemctl --user stop "$AGENT_SERVICE_FILE" >/dev/null 2>&1 || true ;;
    *) die "Unsupported OS: $(os_name)" ;;
  esac
}

status_notification_service() {
  case "$(os_name)" in
    Darwin) launchctl print "gui/$(id -u)/${AGENT_SERVICE_LABEL}" ;;
    Linux) systemctl --user status "$AGENT_SERVICE_FILE" ;;
    *) die "Unsupported OS: $(os_name)" ;;
  esac
}

rotate_token() {
  load_config
  mkdir -p "$(dirname "$token_file")"
  umask 077
  if command_exists openssl; then
    openssl rand -base64 32 > "$token_file"
  else
    head -c 32 /dev/urandom | base64 > "$token_file"
  fi
  chmod 600 "$token_file"
  restart_service
  print_pairing
}

confirm_delete_token() {
  has_tty || return 1
  printf 'Delete NovaScale Codex token and config? [y/N] ' > /dev/tty
  IFS= read -r answer < /dev/tty || answer=""
  case "$answer" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

uninstall() {
  delete_token=ask
  delete_agent_state=no
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --keep-token) delete_token=no ;;
      --delete-token) delete_token=yes ;;
      --keep-agent-state) delete_agent_state=no ;;
      --delete-agent-state) delete_agent_state=yes ;;
      *) die "Unknown uninstall option: $1" ;;
    esac
    shift
  done

  stop_service
  stop_notification_service
  if [ -x "$AGENT_PATH" ]; then
    "$AGENT_PATH" hooks uninstall --agent-path "$AGENT_PATH" --hooks-file "$CODEX_HOOKS_FILE" >/dev/null 2>&1 || \
      printf 'WARNING: Could not remove NovaScale Codex hooks automatically. Review %s manually.\n' "$CODEX_HOOKS_FILE" >&2
  elif [ -f "$CODEX_HOOKS_FILE" ]; then
    printf 'WARNING: Notification agent is missing. Review %s for a stale NovaScale hook command.\n' "$CODEX_HOOKS_FILE" >&2
  fi
  case "$(os_name)" in
    Darwin)
      rm -f "${HOME}/Library/LaunchAgents/${SERVICE_LABEL}.plist"
      rm -f "${HOME}/Library/LaunchAgents/${AGENT_SERVICE_LABEL}.plist"
      ;;
    Linux)
      systemctl --user disable "$SERVICE_FILE" >/dev/null 2>&1 || true
      systemctl --user disable "$AGENT_SERVICE_FILE" >/dev/null 2>&1 || true
      rm -f "${HOME}/.config/systemd/user/${SERVICE_FILE}"
      rm -f "${HOME}/.config/systemd/user/${AGENT_SERVICE_FILE}"
      rm -f "${HOME}/.config/systemd/user/${LEGACY_SERVICE_FILE}"
      systemctl --user daemon-reload >/dev/null 2>&1 || true
      ;;
  esac
  rm -f "${HOME}/Library/LaunchAgents/${LEGACY_SERVICE_LABEL}.plist"

  if [ "$delete_token" = "ask" ]; then
    if confirm_delete_token; then
      delete_token=yes
    else
      delete_token=no
    fi
  fi

  if [ "$delete_token" = "yes" ]; then
    rm -f "$CONFIG_FILE" "$TOKEN_FILE_DEFAULT" "$LEGACY_CONFIG_FILE" "$LEGACY_TOKEN_FILE"
  fi
  if [ "$delete_agent_state" = "yes" ]; then
    rm -rf "$AGENT_CONFIG_DIR" "$AGENT_STATE_DIR"
  fi
  rm -f "$AGENT_PATH" "$HELPER_PATH" "$LEGACY_HELPER_PATH"
  printf 'Uninstalled NovaScale Codex host service.\n'
}

usage() {
  cat <<'EOF'
Usage:
  novascale-codex status
  novascale-codex update-status
  novascale-codex restart-if-updated
  novascale-codex restart
  novascale-codex stop
  novascale-codex notification-status
  novascale-codex notification-restart
  novascale-codex notification-stop
  novascale-codex print-pairing [--no-qr]
  novascale-codex rotate-token
  novascale-codex uninstall [--keep-token|--delete-token] [--keep-agent-state|--delete-agent-state]
EOF
}

cmd="${1:-}"
[ -n "$cmd" ] || {
  usage
  exit 1
}
shift || true

case "$cmd" in
  status) status_service ;;
  update-status) app_server_update_status ;;
  restart-if-updated) restart_service_if_updated ;;
  restart) restart_service ;;
  stop) stop_service ;;
  notification-status) status_notification_service ;;
  notification-restart) restart_notification_service ;;
  notification-stop) stop_notification_service ;;
  print-pairing)
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --no-qr) no_qr=1 ;;
        *) die "Unknown print-pairing option: $1" ;;
      esac
      shift
    done
    print_pairing
    ;;
  rotate-token) rotate_token ;;
  uninstall) uninstall "$@" ;;
  --help|-h|help) usage ;;
  *) die "Unknown command: $cmd" ;;
esac
NOVASCALE_CODEX_HELPER
}

install_macos_service() {
  launch_dir="${HOME}/Library/LaunchAgents"
  plist="${launch_dir}/${SERVICE_LABEL}.plist"
  mkdir -p "$launch_dir"
  cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>${SERVICE_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
      <string>${codex_bin}</string>
      <string>app-server</string>
      <string>--listen</string>
      <string>ws://${listen}:${port}</string>
      <string>--ws-auth</string>
      <string>capability-token</string>
      <string>--ws-token-file</string>
      <string>${TOKEN_FILE}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>EnvironmentVariables</key>
    <dict>
      <key>PATH</key>
      <string>${codex_dir}:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
    <key>StandardOutPath</key>
    <string>${CONFIG_DIR}/novascale-app-server.log</string>
    <key>StandardErrorPath</key>
    <string>${CONFIG_DIR}/novascale-app-server.err.log</string>
  </dict>
</plist>
EOF
  if [ "$no_start" -eq 0 ]; then
    launchctl unload "$plist" >/dev/null 2>&1 || true
    launchctl load "$plist"
    launchctl start "$SERVICE_LABEL" || true
  fi
}

install_linux_service() {
  systemd_dir="${HOME}/.config/systemd/user"
  service="${systemd_dir}/${SERVICE_FILE}"
  mkdir -p "$systemd_dir"
  cat > "$service" <<EOF
[Unit]
Description=NovaScale Codex App Server
After=network-online.target

[Service]
Type=simple
Environment=PATH=${codex_dir}:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
ExecStart=${codex_bin} app-server --listen ws://${listen}:${port} --ws-auth capability-token --ws-token-file ${TOKEN_FILE}
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF
  if [ "$no_start" -eq 0 ]; then
    systemctl --user daemon-reload
    systemctl --user enable --now "$SERVICE_FILE"
  else
    systemctl --user daemon-reload
  fi
}

install_service() {
  case "$(os_name)" in
    Darwin) install_macos_service ;;
    Linux) install_linux_service ;;
    *) die "Unsupported OS: $(os_name). macOS and Linux are supported." ;;
  esac
}

load_existing_config() {
  config_to_load=""
  if [ -f "$CONFIG_FILE" ]; then
    config_to_load="$CONFIG_FILE"
  elif [ -f "$LEGACY_CONFIG_FILE" ]; then
    config_to_load="$LEGACY_CONFIG_FILE"
  else
    return 0
  fi
  # shellcheck disable=SC1090
  . "$config_to_load"
  listen="${listen:-${NOVASCALE_CODEX_LISTEN:-${NOVA_CODEX_LISTEN:-}}}"
  port="${port:-${NOVASCALE_CODEX_PORT:-${NOVA_CODEX_PORT:-$DEFAULT_PORT}}}"
  host="${host:-${NOVASCALE_CODEX_HOST:-${NOVA_CODEX_HOST:-}}}"
  name="${name:-${NOVASCALE_CODEX_NAME:-${NOVA_CODEX_NAME:-}}}"
  codex_bin="${codex_bin:-${NOVASCALE_CODEX_BIN:-${NOVA_CODEX_BIN:-}}}"
  codex_dir="${codex_dir:-${NOVASCALE_CODEX_BIN_DIR:-${NOVA_CODEX_BIN_DIR:-}}}"
  config_no_qr="${NOVASCALE_CODEX_NO_QR:-${NOVA_CODEX_NO_QR:-0}}"
  [ "$no_qr" -eq 1 ] || no_qr="$config_no_qr"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --listen)
      [ "$#" -ge 2 ] || die "--listen requires a value"
      listen="$2"
      listen_set=1
      shift 2
      ;;
    --port)
      [ "$#" -ge 2 ] || die "--port requires a value"
      port="$2"
      shift 2
      ;;
    --host)
      [ "$#" -ge 2 ] || die "--host requires a value"
      host="$2"
      shift 2
      ;;
    --name)
      [ "$#" -ge 2 ] || die "--name requires a value"
      name="$2"
      shift 2
      ;;
    --rotate-token)
      rotate_token=1
      shift
      ;;
    --no-start)
      no_start=1
      shift
      ;;
    --print-only)
      print_only=1
      shift
      ;;
    --no-qr)
      no_qr=1
      shift
      ;;
    --yes|-y)
      assume_yes=1
      shift
      ;;
    --enable-notifications)
      notification_requested=1
      notification_setup_explicit=1
      shift
      ;;
    --notification-endpoint)
      [ "$#" -ge 2 ] || die "--notification-endpoint requires a value"
      notification_endpoint="$2"
      notification_endpoint_set=1
      notification_requested=1
      notification_setup_explicit=1
      shift 2
      ;;
    --dev-agent)
      dev_agent=1
      notification_requested=1
      notification_setup_explicit=1
      shift
      ;;
    --agent-binary)
      [ "$#" -ge 2 ] || die "--agent-binary requires a value"
      agent_binary_source="$2"
      notification_requested=1
      notification_setup_explicit=1
      shift 2
      ;;
    --agent-version)
      [ "$#" -ge 2 ] || die "--agent-version requires a value"
      agent_release_version="$2"
      agent_version_set=1
      notification_requested=1
      notification_setup_explicit=1
      shift 2
      ;;
    --no-hook-install)
      no_hook_install=1
      shift
      ;;
    --no-notifications)
      notification_disabled=1
      notification_requested=0
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
done

if [ "$notification_setup_explicit" -eq 1 ] && [ "$notification_disabled" -eq 1 ]; then
  die "--no-notifications cannot be combined with notification setup options"
fi
if [ "$dev_agent" -eq 1 ] && [ -n "$agent_binary_source" ]; then
  die "--dev-agent and --agent-binary cannot be combined"
fi
if [ "$agent_version_set" -eq 1 ] && { [ "$dev_agent" -eq 1 ] || [ -n "$agent_binary_source" ]; }; then
  die "--agent-version cannot be combined with --dev-agent or --agent-binary"
fi
validate_agent_release_version

if [ "$print_only" -eq 1 ]; then
  load_existing_config
  port="${port:-$DEFAULT_PORT}"
  validate_port
  [ -n "$listen" ] || die "Config does not include NOVASCALE_CODEX_LISTEN. Rerun setup.sh with --listen <tailscale-ip> or --listen 0.0.0.0 --host <reachable-host>."
  [ -n "$host" ] || host="$listen"
  [ -n "$name" ] || name="$(hostname_value)"
  print_pairing
  exit 0
fi

port="${port:-$DEFAULT_PORT}"
validate_port
name="${name:-$(hostname_value)}"
codex_bin="$(find_codex_bin || true)"
[ -n "$codex_bin" ] || die "codex was not found. Install Codex CLI first, then rerun this setup."
codex_dir="$(dirname "$codex_bin")"
PATH="${codex_dir}:$PATH"
export PATH

preflight_notification_agent
confirm_reconfigure_existing_setup

tailscale_ip="$(detect_tailscale_ip || true)"
if [ "$listen_set" -eq 0 ]; then
  if [ -n "$tailscale_ip" ]; then
    choose_listen_interactively "$tailscale_ip"
  else
    die "No Tailscale-range address was detected. Rerun with --listen <tailscale-ip> or --listen 0.0.0.0 --host <reachable-host>."
  fi
fi

[ -n "$host" ] || host="$listen"
if [ "$listen" = "0.0.0.0" ] && [ "$host" = "0.0.0.0" ]; then
  die "--host must be a reachable hostname or IP when --listen is 0.0.0.0"
fi

warn_network_listen
check_port_free
ensure_token
write_config
install_helper
install_service
configure_notification_agent
cleanup_legacy_files
print_pairing

notice ""
notice "Configured ${APP_NAME} host service."
notice "Listen: ws://${listen}:${port}"
notice "Pairing host: ${host}:${port}"
notice "Helper: ${HELPER_PATH}"
if [ "$notification_disabled" -eq 0 ] && { [ "$notification_requested" -eq 1 ] || [ -f "$AGENT_CONFIG_FILE" ]; }; then
  notice "Notification agent: ${AGENT_PATH}"
  if [ "$no_hook_install" -eq 1 ]; then
    notice "Codex hooks were not installed because --no-hook-install was provided."
    notice "Notifications remain inactive until equivalent PermissionRequest and Stop hooks are configured and trusted."
  else
    notice ""
    notice "Finish notification setup:"
    notice "  1. Open Codex CLI on this host and run /hooks."
    notice "  2. Review and trust the NovaScale PermissionRequest and Stop hooks."
    notice "  3. If the hooks were newly installed or trusted, wait for active turns and approvals to finish, then run:"
    notice "     novascale-codex restart"
    notice "Notifications remain inactive until Codex has loaded the trusted hooks."
  fi
fi
