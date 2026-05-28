#!/bin/sh
set -eu

APP_NAME="NovaAccess Codex"
SERVICE_LABEL="dev.galaxnet.novaaccess.codex"
SERVICE_FILE="novaaccess-codex.service"
DEFAULT_PORT="14500"
DEFAULT_SCHEME="ws"
CONFIG_DIR="${HOME}/.codex"
CONFIG_FILE="${CONFIG_DIR}/novaaccess-codex-host.env"
TOKEN_FILE="${CONFIG_DIR}/novaaccess-app-server-token"
HELPER_DIR="${HOME}/.local/bin"
HELPER_PATH="${HELPER_DIR}/novaaccess-codex"
codex_bin=""
codex_dir=""

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

usage() {
  cat <<'EOF'
NovaAccess Codex host setup

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

make_pairing_uri() {
  token="$(read_token)"
  esc_name="$(json_escape "$name")"
  esc_host="$(json_escape "$host")"
  esc_token="$(json_escape "$token")"
  payload="$(printf '{"type":"novaaccess-codex-host","version":1,"name":"%s","host":"%s","port":%s,"scheme":"%s","auth":{"mode":"capability-token","token":"%s"}}' "$esc_name" "$esc_host" "$port" "$DEFAULT_SCHEME" "$esc_token" | base64url_encode)"
  printf 'novaaccess-codex://import?payload=%s\n' "$payload"
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

Copy this URI to your phone, then import it from inside the NovaAccess app.
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

existing_setup_present() {
  service="$(service_path 2>/dev/null || true)"
  [ -f "$CONFIG_FILE" ] && return 0
  [ -n "$service" ] && [ -f "$service" ] && return 0
  [ -f "$HELPER_PATH" ] && return 0
  [ -f "$TOKEN_FILE" ] && return 0
  return 1
}

stop_existing_service_quietly() {
  case "$(os_name)" in
    Darwin)
      plist="$(service_path)"
      launchctl stop "$SERVICE_LABEL" >/dev/null 2>&1 || true
      launchctl unload "$plist" >/dev/null 2>&1 || true
      ;;
    Linux)
      systemctl --user stop "$SERVICE_FILE" >/dev/null 2>&1 || true
      ;;
    *) ;;
  esac
}

confirm_reconfigure_existing_setup() {
  existing_setup_present || return 0

  if [ "$assume_yes" -eq 1 ]; then
    notice "Existing NovaAccess Codex setup detected; reconfiguring because --yes was provided."
    stop_existing_service_quietly
    return 0
  fi

  if ! has_tty; then
    die "Existing setup detected. Rerun interactively or pass --yes to reconfigure."
  fi

  service="$(service_path 2>/dev/null || true)"
  cat > /dev/tty <<EOF

Existing NovaAccess Codex setup detected.

EOF
  if [ -f "$CONFIG_FILE" ]; then
    (
      # shellcheck disable=SC1090
      . "$CONFIG_FILE"
      cat > /dev/tty <<EOF
Current config:
  Listen: ${NOVA_CODEX_LISTEN:-unknown}:${NOVA_CODEX_PORT:-unknown}
  Pairing host: ${NOVA_CODEX_HOST:-unknown}:${NOVA_CODEX_PORT:-unknown}
  Name: ${NOVA_CODEX_NAME:-unknown}
  Config: ${CONFIG_FILE}

EOF
    )
  fi
  if [ -n "$service" ] && [ -f "$service" ]; then
    printf 'Service: %s\n\n' "$service" > /dev/tty
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
        host="$(tty_read "Reachable host/IP for NovaAccess: " "")"
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
    printf 'NOVA_CODEX_LISTEN="%s"\n' "$(env_quote "$listen")"
    printf 'NOVA_CODEX_PORT="%s"\n' "$(env_quote "$port")"
    printf 'NOVA_CODEX_HOST="%s"\n' "$(env_quote "$host")"
    printf 'NOVA_CODEX_NAME="%s"\n' "$(env_quote "$name")"
    printf 'NOVA_CODEX_SCHEME="%s"\n' "$(env_quote "$DEFAULT_SCHEME")"
    printf 'NOVA_CODEX_TOKEN_FILE="%s"\n' "$(env_quote "$TOKEN_FILE")"
    printf 'NOVA_CODEX_BIN="%s"\n' "$(env_quote "$codex_bin")"
    printf 'NOVA_CODEX_BIN_DIR="%s"\n' "$(env_quote "$codex_dir")"
    printf 'NOVA_CODEX_NO_QR="%s"\n' "$(env_quote "$no_qr")"
  } > "$tmp"
  chmod 600 "$tmp"
  mv "$tmp" "$CONFIG_FILE"
}

install_helper() {
  mkdir -p "$HELPER_DIR"
  src_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
  if [ -f "${src_dir}/bin/novaaccess-codex" ]; then
    cp "${src_dir}/bin/novaaccess-codex" "$HELPER_PATH"
  else
    write_embedded_helper "$HELPER_PATH"
  fi
  chmod 755 "$HELPER_PATH"
  case ":$PATH:" in
    *":$HELPER_DIR:"*) ;;
    *) notice "Note: $HELPER_DIR is not in PATH. Run helper as $HELPER_PATH or add it to PATH." ;;
  esac
}

