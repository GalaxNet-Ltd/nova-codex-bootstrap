# NovaScale notification agent

This directory is the host-side, open-source portion of NovaScale remote push
notifications. It is a self-contained Go module that builds
`novascale-agent`.

The shared agent observes Codex `PermissionRequest` / `Stop` hooks and an
optional Herdr plugin, keeps only a
small whitelist of opaque identifiers and documented Herdr presentation fields, queues bounded events locally, signs
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
contains the durable, capped outgoing event queue plus Herdr's local binding
and transition state; successfully delivered events are removed. Herdr socket
paths remain local in the binding table. Herdr events may contain bounded pane titles, agent labels and state labels as described below; terminal content and filesystem-path fields are excluded. The database contains no APNs token.
`XDG_CONFIG_HOME` and `XDG_STATE_HOME` override the respective bases. Bootstrap
records these bases in the notification service so CLI setup and the daemon
continue using the same identity and queue.

## Commands

```sh
novascale-agent init --endpoint <HTTPS_URL>
novascale-agent switch-backend \
  --endpoint <NEW_HTTPS_URL>
novascale-agent serve
novascale-agent status
novascale-agent registration-state
novascale-agent endpoint
novascale-agent daemon-version
novascale-agent capabilities
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
`init` performs no network request: it creates the host ID and private key when
needed and records pending enrollment. `serve` proves possession of that key to
request a short-lived token bound to the host ID and public key, keeps it only
in memory, and consumes it in a separately signed registration request. It
retries transient failures with backoff across daemon restarts. Agents upgrading
from the prerelease `needs_setup_token` state automatically resume using the
same identity. An already-active agent repeats the signed registration once
after daemon startup so the backend learns the running agent version; this
preserves the existing host ID and private key. Normal redeploys preserve active
and pending host identities.

Use `novascale-agent switch-backend production` or `novascale-agent switch-backend staging` for the bundled service endpoints. `--endpoint URL` remains available for an explicit custom endpoint; do not combine it with a preset. Put flags before the preset, for example `switch-backend --force staging`.

Switching preserves the host ID, Ed25519 private key, session identities and wrapper configuration. It changes the endpoint and registration state; the daemon reenrolls asynchronously with retry. Events remain bound to the backend where they were captured and are uploaded only when that backend is active again, subject to normal queue expiry/limits. Existing untagged events bind to the old endpoint before the switch. `--force` reenrolls after the same backend has lost its registration.

Before downgrading to an agent that predates backend-bound queues, run the current agent’s `uninstall` to clear pending events while retaining identity, then install the old version. An old binary cannot honor queue destinations; do not let it open a mixed-backend queue.

Only switch between trusted services. The signing format does not include a backend audience: retaining the same key on two services does not provide cryptographic isolation between them. Phone preferences/credentials must be set separately for each service.

`novascale-agent uninstall --dry-run` previews removal. `uninstall` stops and removes the notification service, its exact Codex hook commands, Herdr plugin and executable, and clears pending events/capture. By default it keeps the host ID, signing key, configuration and opaque session identities for reinstall. Add `--purge` to remove those too. Use `--hooks-file PATH` for a non-default Codex hooks file. Unrelated hooks and the Codex App Server wrapper remain untouched; backend records are not deleted. If a manually started agent is still running, stop it before uninstalling. Plugin removal failure stops cleanup before deleting the executable; resolve it and retry.

Bootstrap preserves the configured backend on every ordinary rerun. If an
explicitly requested endpoint differs, setup leaves the existing identity and
backend unchanged and directs the operator to `switch-backend`. A Debug or
Release app rebootstrap therefore cannot silently move a host between sandbox
and production.

`daemon-version` queries the live process over the same private, same-user IPC
socket used by hooks. Bootstrap uses it to restart the notification service
only when an installed binary update has left an older daemon running.

Hook installation preserves unrelated hook definitions. Review and trust the
installed definitions with `/hooks` in Codex CLI.

## Herdr notifications

The adapter uses Herdr's typed API and plugin contract, with a 0.9.1 minimum;
the current real-producer and system integration fixtures pin Herdr 0.9.5. Install
one shared agent and user service; a second daemon or a Codex installation is
not required. Install the integration once for the OS user, even before any
Herdr session is running. Capture covers existing and future sessions; the
backend applies each phone's exact watches or selected-host consent.

For stable Herdr 0.9.2 and later, capture uses `agent.list` completion evidence
to detect short work that finishes between observations. The running server's
typed `ping` version determines support, independently of the installed binary.
An omitted `completion_seq` on these servers suppresses a progress notification;
0.9.1 retains the observed working-to-idle/done fallback. First installation,
restart and pane replacement establish baselines without replaying old work.
Completion evidence stays local; signed events and backend consent/quota rules
are unchanged.

```sh
novascale-agent herdr status --json
novascale-agent herdr install --json
novascale-agent herdr test --installation <OPAQUE_INSTALLATION_ID> --json
novascale-agent herdr uninstall --json
```

Host status reports `hostReady` independently of any session. An optional
`herdr status --session <name>` (or `--socket <path>`) resolves/checks an exact
local session and returns its opaque `sessionId` for a phone's watch. The same
optional selector can target a test; it never limits capture. Older app install
commands with `--session` remain compatible, but installation is always global.
Status reports installed/live agent versions and capabilities,
endpoint/registration, plugin readiness, and an optional exact-session binding.
Installed binary version alone cannot prove that the running daemon supports
Herdr. Ordinary setup checks both and restarts only the notification service
when necessary, preserving existing Codex hooks and the wrapper process.

Installation writes a NovaScale-owned manifest under the private agent config
directory and links it through Herdr's typed plugin API. The manifest contains
an absolute argv command for `novascale-agent herdr-hook`, with no shell command
or network call. The registry is global to this host's OS user: existing named
sessions see the linked plugin, and unlinking it stops Herdr observations for
all sessions/devices using that account. The agent uses Herdr's own local
registry CLI for global installation/removal; it does not edit Herdr's registry
files or launch a server. `herdr-host-capture-v1` identifies a live daemon that
can capture unvisited sessions automatically. Uninstall disables local Herdr
bindings and discards their queued hints while leaving Codex hooks, host keys,
agent service and Codex events intact. Device watch opt-out is separate.

The hook always returns `{}` successfully, validates the bounded dotted trigger
and snake-case envelope, then sends a binding digest, pane ID and originating
socket locator over bounded same-user local IPC. The locator stays on the host;
it is never part of a signed event or backend/APNs payload. A startup hook
establishes a baseline for new servers, and canonical socket discovery covers
already running servers. Automatic binding validates the same-user Unix socket
and typed state. Persisted adapter consent prevents late hooks from reenabling
capture after uninstall. Existing opaque binding IDs survive upgrade/reinstall.
The hook never approves, denies or changes a Herdr or
agent action. The daemon reads typed `agent.list` metadata and reconciles
terminal identity and state sequence, so callback ordering, repeated callbacks,
renames and seen/focus changes cannot create repeated completion hints.

The first observation of each pane/terminal is a silent baseline for every
agent, including one first discovered already blocked. This also silences a real
approval if it is the first observed state. Later transitions into `blocked`
produce an attention hint once per observed episode; prior observed working is
not required. An agent first seen working can notify on its first later approval.
The existing durable pane ledger preserves observations across reconnects and
restarts. No agent-specific startup rules, screen reads or quiet timer are used.
Observed `working` returning to `idle`/`done` produces a progress hint; it does
not prove task success. Cold start, reconnect and socket replacement establish
a baseline and do not replay historical state. A local socket inode/mtime plus
terminal IDs is a conservative incarnation heuristic, not a verified Herdr
boot ID. Asynchronous hooks, their runtime concurrency cap, very brief states,
service downtime and network failures can lose observations; this channel is
best effort and must never gate work.

Herdr outgoing events use schema 2 with `source: "herdr"`, semantic event type,
timestamp and `target.sessionId` / `paneId`. The session ID is a durable random
binding, not the remote session name/path. A typed Codex agent session with
`kind: "id"` and a validated UUID may add `codexThreadId`; paths and agent labels
never become routing IDs. Preserve this correlation even though the Herdr
baseline policy is agent-independent: matching Codex coverage takes precedence
per phone before quota is charged, while phones without matching Codex coverage
can still receive Herdr. Independent Codex hooks, event bodies and schema-1
signing are unchanged; Herdr's first observation does not suppress a Codex hook.
Targeted test hints use only session and installation IDs.

Attention/progress hints also carry optional `herdrMetadata`, limited to the
remaining documented `pane.agent_status_changed` fields: `workspaceId`,
`agentStatus`, `agent`, `title`, `displayAgent`, and `stateLabels`. Together with
`target.paneId`, these cover the hook's seven documented fields. The daemon
reads the corresponding typed snapshot; it does not upload raw hook data or
arbitrary `agent.list` fields. Metadata-only renames do not emit another hint.

`herdrMetadata.sessionName` also carries Herdr's native managed-session name
(for example `test`), matching `herdr session list --json`. Herdr 0.9.5 exposes
that name through its managed session registry/layout rather than the hook or
snapshot. The agent uses the same validated canonical binding as its SSH
lookup; arbitrary custom socket paths are never turned into names or uploaded.
The name is optional, at most 64 ASCII bytes using Herdr's own name rules, and
counts toward the same metadata budget. Phones can identify an unopened session
while routing and subscriptions continue to use only the opaque session ID.

Limits are 256 UTF-8 bytes for workspace ID/title, 64 for agent/display agent,
and 96 per supported status label (`working`, `blocked`, `idle`, `done`,
`unknown`), with a 1,200-byte encoded JSON budget. Display controls are stripped
and text may be shortened further to fit. An excessively escaped workspace ID
may be omitted; routing still uses the unchanged session/pane target. Titles
and labels are free-form and may contain private project information. This is
an intentional change from identifier-only Herdr notifications, not a promise
that user-supplied labels can never contain a secret. No prompt, command,
transcript, token or working-directory fields are collected.

A backend that rejects the optional field with HTTP 400 is retried once using
the legacy event body and the same event ID. This preserves delivery during a
backend/agent rollout without altering Codex events or signing.

Herdr hints expire after 30 minutes. Per-session/global caps and fair dequeueing
keep a noisy Herdr session from evicting or starving Codex. Event IDs survive
HTTP retries. Rejected or stale hints are not replayed on a later day. The
service decides recipient subscriptions and delivery policy; the host knows
neither APNs tokens nor purchase state.

Additional verification:

```sh
go test -race ./...
python3 scripts/test-herdr-hook-contract.py
../scripts/test-notification-agent-upgrade.sh
```

The hook-contract fixture starts two private XDG-isolated Herdr servers and
removes only its own processes/configuration. It does not modify active user
Herdr sessions or Codex hooks. The upgrade fixture stubs service control in a
temporary namespace; it does not restart the developer's agent or wrapper.

## Resolving a passive Herdr notification locally

`novascale-agent herdr bindings --json` returns the configured host identity,
notification endpoint, and enabled `sessionId` / `sessionName` mappings for
canonical default/named Herdr sockets. This is a typed read for the user's
SSH management connection; it does not attach a terminal, install hooks, or
change notification preferences. Custom sockets without a canonical session
name are omitted. The live daemon advertises `herdr-bindings-v1` so the app can
offer an upgrade when this lookup is unavailable.

Session names stay on the host and the authenticated SSH client. They are not
added to hook IPC, signed host events, or push payloads. The same notification
agent still serves both Codex and Herdr, independently of recipient scope.

### Phone association and host identity

The phone associates the registered host ID, its own installation and a source directly with the notification service. There is no host authorization CLI, phone pairing proof or required SSH round trip for notification settings. SSH remains useful for explicit agent installation/update and support inspection.

Host IDs are random UUID v4 values persisted with their signing key. Knowing a registered ID and owning a valid phone installation is sufficient to request coverage; do not publish IDs as a host directory. Association alone does not enable delivery. The service still applies each phone's source preference, exact watches or selected hosts, presence and quota. Host events remain signed and verified.

Upgrades reuse the same notification identity for Codex and Herdr. Another OS account's separate installation has its own identity. Upgrading the app and service must be coordinated: older apps that require the removed proof endpoint and older services that enforce it are not compatible with the new association flow.
