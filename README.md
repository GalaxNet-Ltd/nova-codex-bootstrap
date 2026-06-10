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
novascale-codex print-pairing
novascale-codex print-pairing --no-qr
novascale-codex rotate-token
novascale-codex uninstall
```

## Files

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

## Repository Structure

If you clone this repository instead of running the setup via `curl`:

- **`bin/novascale-codex`**: The service helper script. When running `./setup.sh` from a local clone, the setup script copies this file directly to the helper path. When installing via the `curl` piping method, the helper is generated from a copy embedded inside `setup.sh`.
- **`templates/`**: Contains static examples of the systemd service (`linux-systemd-user.service`) and macOS LaunchAgent (`macos-launchagent.plist`) configs. These are for manual setup reference only and are not read or executed by `setup.sh` (which generates service configs dynamically).

## Security

NovaScale Codex pairing is generated on your Codex host. It is not sent to GalaxNet or any website. NovaScale imports it locally and stores the token in iOS Keychain.

## Roadmap

APNs-based push notifications are planned for an upcoming release. We are still deciding whether to integrate through Codex hooks or ship a dedicated open-source host proxy in front of `codex app-server`.
