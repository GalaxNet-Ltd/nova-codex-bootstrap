# NovaScale Codex Host

Host-side setup utility for NovaScale Codex integration. It configures a user-level `codex app-server` service, creates a capability token, and prints a pairing URI for NovaScale iOS.

This setup does not install third-party software. It uses the official `codex` command already installed on the host and the user's existing Tailscale networking mesh.

## Setup

```sh
curl -fsSL https://raw.githubusercontent.com/GalaxNet-Ltd/nova-codex-bootstrap/refs/heads/main/setup.sh | sh
```

If a `100.64.0.0/10` interface address is detected, setup assumes it is a Tailscale address and offers a small terminal menu. The recommended choice binds `codex app-server` only to that Tailscale-range address.

If no Tailscale-range address is detected, pass an explicit address:

```sh
./setup.sh --listen 100.88.77.66 --host 100.88.77.66
```

To force literal URI output even when `qrencode` is installed:

```sh
./setup.sh --no-qr
```

If setup has already been run, `setup.sh` shows the current config and asks before reconfiguring it. For scripted or non-interactive SSH reconfiguration:

```sh
./setup.sh --yes
```

Redeploying preserves the existing capability token, so already-paired apps
continue to work. Token replacement happens only when `--rotate-token` is
explicitly supplied or no usable token exists.

### Backward compatibility

The default command above remains the stable, wrapper-only installation path.
It does not install the notification agent, modify Codex hooks, or require a
notification backend. Existing NovaScale versions—including the version
currently available on the App Store—can continue to bootstrap and use Codex
hosts exactly as before.

Notifications remain an additive feature. The updated app's new-host sheet can
default its notification toggle on; supplying the notification endpoint and
setup-token file activates notification setup and downloads the pinned signed
agent release automatically. Turning that toggle off must omit the enrollment
inputs and pass `--no-notifications`. The raw wrapper-only command above remains
unchanged for older apps and manual users.

## Advanced Subnet Mode

Use `0.0.0.0` only when the host is behind Tailscale subnet routing or a trusted private network:

```sh
./setup.sh --listen 0.0.0.0 --host 192.168.50.20 --port 14500
```

Network listen mode exposes Codex app-server to any machine that can reach the configured host and port. A capability token is required, but reachable network exposure still increases risk.

## Manual Setup

You can skip the service helper and run `codex app-server` manually in a terminal. The terminal must stay open. If you want it to run in the background or at login, wire the command into your preferred service manager.

Create a token:

```sh
mkdir -p "$HOME/.codex"
umask 077
openssl rand -base64 32 > "$HOME/.codex/novascale-app-server-token"
chmod 600 "$HOME/.codex/novascale-app-server-token"
```

Start the app server on your Tailscale IP:

```sh
codex app-server \
  --listen ws://100.88.77.66:14500 \
  --ws-auth capability-token \
  --ws-token-file "$HOME/.codex/novascale-app-server-token"
```

For subnet-router mode, use `0.0.0.0` as the listen address and use the reachable subnet IP or hostname when creating the NovaScale host entry.

## Helper

Setup creates:

```text
~/.local/bin/novascale-codex
```

Commands:

```sh
novascale-codex status
novascale-codex restart
novascale-codex stop
novascale-codex notification-status
novascale-codex notification-restart
novascale-codex notification-stop
novascale-codex print-pairing
novascale-codex print-pairing --no-qr
novascale-codex rotate-token
novascale-codex uninstall
```

The `notification-*` commands are available in the unified helper but report
an error until the optional notification agent has been installed.

## Files

The default wrapper-only installation creates:

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

An explicitly enabled notification installation additionally creates or
updates:

```text
~/.codex/hooks.json
~/.local/bin/novascale-agent
~/.config/novascale-agent/config.json
~/.config/novascale-agent/host-key
~/.local/state/novascale-agent/
~/Library/LaunchAgents/dev.galaxnet.novascale.agent.plist
~/.config/systemd/user/novascale-agent.service
```

