#!/bin/sh

set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/novascale-release.XXXXXX")
trap 'rm -rf "$temporary_root"' EXIT HUP INT TERM
umask 077

stub_dir="$temporary_root/bin"
mkdir -p "$stub_dir"
true_binary=/usr/bin/true
[ -x "$true_binary" ] || true_binary=/bin/true
[ -x "$true_binary" ] || {
  printf 'true executable not found\n' >&2
  exit 1
}
ln -s "$true_binary" "$stub_dir/codex"
ln -s "$true_binary" "$stub_dir/launchctl"
ln -s "$true_binary" "$stub_dir/systemctl"
test_path="$stub_dir:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
test_port=$((24000 + ($$ % 10000)))

sh -n "$root_dir/setup.sh"
sh -n "$root_dir/bin/novascale-codex"
sh -n "$root_dir/scripts/test-notification-agent-upgrade.sh"
test -s "$root_dir/LICENSE"
test -d "$root_dir/notifications/third_party_licenses"
find "$root_dir/notifications/third_party_licenses" -type f -name LICENSE -print -quit |
  grep -q .

embedded_helper="$temporary_root/embedded-helper"
awk '
  active && $0 == "NOVASCALE_CODEX_HELPER" { exit }
  active { print }
  /<<'"'"'NOVASCALE_CODEX_HELPER'"'"'/ { active = 1 }
' "$root_dir/setup.sh" >"$embedded_helper"
cmp "$root_dir/bin/novascale-codex" "$embedded_helper"

default_home="$temporary_root/default-home"
mkdir -p "$default_home"
HOME="$default_home" PATH="$test_path" "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$test_port" \
  --no-start \
  --no-qr \
  >"$temporary_root/default-output" \
  2>"$temporary_root/default-error"

test -x "$default_home/.local/bin/novascale-codex"
test -s "$default_home/.codex/novascale-codex-host.env"
test -s "$default_home/.codex/novascale-app-server-token"
test ! -e "$default_home/.local/bin/novascale-agent"
test ! -e "$default_home/.config/novascale-agent"
test ! -e "$default_home/.local/state/novascale-agent"
test ! -e "$default_home/.codex/hooks.json"
test ! -e "$default_home/Library/LaunchAgents/dev.galaxnet.novascale.agent.plist"
test ! -e "$default_home/.config/systemd/user/novascale-agent.service"

case "$(uname -s)" in
  Darwin) test -s "$default_home/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist" ;;
  Linux) test -s "$default_home/.config/systemd/user/novascale-codex.service" ;;
  *) printf 'unsupported release-test host OS\n' >&2; exit 1 ;;
esac

cp "$default_home/.codex/novascale-app-server-token" "$temporary_root/token-before-redeploy"
HOME="$default_home" PATH="$test_path" "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$test_port" \
  --yes \
  --no-start \
  --no-qr \
  >"$temporary_root/redeploy-output" \
  2>"$temporary_root/redeploy-error"
cmp "$temporary_root/token-before-redeploy" "$default_home/.codex/novascale-app-server-token"

HOME="$default_home" PATH="$test_path" "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$test_port" \
  --yes \
  --rotate-token \
  --no-start \
  --no-qr \
  >"$temporary_root/rotate-output" \
  2>"$temporary_root/rotate-error"
if cmp -s "$temporary_root/token-before-redeploy" "$default_home/.codex/novascale-app-server-token"; then
  printf 'explicit token rotation did not replace the capability token\n' >&2
  exit 1
fi

gated_home="$temporary_root/gated-home"
mkdir -p "$gated_home"
if HOME="$gated_home" PATH="$test_path" "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 1))" \
  --no-start \
  --no-qr \
  --enable-notifications \
  --agent-binary "$true_binary" \
  >"$temporary_root/gated-output" \
  2>"$temporary_root/gated-error"
then
  printf 'notification setup unexpectedly succeeded without enrollment inputs\n' >&2
  exit 1
fi
grep -q -- '--notification-endpoint is required' "$temporary_root/gated-error"
test ! -e "$gated_home/.codex/novascale-codex-host.env"
test ! -e "$gated_home/.local/bin/novascale-agent"

if command -v go >/dev/null 2>&1; then
  (
    cd "$root_dir/notifications"
    GOCACHE="${GOCACHE:-$temporary_root/go-cache}" go test ./...
    GOCACHE="${GOCACHE:-$temporary_root/go-cache}" go vet ./...
  )
fi

sh "$root_dir/scripts/test-notification-agent-upgrade.sh"

if command -v rg >/dev/null 2>&1; then
  private_key_pattern='-----BEGIN (RSA |EC |OPENSSH )?PRIVATE'" KEY"'-----'
  # A public dependency digest explicitly prefixed with "sha256:" is not a
  # credential. Standalone 64-hex values remain forbidden because they match
  # APNs device tokens and the local bearer-token format.
  credential_shape_pattern='(^|[^:])[A-Fa-f0-9]{64}([^A-Fa-f0-9]|$)'
  if rg -l --hidden \
    --glob '!.git/**' \
    --glob '!AGENTS.md' \
    --glob '!NovaAccess/**' \
    --glob '!notifications/dist/**' \
    --glob '!notifications/dist.tar.gz' \
    -- "$private_key_pattern|$credential_shape_pattern" \
    "$root_dir" >"$temporary_root/sensitive-candidates"
  then
    printf 'credential-shaped content found in release candidates:\n' >&2
    sed -n '1,40p' "$temporary_root/sensitive-candidates" >&2
    exit 1
  fi
fi

printf 'release verification passed\n'
printf '  default bootstrap: wrapper only\n'
printf '  redeploy token: preserved unless --rotate-token is explicit\n'
printf '  notification bootstrap: explicit, signed host registration\n'
printf '  embedded helper: synchronized\n'
printf '  credential shapes: clear\n'
