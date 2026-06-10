# Security

NovaScale Codex pairing is host-generated.

The pairing URI is generated on your Codex host. It is not sent to GalaxNet or any website. NovaScale imports it locally and stores the token in iOS Keychain.

Network listen mode exposes Codex app-server to whatever can reach the selected address and port. Prefer a Tailscale `100.64.0.0/10` interface address. Use `0.0.0.0` only on a trusted private network, behind Tailscale subnet routing, or in an environment where you understand the risk.
