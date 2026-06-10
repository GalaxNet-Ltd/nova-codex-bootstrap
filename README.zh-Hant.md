# NovaScale Codex Host

NovaScale Codex Host 是 NovaScale Codex 整合的主機端設定工具。它會設定使用者層級的 `codex app-server` 服務，建立 capability token，並列印可匯入 NovaScale iOS 的配對 URI。

此設定不會安裝第三方軟體。它使用主機上已安裝的官方 `codex` 指令，以及使用者既有的 Tailscale 網路。

## 設定

```sh
curl -fsSL https://raw.githubusercontent.com/GalaxNet-Ltd/nova-codex-bootstrap/refs/heads/main/setup.sh | sh
```

如果偵測到 `100.64.0.0/10` 網段內的介面位址，設定腳本會假定它是 Tailscale 位址，並顯示一個小型終端選單。建議選項會讓 `codex app-server` 只繫結到該 Tailscale 位址。

如果沒有偵測到 Tailscale 網段位址，請明確傳入位址：

```sh
./setup.sh --listen 100.88.77.66 --host 100.88.77.66
```

即使已安裝 `qrencode`，也可以強制輸出純文字 URI：

```sh
./setup.sh --no-qr
```

如果先前已經執行過設定，`setup.sh` 會顯示目前設定，並在重新設定前詢問確認。用於腳本或非互動式 SSH 重新設定時：

```sh
./setup.sh --yes
```

## 進階子網模式

僅當主機位於 Tailscale 子網路由後方，或位於可信任的私有網路中時，才使用 `0.0.0.0`：

```sh
./setup.sh --listen 0.0.0.0 --host 192.168.50.20 --port 14500
```

網路監聽模式會讓任何能存取所設定主機和連接埠的裝置連到 Codex app-server。雖然仍需要 capability token，但可達的網路暴露仍會增加風險。

## 手動設定

你可以跳過服務 helper，直接在終端機中手動執行 `codex app-server`。這種方式下終端機不能關閉。如果希望它在背景或登入時執行，請用你偏好的服務管理方式自行接入。

建立 token：

```sh
mkdir -p "$HOME/.codex"
umask 077
openssl rand -base64 32 > "$HOME/.codex/novascale-app-server-token"
chmod 600 "$HOME/.codex/novascale-app-server-token"
```

在你的 Tailscale IP 上啟動 app server：

```sh
codex app-server \
  --listen ws://100.88.77.66:14500 \
  --ws-auth capability-token \
  --ws-token-file "$HOME/.codex/novascale-app-server-token"
```

對於子網路由模式，請使用 `0.0.0.0` 作為監聽位址，並在 NovaScale 中建立主機項目時使用可存取的子網 IP 或主機名稱。

## Helper

設定腳本會建立：

```text
~/.local/bin/novascale-codex
```

指令：

```sh
novascale-codex status
novascale-codex restart
novascale-codex stop
novascale-codex print-pairing
novascale-codex print-pairing --no-qr
novascale-codex rotate-token
novascale-codex uninstall
```

## 檔案

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

## 儲存庫結構

如果你選擇 clone 儲存庫，而不是透過 `curl` 執行設定腳本：

- **`bin/novascale-codex`**：服務 helper 腳本。從本機 clone 執行 `./setup.sh` 時，設定腳本會直接複製此檔案到 helper 路徑。透過 `curl` 管線安裝時，helper 會由嵌入在 `setup.sh` 中的副本產生。
- **`templates/`**：包含 systemd 服務 (`linux-systemd-user.service`) 和 macOS LaunchAgent (`macos-launchagent.plist`) 的靜態範例設定。它們僅供手動設定參考，不會被 `setup.sh` 讀取或執行；`setup.sh` 會動態產生服務設定。

## 安全

NovaScale Codex 配對資訊在你的 Codex 主機上產生。它不會傳送到 GalaxNet 或任何網站。NovaScale 會在本機匯入該資訊，並將 token 儲存在 iOS Keychain 中。

## 路線圖

基於 APNs 的推播通知計畫在後續版本中加入。我們仍在評估是透過 Codex hooks 整合，還是在 `codex app-server` 前提供一個專用的開源主機代理。
