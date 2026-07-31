# Release preparation

This repository has two independently enabled host components:

1. the established Codex App Server wrapper; and
2. the optional notification agent.

Publishing the agent source must not turn notifications on for existing users.
The wrapper-only bootstrap remains the compatibility baseline.

## Compatibility contract

| App and host combination | Required behavior |
| --- | --- |
| Existing app + default bootstrap | Pairing and Codex access continue unchanged; no agent or hooks are installed. |
| Existing app + notification-capable source | Existing host operations continue; notifications remain disabled unless the host user opts in. |
| Updated app + wrapper-only host | Codex access works; the app reports remote notifications as unavailable for that host. |
| Updated app + opted-in host | The agent observes trusted hooks and uploads minimized, signed lifecycle events. |

The default invocation must continue to pass `scripts/verify-release.sh`. It
must not require notification enrollment, an endpoint, hooks, or an agent
binary:

```sh
curl -fsSL https://raw.githubusercontent.com/GalaxNet-Ltd/nova-codex-bootstrap/refs/heads/main/setup.sh | sh
```

## Clean Linux VM validation

On a disposable Linux VM with no NovaScale files in the test user's home:

```sh
./scripts/verify-release.sh
./setup.sh
```

Verify that only the wrapper service is installed, no notification agent state
or hooks exist, existing app pairing still works, prompting and approvals are
unchanged, and uninstall removes the wrapper service.

Run notification validation separately. Until signed artifacts exist, build
locally and keep the development flag explicit:

```sh
notifications/scripts/build-agent-release.sh
./setup.sh --enable-notifications --dev-agent \
  --notification-endpoint https://<NOTIFICATION_ENDPOINT> \
  --notification-setup-token-file /path/to/protected/setup-token
```

Verify both hook events, neutral `{}` output, queue retry across a temporary
endpoint outage, enrollment retry across a daemon restart, removal of the
agent-owned pending token after enrollment, reboot persistence, and
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
device tokens, bearer or setup tokens, host private keys, databases, runtime
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
the inner executable's Developer ID signature before upload.

Automatic bootstrap installation remains gated until setup can download a
pinned immutable version, authenticate `SHA256SUMS`, and fail closed instead
of falling back to a repository build or unsigned file.

Keep `--dev-agent` available only as an explicit developer path.

## Rollout order

Deploy backend setup-token support before enabling fresh notification
enrollment in the app. That backend change does not alter authentication for
already-enrolled hosts sending signed events.

1. Publish the backward-compatible wrapper and agent source.
2. Validate default bootstrap with an existing app release.
3. Publish a signed host-agent prerelease.
4. Validate app-issued, single-use setup-token enrollment and lifecycle
   notifications with a test app.
5. Release updated app support while keeping notifications opt-in per host.

If notification rollout is paused, wrapper access must remain usable. Stopping
or uninstalling the notification agent must not rotate the Codex capability
token or change the App Server protocol.
