# Troubleshooting

## Codex Not Found

`codex` was not found in `PATH`. Install Codex CLI first, then rerun the setup.

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

## Cannot Connect From NovaAccess

Check service status, host reachability, listen mode, token correctness, and firewall rules for the selected port.
