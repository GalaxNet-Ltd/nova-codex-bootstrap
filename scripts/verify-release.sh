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
  --no-notifications \
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
if HOME="$default_home" PATH="$test_path" \
  "$default_home/.local/bin/novascale-codex" restart-if-updated \
  >"$temporary_root/default-conditional-restart-output" \
  2>"$temporary_root/default-conditional-restart-error"
then
  printf 'conditional restart unexpectedly succeeded without the agent detector\n' >&2
  exit 1
fi
grep -q 'no service was restarted' "$temporary_root/default-conditional-restart-error"

case "$(uname -s)" in
  Darwin) test -s "$default_home/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist" ;;
  Linux) test -s "$default_home/.config/systemd/user/novascale-codex.service" ;;
  *) printf 'unsupported release-test host OS\n' >&2; exit 1 ;;
esac

official_home="$temporary_root/official-home"
official_stub_dir="$temporary_root/official-bin"
mkdir -p "$official_home/.local/bin" "$official_stub_dir"
ln -s "$true_binary" "$official_home/.local/bin/codex"
ln -s "$true_binary" "$official_stub_dir/launchctl"
ln -s "$true_binary" "$official_stub_dir/systemctl"
official_path="$official_stub_dir:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
HOME="$official_home" PATH="$official_path" "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 10))" \
  --no-notifications \
  --no-start \
  --no-qr \
  >"$temporary_root/official-output" \
  2>"$temporary_root/official-error"
grep -q "NOVASCALE_CODEX_BIN=\"$official_home/.local/bin/codex\"" \
  "$official_home/.codex/novascale-codex-host.env"

cp "$default_home/.codex/novascale-app-server-token" "$temporary_root/token-before-redeploy"
HOME="$default_home" PATH="$test_path" "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$test_port" \
  --yes \
  --no-notifications \
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
  --no-notifications \
  --no-start \
  --no-qr \
  >"$temporary_root/rotate-output" \
  2>"$temporary_root/rotate-error"
if cmp -s "$temporary_root/token-before-redeploy" "$default_home/.codex/novascale-app-server-token"; then
  printf 'explicit token rotation did not replace the capability token\n' >&2
  exit 1
fi

enrollment_home="$temporary_root/enrollment-home"
enrollment_agent="$temporary_root/enrollment-agent"
enrollment_log="$temporary_root/enrollment-agent.log"
release_fixture_dir="$temporary_root/release-fixture"
release_package_dir="$temporary_root/release-package"
release_download_log="$temporary_root/release-download.log"
download_stub_dir="$temporary_root/download-bin"
mkdir -p "$enrollment_home"
cat >"$enrollment_agent" <<'EOF'
#!/bin/sh
set -eu
case "${1:-}" in
  version)
    printf '%s\n' '0.1.3'
    ;;
  init)
    shift
    [ "$#" -eq 2 ]
    [ "${1:-}" = "--endpoint" ]
    if [ ! -f "$HOME/.config/novascale-agent/config.json" ]; then
      printf 'init endpoint received: %s\n' "${2:-}" >>"$NOVASCALE_TEST_AGENT_LOG"
      mkdir -p "$HOME/.config/novascale-agent"
      printf '%s\n' '{"version":1,"hostId":"host-test","endpoint":"http://127.0.0.1:8080","registrationState":"pending"}' \
        >"$HOME/.config/novascale-agent/config.json"
    else
      [ "${2:-}" = "http://127.0.0.1:8080" ]
      printf 'pending identity preserved\n' >>"$NOVASCALE_TEST_AGENT_LOG"
    fi
    ;;
  registration-state)
    printf 'registration-state\n' >>"$NOVASCALE_TEST_AGENT_LOG"
    sed -n 's/.*"registrationState":"\([^"]*\)".*/\1/p' "$HOME/.config/novascale-agent/config.json"
    ;;
  endpoint)
    printf 'endpoint\n' >>"$NOVASCALE_TEST_AGENT_LOG"
    sed -n 's/.*"endpoint":"\([^"]*\)".*/\1/p' "$HOME/.config/novascale-agent/config.json"
    ;;
  switch-backend)
    printf 'unexpected switch-backend\n' >>"$NOVASCALE_TEST_AGENT_LOG"
    exit 1
    ;;
  status)
    printf 'status\n' >>"$NOVASCALE_TEST_AGENT_LOG"
    ;;
  app-server)
    shift
    printf 'app-server %s\n' "$*" >>"$NOVASCALE_TEST_AGENT_LOG"
    if [ "${1:-}" = "update-status" ]; then
      printf 'current\n'
    fi
    ;;
  hooks|serve|daemon-version)
    exit 0
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod 755 "$enrollment_agent"

