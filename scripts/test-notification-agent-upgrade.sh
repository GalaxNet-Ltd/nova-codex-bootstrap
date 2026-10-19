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
live_capability_file="$temporary_root/live-capability"
incoming_agent="$temporary_root/novascale-agent-v2"
test_setup_source=codex
mkdir -p "$test_home/.local/bin" "$test_home/.config/novascale-agent" "$stub_dir"
export XDG_CONFIG_HOME="$test_home/.config"
export XDG_STATE_HOME="$test_home/.local/state"

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
        "$NOVASCALE_TEST_INSTALLED_AGENT" version >"$NOVASCALE_TEST_LIVE_VERSION_FILE"
        case "$("$NOVASCALE_TEST_INSTALLED_AGENT" capabilities)" in
          *'"herdr-v2"'*) printf '1\n' >"$NOVASCALE_TEST_LIVE_CAPABILITY_FILE" ;;
          *) printf '0\n' >"$NOVASCALE_TEST_LIVE_CAPABILITY_FILE" ;;
        esac
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
  endpoint) printf '%s\n' 'https://notify.invalid' ;;
  registration-state) printf '%s\n' 'active' ;;
  capabilities)
    case '$binary_version' in
      1.*) printf '%s\n' '{"capabilities":["codex-v1"]}' ;;
      *) printf '%s\n' '{"capabilities":["codex-v1","herdr-v2","herdr-host-capture-v1"]}' ;;
    esac
    ;;
  hooks) printf 'agent %s\n' "\$*" >>"\$NOVASCALE_TEST_SERVICE_LOG" ;;
  herdr)
    printf 'agent %s\n' "\$*" >>"\$NOVASCALE_TEST_SERVICE_LOG"
    if [ "\${2:-}" = "status" ]; then
      if [ "\$(cat "\$NOVASCALE_TEST_LIVE_CAPABILITY_FILE")" = "1" ]; then
        printf '%s\n' '{"daemonCapabilities":["herdr-v2","herdr-host-capture-v1"]}'
      else
        printf '%s\n' '{"daemonCapabilities":[]}'
      fi
    else
      [ "\${NOVASCALE_TEST_FAIL_HERDR:-0}" -eq 0 ]
    fi
    ;;
  init|switch-backend|serve|status|app-server) exit 0 ;;
  *) exit 1 ;;
esac
EOF
  chmod 755 "$target"
}

write_fake_agent "$test_home/.local/bin/novascale-agent" "1.0.0-test"
write_fake_agent "$incoming_agent" "2.0.0-test"
printf '%s\n' "1.0.0-test" >"$live_version_file"
printf '0\n' >"$live_capability_file"
printf '%s\n' '{"version":1,"hostId":"host-test","endpoint":"https://notify.invalid","registrationState":"active"}' \
  >"$test_home/.config/novascale-agent/config.json"

export NOVASCALE_TEST_SERVICE_LOG="$service_log"
export NOVASCALE_TEST_LIVE_VERSION_FILE="$live_version_file"
export NOVASCALE_TEST_LIVE_CAPABILITY_FILE="$live_capability_file"
export NOVASCALE_TEST_RESTART_VERSION="2.0.0-test"
export NOVASCALE_TEST_INSTALLED_AGENT="$test_home/.local/bin/novascale-agent"
test_path="$stub_dir:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

