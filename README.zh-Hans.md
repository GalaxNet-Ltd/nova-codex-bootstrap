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
novascale-codex print-pairing
novascale-codex print-pairing --no-qr
novascale-codex rotate-token
novascale-codex uninstall
```

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

基于 APNs 的推送通知计划在后续版本中加入。我们仍在评估是通过 Codex hooks 集成，还是在 `codex app-server` 前提供一个专用的开源主机代理。
