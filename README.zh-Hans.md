# NovaScale Codex Host

[English](README.md) | [简体中文](README.zh-Hans.md) | [繁體中文](README.zh-Hant.md)

NovaScale Codex Host 是 NovaScale Codex 集成的主机端设置工具。它会配置用户级 `codex app-server` 服务，创建 capability token，并打印可导入 NovaScale iOS 的配对 URI。

App Server wrapper 使用主机上已经安装好的官方 `codex` 命令和用户已有的 Tailscale 网络。启用远程通知时，设置脚本还会从固定版本的已签名 Release 安装 NovaScale 的开源通知 agent。

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

重新部署会保留现有 capability token，因此已经配对的 App 可以继续使用。
只有显式传入 `--rotate-token`，或不存在可用 token 时，设置脚本才会更换
token。

### 向后兼容性

所有用户的主机通知支持都默认开启。新版 App 会提供通知后端地址和短时
setup-token 文件，bootstrap 随后会自动下载并注册固定版本的已签名 agent。
这只会让主机具备通知能力，不会启用付费的远程通知投递。远程推送需要在
Codex 设置中单独启用，并且需要 Pro 订阅。关闭主机通知支持会传入
`--no-notifications`，让主机保持精简的 wrapper-only 安装。

旧版 App 和手动调用不会提供注册凭据。此时设置脚本会提示通知注册不可用，
并继续完成 wrapper-only 安装，而不会失败。也可以显式选择该路径：

```sh
./setup.sh --no-notifications
```

因此旧版 NovaScale 仍可继续使用 Codex 主机。对于在通知版 bootstrap 之前
创建的主机，之后只需重新部署；不需要删除、修复或重新添加主机。

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

统一 helper 中始终会提供 `notification-*` 命令；安装通知 agent 后这些
命令才可使用。

## 文件

App Server wrapper 会创建：

```text
~/.codex/novascale-codex-host.env
~/.codex/novascale-app-server-token
~/Library/LaunchAgents/dev.galaxnet.novascale.codex.plist
~/.config/systemd/user/novascale-codex.service
```

启用通知时还会创建或更新：

```text
~/.codex/hooks.json
~/.local/bin/novascale-agent
~/.config/novascale-agent/config.json
~/.config/novascale-agent/host-key
~/.local/state/novascale-agent/
~/Library/LaunchAgents/dev.galaxnet.novascale.agent.plist
~/.config/systemd/user/novascale-agent.service
```

## 仓库结构

如果你选择克隆仓库，而不是通过 `curl` 运行设置脚本：

- **`bin/novascale-codex`**：服务 helper 脚本。从本地 clone 运行 `./setup.sh` 时，设置脚本会直接复制该文件到 helper 路径。通过 `curl` 管道安装时，helper 会由嵌入在 `setup.sh` 中的副本生成。
- **`templates/`**：包含 systemd 服务 (`linux-systemd-user.service`) 和 macOS LaunchAgent (`macos-launchagent.plist`) 的静态示例配置。它们仅供手动设置参考，不会被 `setup.sh` 读取或执行；`setup.sh` 会动态生成服务配置。

## 安全

NovaScale Codex 配对信息在你的 Codex 主机上生成。它不会发送到 GalaxNet 或任何网站。NovaScale 会在本地导入该信息，并将 token 存储在 iOS Keychain 中。

## 保护隐私的通知标题

主机 agent 和通知后端都不会收到 thread 标题、prompt、assistant 回复、
工具输入、命令、patch、工作目录或 transcript 路径。agent 只发送生命周期
事件类型、时间戳，以及关联主机、thread、turn 和批准请求所需的非内容标识符。
每个事件都会先由主机密钥签名，再上传到通知服务。

APNs payload 只包含通用通知文案，以及不透明的 event、host、thread 和 turn
标识符。NovaScale 会在设备受数据保护的 App Group 容器中保存一个小型、
有时限的 host/thread 标识符到标题的映射。通知服务扩展在收到通知后使用这份
本地映射补充标题；如果设备上没有对应标题，就显示通用通知。因此 thread
标题不会经过通知后端或 APNs。通知后端会加密存储 APNs device token。

## 通知 Agent

仓库包含 NovaScale 独立通知旁路的开源主机 agent。`novascale-agent` 只观察
`PermissionRequest` 和 `Stop`，丢弃未列入白名单的内容，在本地排队最小化
事件，并上传签名事件。hook 始终返回 `{}`，不会替 Codex 作出任何决定。

启用通知的 bootstrap 会下载适合主机平台的固定 agent Release。已经注册的
agent 会保留现有身份。首次注册使用由 App 获取的短时、一次性 setup token；
App 将 token 写入受保护的 `0600` 临时文件，bootstrap 只在本地暂存注册，
agent daemon 随后在后台注册并自动重试。注册完成或被永久拒绝后，agent 会
删除自己的 token 副本。

当前调试流程固定使用 agent `0.1.0-dev.5`。bootstrap 会校验平台归档、
`SHA256SUMS`、归档路径和内嵌版本，且不会回退到未签名构建。macOS 还会验证
Developer ID 签名、公证票据和预期的 universal 架构。

通知端点必须使用 HTTPS。只有通过 `localhost`、`127.0.0.0/8` 或 `::1`
访问的 loopback 开发服务可以使用 HTTP。

设置脚本只向现有 `~/.codex/hooks.json` 添加精确的 `novascale-agent hook`
处理器。请在 Codex CLI 中使用 `/hooks` 检查并信任生成的 hook 定义。

详见 [`docs/release-preparation.md`](docs/release-preparation.md) 和
[`notifications/README.md`](notifications/README.md)。

## 架构

基于 APNs 的推送通知使用 Codex 生命周期 hooks 和独立、仅出站的 companion
agent。该 agent 不是代理，也不会进入权威 App Server/Tailscale 连接路径。
