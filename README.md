# NovaScale Codex Host

[English](README.md) | [简体中文](README.zh-Hans.md) | [繁體中文](README.zh-Hant.md)

Host-side setup utility for NovaScale Codex integration. It configures a user-level `codex app-server` service, creates a capability token, and prints a pairing URI for NovaScale iOS.

The wrapper uses the official `codex` command already installed on the host and the user's existing Tailscale networking mesh. When remote notifications are enabled, setup also installs NovaScale's open-source notification agent from a signed, pinned release.

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

Host notification support is enabled by default for every user. The updated
bootstrap downloads the pinned signed agent release, creates the host identity,
and lets the agent enroll itself through the public production endpoint. This
requires no app, APNs token, or transferred enrollment credential. It prepares
the host but does not activate paid remote delivery. Remote Push is enabled
separately in Codex Settings and requires a Pro subscription.
Turning host notification support off passes `--no-notifications` and keeps the
host on the lean wrapper-only path.

**Availability:** Remote Push requires a NovaScale Pro subscription and
NovaScale 1.6.0 or later. Version 1.6.0 will be released soon.

Older apps remain compatible because notification enrollment is an
observational side channel and does not change wrapper pairing or the Codex App
Server protocol. To choose the wrapper-only path explicitly, use:

```sh
./setup.sh --no-notifications
```

This compatibility fallback lets existing NovaScale versions continue to use
Codex hosts, while hosts created before notification-aware bootstrap can be
redeployed later without deleting, repairing, or re-adding them.

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
novascale-codex update-status
novascale-codex restart-if-updated
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
an error until the notification agent has been installed.

## Files

The wrapper installation creates:

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

A notification-enabled installation additionally creates or updates:

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

## Privacy-Preserving Notification Titles

The host agent and notification backend never receive a thread title, prompt,
assistant response, tool input, command, patc, working directory, or transcript
path. The agent emits only the lifecycle event type, timestamp, and non-content
identifiers needed to correlate the host, thread, turn, and approval request.
Each event is signed by the host key before upload.

The APNs payload contains a generic notification plus opaque event, host,
thread, and turn identifiers. NovaScale keeps a small, time-limited mapping from
host and thread identifiers to titles in the device's protected App Group
container. Its notification service extension uses that local mapping to add a
title after delivery. If no local title is available, the generic notification
is shown. Thread titles therefore do not pass through the notification backend
or APNs. APNs device tokens are encrypted at rest by the notification backend.

## Notification Agent Development

The repository contains the open-source host agent for NovaScale's independent
notification side channel. `novascale-agent` observes `PermissionRequest` and
`Stop`, discards all non-whitelisted content, queues the minimized event
locally, and uploads a signed event to the configured service. The hook always
returns `{}` and never supplies a Codex verdict.

The agent provides explicit `app-server update-status` and
`app-server restart-if-updated` commands for host-side maintenance, exposed by
the installed helper as `novascale-codex update-status` and
`novascale-codex restart-if-updated`. The first
reports whether the configured Codex executable changed after the running
wrapper started. The second restarts only when that check reports an update.
An unknown state never triggers a restart. The agent never restarts the wrapper
automatically or exposes restart control over the notification channel, so it
cannot interrupt a long-running turn or goal in the background.

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

For first enrollment, bootstrap creates the host identity locally and stages
the daemon for autonomous registration. The production endpoint is the default;
an explicit endpoint is needed only for local development or another backend:

```sh
./setup.sh --yes --enable-notifications \
  --notification-endpoint https://<NOTIFICATION_ENDPOINT>
```

The bootstrap currently pins stable agent `0.1.3`. Use
`--agent-version <version>` to select another published immutable release.
Bootstrap downloads the exact platform archive and `SHA256SUMS` from the
corresponding `agent-v<version>` GitHub Release, rejects unexpected archive
paths or links, verifies the SHA-256 digest and embedded version, and never
falls back to an unsigned build. macOS additionally requires a valid Developer
ID signature, notarization ticket, and the expected universal architectures
before the binary is executed. A matching installed version is reused without
a network download.

The daemon proves possession of the host private key to obtain a short-lived,
single-use token bound to the same host ID and public key, keeps it only in
memory, and consumes it in a separately signed registration request. It retries
transient failures across restarts. Redeploying an active or pending host
preserves its host ID, private key, queue, and backend. Changing the backend
requires the explicit `novascale-agent switch-backend --endpoint URL` command.

Notification endpoints must use HTTPS. Plain HTTP is accepted only for
loopback development through `localhost`, `127.0.0.0/8`, or `::1`.

Setup adds only the exact `novascale-agent hook` handlers to the user's
existing `~/.codex/hooks.json`. Notifications remain inactive until those exact
definitions have been reviewed and trusted, and Codex has loaded them. After
setup, open Codex CLI on the host, run `/hooks`, and review and trust the
NovaScale `PermissionRequest` and `Stop` hooks. If the Codex App Server is
already running, wait for active turns and approvals to finish, then restart it
after trusting the hooks before testing a new or existing thread:

```sh
novascale-codex restart
```

The hook reports events only and always returns `{}`; Codex and the user retain
the approval decision.

`--dev-agent` and `--agent-binary` remain explicit development overrides and
cannot be combined with `--agent-version`.

See [`docs/release-preparation.md`](docs/release-preparation.md) for the clean
Linux host matrix, backward-compatibility gate, and staged release checklist.
See [`notifications/README.md`](notifications/README.md) for the agent's data
minimization, local state, commands, and build instructions.

## Architecture

APNs-based push notifications use Codex lifecycle hooks and a dedicated
outbound-only companion agent. The agent is not a proxy and does not enter the
authoritative App Server/Tailscale connection path.
