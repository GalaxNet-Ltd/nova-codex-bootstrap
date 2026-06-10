# Troubleshooting

## Codex Not Found

`codex` was not found. Setup checks `PATH` and common Codex CLI install locations such as `~/.nvm`, `~/.npm-global`, `~/.local/bin`, and Homebrew.

Install Codex CLI, log in with the account you want this host to use, then rerun setup.

## Port In Use

Port `14500` is already in use. Use `--port <another-port>`.

## Linux User Service Does Not Start After Reboot

You may need user service linger:

```sh
loginctl enable-linger "$USER"
```

This is not enabled automatically because it changes login and service lifecycle behavior.

## QR Missing

Install `qrencode`:

```sh
brew install qrencode
sudo apt install qrencode
sudo dnf install qrencode
sudo pacman -S qrencode
```

## Cannot Connect From NovaScale

Check service status, host reachability, listen mode, token correctness, and firewall rules for the selected port.

## NovaScale Shows 403

A `403` from the Codex host usually means the pairing token stored in NovaScale no longer matches the running Codex app server.

This can happen after switching Codex accounts on the host. Codex may expire or replace auth state behind the app server, while NovaScale still has the old host token saved in iOS Keychain.

Fix:

1. Delete the Codex host entry from NovaScale.
2. Rerun setup on the host and rotate the pairing token:

```sh
./setup.sh --yes --rotate-token
```

3. Import the new pairing URI in NovaScale.

If you are testing literal URI import:

```sh
./setup.sh --yes --rotate-token --no-qr
```
