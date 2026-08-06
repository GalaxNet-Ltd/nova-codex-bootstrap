# NovaScale Codex Host

[English](README.md) | [简体中文](README.zh-Hans.md) | [繁體中文](README.zh-Hant.md)

NovaScale Codex Host 是 NovaScale Codex 整合的主機端設定工具。它會設定使用者層級的 `codex app-server` 服務，建立 capability token，並列印可匯入 NovaScale iOS 的配對 URI。

App Server wrapper 使用主機上已安裝的官方 `codex` 指令和使用者既有的 Tailscale 網路。啟用遠端通知時，設定腳本也會從固定版本的已簽署 Release 安裝 NovaScale 的開源通知 agent。

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

重新部署會保留現有 capability token，因此已配對的 App 可以繼續使用。
只有明確傳入 `--rotate-token`，或不存在可用 token 時，設定腳本才會更換
token。

### 向後相容性

所有使用者的主機通知支援都預設開啟。新版 App 會提供通知後端位址和短效
setup-token 檔案，bootstrap 隨後會自動下載並註冊固定版本的已簽署 agent。
這只會讓主機具備通知能力，不會啟用付費的遠端通知傳遞。遠端推播需要在
Codex 設定中另外啟用，並且需要 Pro 訂閱。關閉主機通知支援會傳入
`--no-notifications`，讓主機保持精簡的 wrapper-only 安裝。

舊版 App 和手動呼叫不會提供註冊憑據。此時設定腳本會提示通知註冊不可用，
並繼續完成 wrapper-only 安裝，而不會失敗。也可以明確選擇該路徑：

```sh
./setup.sh --no-notifications
```

因此舊版 NovaScale 仍可繼續使用 Codex 主機。對於在通知版 bootstrap 之前
建立的主機，之後只需重新部署；不需要刪除、修復或重新加入主機。

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
novascale-codex notification-status
novascale-codex notification-restart
novascale-codex notification-stop
novascale-codex print-pairing
novascale-codex print-pairing --no-qr
novascale-codex rotate-token
novascale-codex uninstall
```

統一 helper 中會提供 `notification-*` 指令；安裝通知 agent 後這些指令
才可使用。

## 檔案

App Server wrapper 會建立：

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

啟用通知時還會建立或更新：

```text
~/.codex/hooks.json
~/.local/bin/novascale-agent
~/.config/novascale-agent/config.json
~/.config/novascale-agent/host-key
~/.local/state/novascale-agent/
~/Library/LaunchAgents/dev.galaxnet.novascale.agent.plist
~/.config/systemd/user/novascale-agent.service
```

## 儲存庫結構

如果你選擇 clone 儲存庫，而不是透過 `curl` 執行設定腳本：

- **`bin/novascale-codex`**：服務 helper 腳本。從本機 clone 執行 `./setup.sh` 時，設定腳本會直接複製此檔案到 helper 路徑。透過 `curl` 管線安裝時，helper 會由嵌入在 `setup.sh` 中的副本產生。
- **`templates/`**：包含 systemd 服務 (`linux-systemd-user.service`) 和 macOS LaunchAgent (`macos-launchagent.plist`) 的靜態範例設定。它們僅供手動設定參考，不會被 `setup.sh` 讀取或執行；`setup.sh` 會動態產生服務設定。

## 安全

NovaScale Codex 配對資訊在你的 Codex 主機上產生。它不會傳送到 GalaxNet 或任何網站。NovaScale 會在本機匯入該資訊，並將 token 儲存在 iOS Keychain 中。

## 保護隱私的通知標題

主機 agent 和通知後端都不會收到 thread 標題、prompt、assistant 回覆、
工具輸入、指令、patch、工作目錄或 transcript 路徑。agent 只傳送生命週期
事件類型、時間戳，以及關聯主機、thread、turn 和核准請求所需的非內容識別碼。
每個事件都會先由主機金鑰簽署，再上傳到通知服務。

APNs payload 只包含通用通知文案，以及不透明的 event、host、thread 和 turn
識別碼。NovaScale 會在裝置受資料保護的 App Group 容器中保存一份小型、
有時限的 host/thread 識別碼到標題的對應。通知服務延伸功能在收到通知後使用
這份本機對應補上標題；如果裝置上沒有對應標題，就顯示通用通知。因此 thread
標題不會經過通知後端或 APNs。通知後端會加密儲存 APNs device token。

## 通知 Agent

儲存庫包含 NovaScale 獨立通知旁路的開源主機 agent。`novascale-agent` 只
觀察 `PermissionRequest` 和 `Stop`，丟棄未列入白名單的內容，在本機排隊
最小化事件，並上傳簽署事件。hook 始終回傳 `{}`，不會替 Codex 作出任何
決定。

啟用通知的 bootstrap 會下載適合主機平台的固定 agent Release。已註冊的
agent 會保留現有身分。首次註冊使用由 App 取得的短效、一次性 setup token；
App 將 token 寫入受保護的 `0600` 暫存檔，bootstrap 只在本機暫存註冊，
agent daemon 隨後在背景註冊並自動重試。註冊完成或遭永久拒絕後，agent 會
刪除自己的 token 副本。

目前除錯流程固定使用 agent `0.1.0-dev.5`。bootstrap 會驗證平台封裝、
`SHA256SUMS`、封裝路徑和內嵌版本，且不會回退到未簽署建置。macOS 還會驗證
Developer ID 簽章、公證票據及預期的 universal 架構。

通知端點必須使用 HTTPS。只有透過 `localhost`、`127.0.0.0/8` 或 `::1`
存取的 loopback 開發服務可以使用 HTTP。

設定腳本只向現有 `~/.codex/hooks.json` 加入精確的 `novascale-agent hook`
處理器。請在 Codex CLI 中使用 `/hooks` 檢查並信任產生的 hook 定義。

詳見 [`docs/release-preparation.md`](docs/release-preparation.md) 和
[`notifications/README.md`](notifications/README.md)。

## 架構

基於 APNs 的推播通知使用 Codex 生命週期 hooks 和獨立、僅出站的 companion
agent。此 agent 不是代理，也不會進入權威 App Server/Tailscale 連線路徑。
