# Release preparation

This repository has two independently enabled host components:

1. the established Codex App Server wrapper; and
2. the notification agent, installed and autonomously enrolled by default.

Publishing the agent source must not break existing users. Wrapper-only setup
remains available through the explicit `--no-notifications` option. Installing
the host agent does not activate remote APNs delivery; app installation
registration, entitlement, host association, and the remote-push toggle remain
separate requirements.

## Compatibility contract

| App and host combination | Required behavior |
| --- | --- |
| Existing app + default bootstrap | Pairing and Codex access continue unchanged; the host agent enrolls autonomously, but an existing app does not activate remote APNs delivery. |
| Existing app + notification-capable source | Existing host operations and local notification behavior continue; remote delivery remains inactive without app installation registration and an explicit app toggle. |
| Updated app + wrapper-only host | Codex access works; the app reports remote notifications as unavailable for that host. |
| Updated app + opted-in host | The agent observes trusted hooks and uploads minimized, signed lifecycle events. |
| Updated app + new host | Host notification support defaults on for every user; the user can choose the lean `--no-notifications` path. Remote delivery remains a separate Pro subscription setting. |

The default invocation must continue to pass `scripts/verify-release.sh`. It
resolves the latest stable agent and stages autonomous enrollment without requiring an
app-provided token or other enrollment input:

```sh
curl -fsSL https://raw.githubusercontent.com/GalaxNet-Ltd/nova-codex-bootstrap/refs/heads/main/setup.sh | sh
```

The explicit wrapper-only invocation must remain available:

```sh
curl -fsSL https://raw.githubusercontent.com/GalaxNet-Ltd/nova-codex-bootstrap/refs/heads/main/setup.sh | sh -s -- --no-notifications
```

## Clean Linux VM validation

On a disposable Linux VM with no NovaScale files in the test user's home:

```sh
./scripts/verify-release.sh
./setup.sh
```

Verify the explicit `--no-notifications` path installs only the wrapper, while
default setup installs the agent and stages autonomous enrollment. Existing app
pairing must still work, prompting and approvals must remain unchanged, and
uninstall must remove the selected services.

After publishing the stable agent release, run notification validation
separately:

```sh
notifications/scripts/build-agent-release.sh
./setup.sh --enable-notifications \
  --notification-endpoint https://<NOTIFICATION_ENDPOINT>
```

Verify both hook events, neutral `{}` output, queue retry across a temporary
endpoint outage, autonomous enrollment retry across a daemon restart, absence
of a persistent enrollment token, reboot persistence, and
preservation of unrelated hooks. Bootstrap must only stage enrollment; the
daemon performs the signed registration and proves possession of the locally
generated signing key.

## Source publication gate

Before the human maintainer commits:

```sh
./scripts/verify-release.sh
git diff --check
git status --short
```

Review every tracked candidate for populated environment files, APNs keys,
device tokens, bearer or enrollment tokens, host private keys, databases, runtime
logs, local credential paths, and generated binaries.

## Agent binary gate

The development build script creates unsigned release inputs and accepts a
safe `VERSION` value for local release rehearsal:

```sh
VERSION=0.1.0-pre.1 notifications/scripts/build-agent-release.sh
```

`.github/workflows/agent-release.yml` implements the public artifact pipeline.
It runs only for `agent-v*` tag pushes. The initial signing rehearsal used a
temporary exact-branch trigger; that trigger must not remain in the permanent
release workflow. Tag runs:

1. inject the tag version into the Go binary;
2. build Linux arm64/amd64 and a universal macOS binary;
3. sign the macOS executable with Developer ID Application and hardened
   runtime;
4. submit its ZIP archive with `notarytool` and require an accepted result;
5. include the repository license and linked Go dependency notices in every
   binary archive;
6. publish the archives and one `SHA256SUMS` file from the same workflow.

Create a protected GitHub Environment named `agent-release` and allow only
`agent-v*` tags. A future branch rehearsal must use a temporary exact-branch
workflow trigger and matching environment rule, and both must be removed after
the rehearsal. Require a separate maintainer review and prevent self-approval
when the repository has more than one maintainer; a sole maintainer must not
enable a self-review restriction that would make releases impossible.
Configure these as environment secrets—not repository-level secrets—without
placing their values in the repository, logs, or workflow inputs:

```text
MACOS_DEVELOPER_ID_APPLICATION_P12_BASE64
MACOS_DEVELOPER_ID_APPLICATION_P12_PASSWORD
APPLE_NOTARY_KEY_P8_BASE64
APPLE_NOTARY_KEY_ID
APPLE_NOTARY_ISSUER_ID
```

The workflow reconstructs the certificate and notary key only in the
ephemeral runner directory, imports the certificate into an ephemeral
keychain, and removes both after signing. It parses the `notarytool` result and
fails unless the terminal status is exactly `Accepted`. Apple supports
notarizing a ZIP but does not support stapling a ticket directly to the ZIP, so
the workflow relies on the accepted online notarization ticket and verifies
the inner executable's Developer ID signature before upload. Because the agent
is a standalone executable rather than an app bundle, both release and
bootstrap use `codesign --check-notarization` to verify that ticket.

Automatic bootstrap installation queries this repository's GitHub
`releases/latest` API and accepts only a published, non-prerelease
`agent-v<stable-semantic-version>` tag. It downloads only from the
corresponding HTTPS GitHub Release URL, verifies the matching `SHA256SUMS`
entry, rejects unexpected archive paths and links, requires the embedded
version to match, and fails closed instead of falling back to a repository
build or unsigned file. macOS also requires Developer ID signature and
notarization-ticket verification, plus both universal architectures, before
execution. `--agent-version` supports an explicitly selected immutable
release and bypasses latest discovery; an already installed matching explicit
version is usable offline.

GitHub Releases in this repository are reserved for stable `agent-v*`
publishing. A newer non-agent, draft, or prerelease entry must never become the
default bootstrap input; setup rejects it before modifying an installation.

Keep `--dev-agent` available only as an explicit developer path.

Host maintenance should invoke `setup.sh --notifications-only`. Release
verification must prove that this path preserves the enrolled agent identity
and backend, updates only the agent, hooks, and agent service, and never reads,
rewrites, stops, or restarts the Codex wrapper configuration, capability token,
helper, service, or process. A stale agent may restart after the replacement is
ready; `--no-start` must leave its current daemon untouched.

## Rollout order

Deploy backend autonomous-enrollment support before enabling fresh notification
enrollment in bootstrap. That backend change does not alter authentication for
already-enrolled hosts sending signed events.

1. Tag the reviewed commit as `agent-v<VERSION>` and publish the signed
   host-agent release without first moving the public bootstrap branch.
2. Verify the release archives, `SHA256SUMS`, macOS signature, and accepted
   notarization ticket.
3. Confirm GitHub reports `agent-v<VERSION>` as the latest stable release,
   then push the bootstrap branch and validate default bootstrap with an
   existing app release.
4. Point the debug app bootstrap sheet at this branch, leave notifications on
   by default, and validate autonomous host enrollment and lifecycle
   notifications with a test app.
5. Release updated app support while keeping notifications opt-in per host.

If notification rollout is paused, wrapper access must remain usable. Stopping
or uninstalling the notification agent must not rotate the Codex capability
token or change the App Server protocol.