mkdir -p "$release_fixture_dir" "$release_package_dir/THIRD_PARTY_LICENSES" "$download_stub_dir"
cp "$enrollment_agent" "$release_package_dir/novascale-agent"
cp "$root_dir/LICENSE" "$release_package_dir/LICENSE"
cp -R "$root_dir/notifications/third_party_licenses/." "$release_package_dir/THIRD_PARTY_LICENSES/"
release_archive="novascale-agent_0.1.3_linux_amd64.tar.gz"
tar -C "$release_package_dir" -czf "$release_fixture_dir/$release_archive" \
  novascale-agent LICENSE THIRD_PARTY_LICENSES
if command -v shasum >/dev/null 2>&1; then
  (cd "$release_fixture_dir" && shasum -a 256 "$release_archive" >SHA256SUMS)
else
  (cd "$release_fixture_dir" && sha256sum "$release_archive" >SHA256SUMS)
fi
cat >"$download_stub_dir/curl" <<'EOF'
#!/bin/sh
set -eu
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      output="$2"
      shift 2
      ;;
    http://*|https://*)
      url="$1"
      shift
      ;;
    *) shift ;;
  esac
done
[ -n "$output" ] && [ -n "$url" ]
asset="${url##*/}"
printf '%s\n' "$asset" >>"$NOVASCALE_TEST_RELEASE_LOG"
cp "$NOVASCALE_TEST_RELEASE_DIR/$asset" "$output"
EOF
cat >"$download_stub_dir/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
  -s|'') printf '%s\n' 'Linux' ;;
  -m) printf '%s\n' 'x86_64' ;;
  -n) printf '%s\n' 'release-test-host' ;;
  *) exit 1 ;;
esac
EOF
chmod 755 "$download_stub_dir/curl" "$download_stub_dir/uname"
download_test_path="$download_stub_dir:$test_path"

bad_release_dir="$temporary_root/bad-release-fixture"
bad_release_home="$temporary_root/bad-release-home"
mkdir -p "$bad_release_dir" "$bad_release_home"
cp "$release_fixture_dir/$release_archive" "$bad_release_dir/$release_archive"
printf '%064d  %s\n' 0 "$release_archive" >"$bad_release_dir/SHA256SUMS"
if HOME="$bad_release_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  NOVASCALE_TEST_RELEASE_DIR="$bad_release_dir" NOVASCALE_TEST_RELEASE_LOG="$release_download_log" \
  "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 3))" \
  --no-start \
  --no-qr \
  --notification-endpoint http://127.0.0.1:8080 \
  >"$temporary_root/bad-release-output" \
  2>"$temporary_root/bad-release-error"
then
  printf 'notification setup accepted a mismatched release checksum\n' >&2
  exit 1
fi
grep -q 'release checksum mismatch' "$temporary_root/bad-release-error"
test ! -e "$bad_release_home/.codex/novascale-codex-host.env"
test ! -e "$bad_release_home/.local/bin/novascale-agent"
: >"$release_download_log"

unsafe_release_dir="$temporary_root/unsafe-release-fixture"
unsafe_release_package="$temporary_root/unsafe-release-package"
unsafe_release_home="$temporary_root/unsafe-release-home"
mkdir -p "$unsafe_release_dir" "$unsafe_release_package/THIRD_PARTY_LICENSES" "$unsafe_release_home"
ln -s /bin/sh "$unsafe_release_package/novascale-agent"
cp "$root_dir/LICENSE" "$unsafe_release_package/LICENSE"
cp -R "$root_dir/notifications/third_party_licenses/." "$unsafe_release_package/THIRD_PARTY_LICENSES/"
tar -C "$unsafe_release_package" -czf "$unsafe_release_dir/$release_archive" \
  novascale-agent LICENSE THIRD_PARTY_LICENSES
if command -v shasum >/dev/null 2>&1; then
  (cd "$unsafe_release_dir" && shasum -a 256 "$release_archive" >SHA256SUMS)
else
  (cd "$unsafe_release_dir" && sha256sum "$release_archive" >SHA256SUMS)
fi
if HOME="$unsafe_release_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  NOVASCALE_TEST_RELEASE_DIR="$unsafe_release_dir" NOVASCALE_TEST_RELEASE_LOG="$release_download_log" \
  "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 4))" \
  --no-start \
  --no-qr \
  --notification-endpoint http://127.0.0.1:8080 \
  >"$temporary_root/unsafe-release-output" \
  2>"$temporary_root/unsafe-release-error"
then
  printf 'notification setup accepted a linked release executable\n' >&2
  exit 1
fi
grep -q 'archive contains a link' "$temporary_root/unsafe-release-error"
test ! -e "$unsafe_release_home/.codex/novascale-codex-host.env"
test ! -e "$unsafe_release_home/.local/bin/novascale-agent"
: >"$release_download_log"

HOME="$enrollment_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  NOVASCALE_TEST_RELEASE_DIR="$release_fixture_dir" NOVASCALE_TEST_RELEASE_LOG="$release_download_log" \
  "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 3))" \
  --no-start \
  --no-qr \
  --notification-endpoint http://127.0.0.1:8080 \
  >"$temporary_root/enrollment-output" \
  2>"$temporary_root/enrollment-error"
