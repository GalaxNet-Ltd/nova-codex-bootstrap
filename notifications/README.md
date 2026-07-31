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
release inputs. Public installation requires immutable versions, SHA-256
verification, and macOS signing/notarization.

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
novascale-agent serve
novascale-agent status
novascale-agent registration-state
novascale-agent daemon-version
novascale-agent hooks install
novascale-agent hooks status
novascale-agent hooks uninstall
novascale-agent app-server restart
```

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

`daemon-version` queries the live process over the same private, same-user IPC
socket used by hooks. Bootstrap uses it to restart the notification service
only when an installed binary update has left an older daemon running.

Hook installation preserves unrelated hook definitions. Review and trust the
installed definitions with `/hooks` in Codex CLI.