## Repository Structure

If you clone this repository instead of running the setup via `curl`:

- **`bin/novascale-codex`**: The service helper script. When running `./setup.sh` from a local clone, the setup script copies this file directly to the helper path. When installing via the `curl` piping method, the helper is generated from a copy embedded inside `setup.sh`.
- **`templates/`**: Contains static examples of the systemd service (`linux-systemd-user.service`) and macOS LaunchAgent (`macos-launchagent.plist`) configs. These are for manual setup reference only and are not read or executed by `setup.sh` (which generates service configs dynamically).

## Security

NovaScale Codex pairing is generated on your Codex host. It is not sent to GalaxNet or any website. NovaScale imports it locally and stores the token in iOS Keychain.

## Notification Agent Development

The repository contains the open-source host agent for NovaScale's independent
notification side channel. `novascale-agent` observes `PermissionRequest` and
`Stop`, discards all non-whitelisted content, queues the minimized event
locally, and uploads a signed event to the configured service. The hook always
returns `{}` and never supplies a Codex verdict.

The agent also provides an explicit `app-server restart` command for the host
user. It never restarts the wrapper automatically or exposes restart control
over the notification channel.

For notification-enabled setup, bootstrap downloads the pinned agent release
for the host platform. An already-enrolled agent keeps its existing identity:

```sh
./setup.sh --yes --enable-notifications
```

During an agent redeploy, setup asks the running daemon for its version and
compares the installed and incoming binaries. It restarts only the notification
daemon when the live version or binary is stale, after the binary, hooks, and
service definition are ready. The durable queue survives this restart, and the
Codex App Server wrapper is not restarted by the notification upgrade check.

For first enrollment, the app obtains a short-lived, single-use setup token and
writes it to a temporary `0600` file. Pass that protected file and the backend
URL. Bootstrap creates the host identity and stages enrollment locally; it does
not wait on a registration network request:

```sh
./setup.sh --yes --enable-notifications \
  --notification-endpoint https://<NOTIFICATION_ENDPOINT> \
  --notification-setup-token-file /path/to/protected/setup-token
```

The debug release currently pins agent `0.1.0-dev.1`. Use
`--agent-version <version>` to select another published immutable release.
Bootstrap downloads the exact platform archive and `SHA256SUMS` from the
corresponding `agent-v<version>` GitHub Release, rejects unexpected archive
paths or links, verifies the SHA-256 digest and embedded version, and never
falls back to an unsigned build. macOS additionally requires a valid Developer
ID signature, Gatekeeper assessment, and the expected universal architectures
before the binary is executed. A matching installed version is reused without
a network download.

The daemon keeps a protected `0600` copy outside its config, key file, queue,
and service definition while registration is pending. It proves possession of
the host private key, sends the token only as registration authorization,
retries transient failures across restarts, and removes the copy after success
or permanent rejection. The caller-owned temporary file can be deleted as
soon as bootstrap has staged it. Redeploying an active host preserves its
identity; redeploying a pending host continues the existing attempt.

Notification endpoints must use HTTPS. Plain HTTP is accepted only for
loopback development through `localhost`, `127.0.0.0/8`, or `::1`.

Setup adds only the exact `novascale-agent hook` handlers to the user's
existing `~/.codex/hooks.json`. Review and trust the resulting hook definition
with `/hooks` in Codex CLI. The hook reports events only and always returns
`{}`; Codex and the user retain the approval decision.

`--dev-agent` and `--agent-binary` remain explicit development overrides and
cannot be combined with `--agent-version`.

See [`docs/release-preparation.md`](docs/release-preparation.md) for the clean
Linux host matrix, backward-compatibility gate, and staged release checklist.
See [`notifications/README.md`](notifications/README.md) for the agent's data
minimization, local state, commands, and build instructions.

## Roadmap

APNs-based push notifications are being developed through Codex lifecycle hooks
and a dedicated outbound-only companion agent. The agent is not a proxy and
does not enter the authoritative App Server/Tailscale connection path.