run_setup() {
  if [ "$test_setup_source" = "herdr" ]; then
    set -- --notifications-only --notification-source herdr
  else
    set -- --listen 127.0.0.1 --host 127.0.0.1 --port 24567
  fi
  HOME="$test_home" PATH="$test_path" "$root_dir/setup.sh" \
    "$@" \
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

test_setup_source=herdr
mkdir -p "$test_home/.codex" "$test_home/.local/state/novascale-agent"
printf '%s\n' '{"hooks":{"Stop":[]}}' >"$test_home/.codex/hooks.json"
printf '%s\n' 'retained-queue-fixture' >"$test_home/.local/state/novascale-agent/agent.db"
cp "$test_home/.codex/hooks.json" "$temporary_root/hooks-before"
cp "$test_home/.config/novascale-agent/config.json" "$temporary_root/identity-before"
cp "$test_home/.local/state/novascale-agent/agent.db" "$temporary_root/queue-before"
cp "$test_home/.local/bin/novascale-codex" "$temporary_root/wrapper-before"

# Upgrade a Codex-only agent through Herdr, preserving the same installation.
write_fake_agent "$test_home/.local/bin/novascale-agent" "1.0.0-test"
printf '%s\n' "1.0.0-test" >"$live_version_file"
: >"$service_log"
run_setup
test "$("$test_home/.local/bin/novascale-agent" version)" = "2.0.0-test"
cmp -s "$temporary_root/hooks-before" "$test_home/.codex/hooks.json"
cmp -s "$temporary_root/identity-before" "$test_home/.config/novascale-agent/config.json"
cmp -s "$temporary_root/queue-before" "$test_home/.local/state/novascale-agent/agent.db"
cmp -s "$temporary_root/wrapper-before" "$test_home/.local/bin/novascale-codex"
if grep -qE 'agent hooks|dev.galaxnet.novascale.codex|novascale-codex.service' "$service_log"; then
  printf 'Herdr setup touched Codex hooks or wrapper service\n' >&2; exit 1
fi
grep -q 'agent herdr install --json' "$service_log"
if grep -q -- '--session' "$service_log"; then
  printf 'Host-wide Herdr setup selected a session\n' >&2; exit 1
fi

# Herdr-only setup must also work without Codex present in PATH.
mv "$stub_dir/codex" "$temporary_root/codex-stub-withheld"
original_test_path="$test_path"
test_path="$stub_dir:/usr/bin:/bin:/usr/sbin:/sbin"
run_setup
test_path="$original_test_path"
mv "$temporary_root/codex-stub-withheld" "$stub_dir/codex"

# Capability differences matter even when both binaries report the same label.
printf '0\n' >"$live_capability_file"
: >"$service_log"
run_setup
test "$(cat "$live_capability_file")" = "1"
test "$(cat "$live_version_file")" = "2.0.0-test"

# An installed new binary with an older live daemon still needs one restart.
printf '%s\n' "1.0.0-test" >"$live_version_file"
: >"$service_log"
run_setup
test "$(cat "$live_version_file")" = "2.0.0-test"

# Feature support cannot be silently removed by a later old installer input.
write_fake_agent "$incoming_agent" "1.0.0-test"
if run_setup; then printf 'Herdr-capable agent was silently downgraded\n' >&2; exit 1; fi
test "$("$test_home/.local/bin/novascale-agent" version)" = "2.0.0-test"

# A failure after binary replacement restores the previous agent and identity.
write_fake_agent "$incoming_agent" "3.0.0-test"
export NOVASCALE_TEST_FAIL_HERDR=1
if run_setup; then printf 'partial Herdr setup failure was hidden\n' >&2; exit 1; fi
unset NOVASCALE_TEST_FAIL_HERDR
test "$("$test_home/.local/bin/novascale-agent" version)" = "2.0.0-test"
test "$(cat "$live_version_file")" = "2.0.0-test"
cmp -s "$temporary_root/identity-before" "$test_home/.config/novascale-agent/config.json"
cmp -s "$temporary_root/hooks-before" "$test_home/.codex/hooks.json"

# Custom XDG locations must remain the same in CLI setup and the background
# service; otherwise an upgrade can accidentally enroll a second identity.
write_fake_agent "$incoming_agent" "2.0.0-test"
export XDG_CONFIG_HOME="$temporary_root/custom config & preferences"
export XDG_STATE_HOME="$temporary_root/custom state"
mkdir -p "$XDG_CONFIG_HOME/novascale-agent" "$XDG_STATE_HOME/novascale-agent"
cp "$temporary_root/identity-before" "$XDG_CONFIG_HOME/novascale-agent/config.json"
cp "$temporary_root/queue-before" "$XDG_STATE_HOME/novascale-agent/agent.db"
run_setup
cmp -s "$temporary_root/identity-before" "$XDG_CONFIG_HOME/novascale-agent/config.json"
cmp -s "$temporary_root/queue-before" "$XDG_STATE_HOME/novascale-agent/agent.db"
case "$(uname -s)" in
  Darwin)
    plutil -lint "$test_home/Library/LaunchAgents/dev.galaxnet.novascale.agent.plist" >/dev/null
    grep -q 'custom config &amp; preferences' "$test_home/Library/LaunchAgents/dev.galaxnet.novascale.agent.plist"
    ;;
  Linux)
    grep -Fq "XDG_CONFIG_HOME=$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/systemd/user/novascale-agent.service"
    ;;
esac
export XDG_CONFIG_HOME="$test_home/.config"
export XDG_STATE_HOME="$test_home/.local/state"

printf 'notification agent upgrade and Herdr coexistence verification passed\n'
