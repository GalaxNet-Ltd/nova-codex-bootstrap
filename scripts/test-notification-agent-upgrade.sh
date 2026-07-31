#!/bin/sh

set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/novascale-agent-upgrade.XXXXXX")
trap 'rm -rf "$temporary_root"' EXIT HUP INT TERM
umask 077

test_home="$temporary_root/home"
stub_dir="$temporary_root/bin"
service_log="$temporary_root/service.log"
live_version_file="$temporary_root/live-version"
incoming_agent="$temporary_root/novascale-agent-v2"
mkdir -p "$test_home/.local/bin" "$test_home/.config/novascale-agent" "$stub_dir"

true_binary=/usr/bin/true
[ -x "$true_binary" ] || true_binary=/bin/true
ln -s "$true_binary" "$stub_dir/codex"

cat >"$stub_dir/service-control" <<'EOF'
#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >>"$NOVASCALE_TEST_SERVICE_LOG"
case "$*" in
  *dev.galaxnet.novascale.agent*|*novascale-agent.service*)
    case "$*" in
      *unload*|*restart*)
        printf '%s\n' "$NOVASCALE_TEST_RESTART_VERSION" >"$NOVASCALE_TEST_LIVE_VERSION_FILE"
        ;;
    esac
    ;;
esac
exit 0
EOF
chmod 755 "$stub_dir/service-control"
ln -s service-control "$stub_dir/launchctl"
ln -s service-control "$stub_dir/systemctl"

write_fake_agent() {
  target=$1
  binary_version=$2
  cat >"$target" <<EOF
#!/bin/sh
case "\${1:-}" in
  version) printf '%s\n' '$binary_version' ;;
  daemon-version) cat "\$NOVASCALE_TEST_LIVE_VERSION_FILE" ;;
  registration-state) printf '%s\n' 'active' ;;
  init|serve|status|hooks|app-server) exit 0 ;;
  *) exit 1 ;;
esac
EOF
  chmod 755 "$target"
}

write_fake_agent "$test_home/.local/bin/novascale-agent" "1.0.0-test"
write_fake_agent "$incoming_agent" "2.0.0-test"
printf '%s\n' "1.0.0-test" >"$live_version_file"
printf '%s\n' '{"version":1,"hostId":"host-test","endpoint":"https://notify.invalid","registrationState":"active"}' \
  >"$test_home/.config/novascale-agent/config.json"

export NOVASCALE_TEST_SERVICE_LOG="$service_log"
export NOVASCALE_TEST_LIVE_VERSION_FILE="$live_version_file"
export NOVASCALE_TEST_RESTART_VERSION="2.0.0-test"
test_path="$stub_dir:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

run_setup() {
  HOME="$test_home" PATH="$test_path" "$root_dir/setup.sh" \
    --listen 127.0.0.1 \
    --host 127.0.0.1 \
    --port 24567 \
    --yes \
    --no-qr \
    --enable-notifications \
    --agent-binary "$incoming_agent" \
    >"$temporary_root/setup-output" \
    2>"$temporary_root/setup-error"
}

run_setup
test "$("$test_home/.local/bin/novascale-agent" version)" = "2.0.0-test"
test "$(cat "$live_version_file")" = "2.0.0-test"
case "$(uname -s)" in
  Darwin) grep -q 'unload .*dev.galaxnet.novascale.agent.plist' "$service_log" ;;
  Linux) grep -q 'restart novascale-agent.service' "$service_log" ;;
  *) printf 'unsupported agent-upgrade test host OS\n' >&2; exit 1 ;;
esac

: >"$service_log"
run_setup
case "$(uname -s)" in
  Darwin)
    if grep -qE '(unload|load) .*dev.galaxnet.novascale.agent.plist' "$service_log"; then
      printf 'same-version macOS redeploy restarted the notification agent\n' >&2
      exit 1
    fi
    ;;
  Linux)
    if grep -q 'restart novascale-agent.service' "$service_log"; then
      printf 'same-version Linux redeploy restarted the notification agent\n' >&2
      exit 1
    fi
    ;;
esac

printf 'notification agent upgrade verification passed\n'
