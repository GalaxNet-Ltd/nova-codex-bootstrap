# NovaScale Codex Host

NovaScale Codex Host 是 NovaScale Codex 集成的主机端设置工具。它会配置用户级 `codex app-server` 服务，创建 capability token，并打印可导入 NovaScale iOS 的配对 URI。

该设置不会安装第三方软件。它使用主机上已经安装好的官方 `codex` 命令，以及用户已有的 Tailscale 网络。

## 设置

```sh
curl -fsSL https://raw.githubusercontent.com/GalaxNet-Ltd/nova-codex-bootstrap/refs/heads/main/setup.sh | sh
```

如果检测到 `100.64.0.0/10` 网段内的接口地址，设置脚本会假定它是 Tailscale 地址，并显示一个小型终端菜单。推荐选项会让 `codex app-server` 只绑定到该 Tailscale 地址。

如果没有检测到 Tailscale 网段地址，请显式传入地址：

```sh
./setup.sh --listen 100.88.77.66 --host 100.88.77.66
```

即使安装了 `qrencode`，也可以强制输出纯文本 URI：

```sh
./setup.sh --no-qr
```

如果之前已经运行过设置，`setup.sh` 会显示当前配置并在重新配置前询问确认。用于脚本或非交互式 SSH 重新配置时：

```sh
./setup.sh --yes
```

### 向后兼容性

上面的默认命令仍然只安装稳定的 App Server wrapper。它不会安装通知
agent、修改 Codex hooks，也不要求通知后端。包括当前 App Store 版本在内
的旧版 NovaScale，仍可按原方式安装并使用 Codex 主机。

通知功能仍是附加功能。新版 App 的新主机页面可以默认开启通知；传入通知
后端地址和 setup-token 文件时，bootstrap 会自动下载固定版本的已签名
agent。用户关闭通知开关时，App 应省略注册参数并传入
`--no-notifications`。旧版 App 和上面的纯 wrapper 命令不会被静默改变。

通知 agent 的首次注册还需要由 App/后端签发的短时、一次性 setup
token。token 必须放在受保护的 `0600` 临时文件中，并通过
`--notification-setup-token-file` 传入。bootstrap 只把注册任务和 token
暂存到 agent 自己的 `0600` 文件中，不执行网络注册；调用方随后即可删除
原临时文件。agent daemon 会在后台注册、对临时故障自动重试，并在成功或
永久拒绝后删除自己的 token 文件。重新部署已注册或仍在注册的主机会保留
原有身份。

当前调试流程固定使用 agent `0.1.0-dev.4`。bootstrap 会从对应的 GitHub
Release 下载当前平台的归档和 `SHA256SUMS`，校验摘要、归档路径和内嵌版本；
macOS 还会验证 Developer ID 签名、公证票据和 universal 架构。

## 高级子网模式

仅当主机位于 Tailscale 子网路由后方，或位于可信私有网络中时，才使用 `0.0.0.0`：

```sh
./setup.sh --listen 0.0.0.0 --host 192.168.50.20 --port 14500
```

网络监听模式会让任何能访问所配置主机和端口的设备连接到 Codex app-server。虽然仍然需要 capability token，但可达的网络暴露仍会增加风险。

## 手动设置

你可以跳过服务 helper，直接在终端中手动运行 `codex app-server`。这种方式下终端不能关闭。如果希望它在后台或登录时运行，请用你偏好的服务管理方式自行接入。

创建 token：

```sh
mkdir -p "$HOME/.codex"
umask 077
openssl rand -base64 32 > "$HOME/.codex/novascale-app-server-token"
chmod 600 "$HOME/.codex/novascale-app-server-token"
```

在你的 Tailscale IP 上启动 app server：

```sh
codex app-server \
  --listen ws://100.88.77.66:14500 \
  --ws-auth capability-token \
  --ws-token-file "$HOME/.codex/novascale-app-server-token"
```

对于子网路由模式，请使用 `0.0.0.0` 作为监听地址，并在 NovaScale 中创建主机条目时使用可访问的子网 IP 或主机名。

## Helper

设置脚本会创建：

```text
~/.local/bin/novascale-codex
```

命令：

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

统一 helper 中始终会显示 `notification-*` 命令，但只有显式安装通知
agent 后这些命令才可使用。

## 文件

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

## 仓库结构

如果你选择克隆仓库，而不是通过 `curl` 运行设置脚本：

- **`bin/novascale-codex`**：服务 helper 脚本。从本地 clone 运行 `./setup.sh` 时，设置脚本会直接复制该文件到 helper 路径。通过 `curl` 管道安装时，helper 会由嵌入在 `setup.sh` 中的副本生成。
- **`templates/`**：包含 systemd 服务 (`linux-systemd-user.service`) 和 macOS LaunchAgent (`macos-launchagent.plist`) 的静态示例配置。它们仅供手动设置参考，不会被 `setup.sh` 读取或执行；`setup.sh` 会动态生成服务配置。

## 安全

NovaScale Codex 配对信息在你的 Codex 主机上生成。它不会发送到 GalaxNet 或任何网站。NovaScale 会在本地导入该信息，并将 token 存储在 iOS Keychain 中。

## 路线图

基于 APNs 的推送通知正在通过 Codex hooks 和独立的仅出站 companion
agent 开发。该 agent 不是代理，也不会进入权威 App Server/Tailscale
连接路径。默认 wrapper-only 安装将继续兼容旧版 App。
