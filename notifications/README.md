# NovaScale notification agent

This directory is the host-side, open-source portion of NovaScale remote push
notifications. It is a self-contained Go module that builds
`novascale-agent`.

The agent observes the Codex `PermissionRequest` and `Stop` hooks, keeps only a
small whitelist of opaque identifiers, queues sanitized events locally, signs
them with the host key, and uploads them to the configured notification
service. It never approves, denies, blocks, or continues a Codex action.

It does not transmit prompts, commands, tool input, patches, transcripts,
assistant output, working directories, or the Codex capability token. The hook
always writes `{}` and exits successfully even when input, IPC, or delivery
fails.

## Layout

```text
notifications/
├── cmd/novascale-agent/  host agent CLI and daemon
├── internal/agent/       hook normalization, IPC, identity, and durable queue
├── internal/protocol/    minimized event types
├── internal/signing/     host-event signing
├── internal/hookconfig/  additive Codex hook management
├── hooks/hooks.json      non-installing hook template
├── scripts/              release-input build tooling
├── third_party_licenses/ notices for packages linked into release binaries
├── go.mod
└── go.sum
```

## Validation and development builds

```sh
go test ./...
go vet ./...
./scripts/build-agent-release.sh
```

The build script produces macOS arm64/amd64, Linux arm64/amd64, and—when
`lipo` is available—a universal macOS binary under `dist/`. These are unsigned
release inputs. Tag-driven public releases are signed/notarized where
applicable, and `setup.sh` installs only a pinned release after checksum,
archive, platform, and embedded-version verification.

Binary release archives include the repository `LICENSE` and the dependency
notices collected under `third_party_licenses/`.

## Local data

The agent stores:

```text
~/.config/novascale-agent/config.json
~/.config/novascale-agent/host-key
~/.local/state/novascale-agent/agent.db
~/.local/state/novascale-agent/agent.sock
```

The private Ed25519 host key remains on the host with mode `0600`. `agent.db`
contains only the durable, capped outgoing event queue; successfully delivered
events are removed. It contains no APNs device token or Codex session content.

## Commands

```sh
novascale-agent init \
  --endpoint <HTTPS_URL> \
  --setup-token-file /path/to/protected/setup-token
novascale-agent switch-backend \
  --endpoint <NEW_HTTPS_URL> \
  --setup-token-file /path/to/protected/setup-token
novascale-agent serve
novascale-agent status
novascale-agent registration-state
novascale-agent endpoint
novascale-agent daemon-version
novascale-agent hooks install
novascale-agent hooks status
novascale-agent hooks uninstall
novascale-agent app-server update-status
novascale-agent app-server restart-if-updated
novascale-agent app-server restart
```

`app-server update-status` prints `current` or `restart_required` by comparing
the configured Codex executable and its resolved symbolic-link chain with the
running wrapper service's process start time. `restart-if-updated` restarts the
wrapper only for `restart_required`; an unavailable configuration, stopped
service, or inconclusive check fails without restarting it. These checks run
only when explicitly invoked. The agent daemon, notification hooks, and setup
upgrade check never restart the Codex App Server automatically, so an active
turn or goal is not interrupted in the background.

Notification endpoints must use HTTPS outside explicit loopback development.
The setup-token file must be a regular `0400` or `0600` file containing the
short-lived, single-use token issued for this installation. `init` performs no
network request: it copies the token to
`~/.config/novascale-agent/pending-setup-token` with mode `0600` and records a
pending enrollment. The caller may then remove its input file. `serve` owns
registration and retries transient failures with backoff across daemon
restarts. It removes the agent-owned token after success or permanent
rejection. A rejected or missing token leaves the state as
`needs_setup_token`, and rerunning `init` with a new token resumes enrollment.
Normal redeploys preserve active and pending host identities.

`switch-backend` preserves the existing host ID, Ed25519 private key, queued
events, Codex App Server capability token, and wrapper configuration. It
atomically stages a setup token issued by the destination backend, changes only
the notification endpoint and registration state, and lets the running daemon
perform signed registration with normal retry behavior. It is a no-op when an
active agent already uses the requested endpoint. Use `--force` only to
re-enroll after the same backend has lost or intentionally removed its host
registration.

When app-assisted bootstrap supplies both an endpoint and a newly issued setup
token for an already active host, bootstrap first compares that endpoint with
the agent's current configuration. A match uses the forced form, repairing the
registration relationship even if an operator previously edited the URL by
hand. A mismatch is preserved and requires an explicit `switch-backend`
command, so a Debug or Release app rebootstrap cannot silently move a test host
between sandbox and production. Both paths preserve the same host identity and
wrapper pairing.

`daemon-version` queries the live process over the same private, same-user IPC
socket used by hooks. Bootstrap uses it to restart the notification service
only when an installed binary update has left an older daemon running.

Hook installation preserves unrelated hook definitions. Review and trust the
installed definitions with `/hooks` in Codex CLI.