write_embedded_helper() {
  helper_target="$1"
  cat > "$helper_target" <<'NOVAACCESS_CODEX_HELPER'
#!/bin/sh
set -eu

SERVICE_LABEL="dev.galaxnet.novaaccess.codex"
SERVICE_FILE="novaaccess-codex.service"
CONFIG_DIR="${HOME}/.codex"
CONFIG_FILE="${CONFIG_DIR}/novaaccess-codex-host.env"
TOKEN_FILE_DEFAULT="${CONFIG_DIR}/novaaccess-app-server-token"
HELPER_PATH="${HOME}/.local/bin/novaaccess-codex"
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
  . "$CONFIG_FILE"
  listen="${NOVA_CODEX_LISTEN:-}"
  port="${NOVA_CODEX_PORT:-14500}"
  host="${NOVA_CODEX_HOST:-$listen}"
  name="${NOVA_CODEX_NAME:-$(hostname 2>/dev/null || printf 'codex-host')}"
  scheme="${NOVA_CODEX_SCHEME:-$DEFAULT_SCHEME}"
  token_file="${NOVA_CODEX_TOKEN_FILE:-$TOKEN_FILE_DEFAULT}"
  config_no_qr="${NOVA_CODEX_NO_QR:-0}"
  [ "$no_qr" -eq 1 ] || no_qr="$config_no_qr"
  [ -n "$listen" ] || die "Config does not include NOVA_CODEX_LISTEN. Rerun setup.sh with --listen <tailscale-ip> or --listen 0.0.0.0 --host <reachable-host>."
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

make_pairing_uri() {
  token="$(read_token)"
  esc_name="$(json_escape "$name")"
  esc_host="$(json_escape "$host")"
  esc_token="$(json_escape "$token")"
  payload="$(printf '{"type":"novaaccess-codex-host","version":1,"name":"%s","host":"%s","port":%s,"scheme":"%s","auth":{"mode":"capability-token","token":"%s"}}' "$esc_name" "$esc_host" "$port" "$scheme" "$esc_token" | base64url_encode)"
  printf 'novaaccess-codex://import?payload=%s\n' "$payload"
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

Copy this URI to your phone, then import it from inside the NovaAccess app.
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
      ;;
    Linux)
      systemctl --user stop "$SERVICE_FILE"
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
  printf 'Delete NovaAccess Codex token and config? [y/N] ' > /dev/tty
  IFS= read -r answer < /dev/tty || answer=""
  case "$answer" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

uninstall() {
  delete_token=ask
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --keep-token) delete_token=no ;;
      --delete-token) delete_token=yes ;;
      *) die "Unknown uninstall option: $1" ;;
    esac
    shift
  done

  stop_service
  case "$(os_name)" in
    Darwin) rm -f "${HOME}/Library/LaunchAgents/${SERVICE_LABEL}.plist" ;;
    Linux)
      systemctl --user disable "$SERVICE_FILE" >/dev/null 2>&1 || true
      rm -f "${HOME}/.config/systemd/user/${SERVICE_FILE}"
      systemctl --user daemon-reload >/dev/null 2>&1 || true
      ;;
  esac

  if [ "$delete_token" = "ask" ]; then
    if confirm_delete_token; then
      delete_token=yes
    else
      delete_token=no
    fi
  fi

  if [ "$delete_token" = "yes" ]; then
    rm -f "$CONFIG_FILE" "$TOKEN_FILE_DEFAULT"
  fi
  rm -f "$HELPER_PATH"
  printf 'Uninstalled NovaAccess Codex host service.\n'
}

usage() {
  cat <<'EOF'
Usage:
  novaaccess-codex status
  novaaccess-codex restart
  novaaccess-codex stop
  novaaccess-codex print-pairing [--no-qr]
  novaaccess-codex rotate-token
  novaaccess-codex uninstall [--keep-token|--delete-token]
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
  restart) restart_service ;;
  stop) stop_service ;;
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
NOVAACCESS_CODEX_HELPER
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
    <string>${CONFIG_DIR}/novaaccess-app-server.log</string>
    <key>StandardErrorPath</key>
    <string>${CONFIG_DIR}/novaaccess-app-server.err.log</string>
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
Description=NovaAccess Codex App Server
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
  [ -f "$CONFIG_FILE" ] || return 0
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  listen="${listen:-${NOVA_CODEX_LISTEN:-}}"
  port="${port:-${NOVA_CODEX_PORT:-$DEFAULT_PORT}}"
  host="${host:-${NOVA_CODEX_HOST:-}}"
  name="${name:-${NOVA_CODEX_NAME:-}}"
  codex_bin="${codex_bin:-${NOVA_CODEX_BIN:-}}"
  codex_dir="${codex_dir:-${NOVA_CODEX_BIN_DIR:-}}"
  config_no_qr="${NOVA_CODEX_NO_QR:-0}"
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
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
done

if [ "$print_only" -eq 1 ]; then
  load_existing_config
  port="${port:-$DEFAULT_PORT}"
  validate_port
  [ -n "$listen" ] || die "Config does not include NOVA_CODEX_LISTEN. Rerun setup.sh with --listen <tailscale-ip> or --listen 0.0.0.0 --host <reachable-host>."
  [ -n "$host" ] || host="$listen"
  [ -n "$name" ] || name="$(hostname_value)"
  print_pairing
  exit 0
fi

port="${port:-$DEFAULT_PORT}"
validate_port
name="${name:-$(hostname_value)}"
command_exists codex || die "codex was not found in PATH. Install Codex CLI first, then rerun this setup."
codex_bin="$(command -v codex)"
codex_dir="$(dirname "$codex_bin")"

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
print_pairing

notice ""
notice "Configured ${APP_NAME} host service."
notice "Listen: ws://${listen}:${port}"
notice "Pairing host: ${host}:${port}"
notice "Helper: ${HELPER_PATH}"