grep -q 'Downloading signed NovaScale notification agent 0.1.3 for linux-amd64.' "$temporary_root/enrollment-output"
grep -q '^Finish notification setup:$' "$temporary_root/enrollment-output"
grep -q '^     novascale-codex restart$' "$temporary_root/enrollment-output"
test "$(grep -c '^SHA256SUMS$' "$release_download_log")" -eq 1
test "$(grep -c "^${release_archive}$" "$release_download_log")" -eq 1
cmp -s "$enrollment_agent" "$enrollment_home/.local/bin/novascale-agent"
grep -q '^init endpoint received: http://127.0.0.1:8080$' "$enrollment_log"
HOME="$enrollment_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  "$enrollment_home/.local/bin/novascale-codex" update-status \
  >"$temporary_root/conditional-update-status"
grep -q '^current$' "$temporary_root/conditional-update-status"
HOME="$enrollment_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  "$enrollment_home/.local/bin/novascale-codex" restart-if-updated \
  >"$temporary_root/conditional-restart-output"
grep -q '^app-server update-status$' "$enrollment_log"
grep -q '^app-server restart-if-updated$' "$enrollment_log"
test ! -e "$enrollment_home/.config/novascale-agent/pending-setup-token"

: >"$enrollment_log"
: >"$release_download_log"
HOME="$enrollment_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  NOVASCALE_TEST_RELEASE_DIR="$release_fixture_dir" NOVASCALE_TEST_RELEASE_LOG="$release_download_log" \
  "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 3))" \
  --yes \
  --no-start \
  --no-qr \
  --enable-notifications \
  >"$temporary_root/enrollment-redeploy-output" \
  2>"$temporary_root/enrollment-redeploy-error"
test ! -s "$release_download_log"
grep -q '^registration-state$' "$enrollment_log"
grep -q '^pending identity preserved$' "$enrollment_log"

printf '%s\n' '{"version":1,"hostId":"host-test","endpoint":"http://127.0.0.1:8080","registrationState":"active"}' \
  >"$enrollment_home/.config/novascale-agent/config.json"
: >"$enrollment_log"
HOME="$enrollment_home" PATH="$download_test_path" NOVASCALE_TEST_AGENT_LOG="$enrollment_log" \
  NOVASCALE_TEST_RELEASE_DIR="$release_fixture_dir" NOVASCALE_TEST_RELEASE_LOG="$release_download_log" \
  "$root_dir/setup.sh" \
  --listen 127.0.0.1 \
  --host 127.0.0.1 \
  --port "$((test_port + 3))" \
  --yes \
  --no-start \
  --no-qr \
  --enable-notifications \
  --notification-endpoint https://notify.production.invalid \
  >"$temporary_root/enrollment-mismatch-output" \
  2>"$temporary_root/enrollment-mismatch-error"
grep -q '^endpoint$' "$enrollment_log"
if grep -q '^switch-backend ' "$enrollment_log"; then
  printf 'bootstrap implicitly switched an active notification backend\n' >&2
  exit 1
fi
grep -q '"endpoint":"http://127.0.0.1:8080"' "$enrollment_home/.config/novascale-agent/config.json"
grep -q '"registrationState":"active"' "$enrollment_home/.config/novascale-agent/config.json"

if command -v go >/dev/null 2>&1; then
  (
    cd "$root_dir/notifications"
    GOCACHE="${GOCACHE:-$temporary_root/go-cache}" go test ./...
    GOCACHE="${GOCACHE:-$temporary_root/go-cache}" go vet ./...
  )
fi

sh "$root_dir/scripts/test-notification-agent-upgrade.sh"

test "$(grep -c -- '--check-notarization' "$root_dir/setup.sh")" -eq 1
test "$(grep -c -- '--check-notarization' "$root_dir/.github/workflows/agent-release.yml")" -eq 1
grep -q 'lipo "$candidate" -verify_arch arm64' "$root_dir/setup.sh"
grep -q 'lipo "$candidate" -verify_arch x86_64' "$root_dir/setup.sh"
grep -q 'lipo "$package_dir/novascale-agent" -verify_arch arm64' "$root_dir/.github/workflows/agent-release.yml"
grep -q 'lipo "$package_dir/novascale-agent" -verify_arch x86_64' "$root_dir/.github/workflows/agent-release.yml"
test "$(grep -c -- '--repo "$GITHUB_REPOSITORY"' "$root_dir/.github/workflows/agent-release.yml")" -eq 1
if grep -q -- 'spctl --assess' \
  "$root_dir/setup.sh" \
  "$root_dir/.github/workflows/agent-release.yml"
then
  printf 'standalone macOS agent incorrectly uses an app-bundle Gatekeeper assessment\n' >&2
  exit 1
fi

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
printf '  notification opt-out bootstrap: wrapper only\n'
printf '  redeploy token: preserved unless --rotate-token is explicit\n'
printf '  notification bootstrap: autonomous identity-bound enrollment with daemon-owned retry\n'
printf '  embedded helper: synchronized\n'
printf '  credential shapes: clear\n'
