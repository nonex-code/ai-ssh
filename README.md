# ai-ssh

[中文](README.md) | [English](README.en.md)

<img src="internal/gui/assets/logo.svg" width="112" height="112" alt="ai-ssh：盾牌里的 AI 星芒与终端提示符">

**给 AI 用的 SSH，凭证不出本机。**

ai-ssh 是一个单二进制的 SSH 客户端：桌面窗口、给 AI 用的 CLI、MCP 服务器和持有凭据的
agent 都在同一个程序里。AI 只能通过受策略约束的 `exec` / 上传 / 下载访问服务器；主密码、
私钥、登录口令和提权口令只存在于本机 agent 内存和 Argon2id + AES-256-GCM 加密的 vault 里。

> 项目还在 1.0 之前；CLI 的 JSON 结构和退出码已经当作稳定契约维护。

## 特性

- **一个二进制**：双击打开窗口，带参数就是 CLI / MCP / agent。
- **凭据不出本机**：AI 进程不读主密码、不读私钥；所有敏感操作由 agent 完成。
- **真终端**：GUI 内嵌 Ghostty 的 libghostty-vt，支持备用屏、鼠标上报、括号粘贴、超链接、
  IME、缩放、滚动缓冲；Linux / macOS / Windows 的发布包都内嵌对应平台的终端库。
- **桌面体验**：标签拖动排序、会话分组、本机 shell（PowerShell / cmd / WSL / Git Bash /
  `$SHELL`）、SFTP 双栏文件面板、端口转发、主机概览。
- **可审计**：动作、命令、结果写入审计日志；AI 动作可按策略弹窗审批。
- **自动/手动更新**：从 GitHub Releases 检查更新、校验 SHA256 并替换可执行文件。

## 快速开始

### 使用发布包

从 [GitHub Releases](https://github.com/nonex-code/ai-ssh/releases) 下载对应平台的压缩包：

| 平台 | 文件 | 解压后 |
|---|---|---|
| Windows | `ai-ssh-<version>-windows-amd64.zip` | `ai-ssh.exe` + `logo.ico` |
| Linux | `ai-ssh-<version>-linux-{amd64,arm64}.tar.gz` | `ai-ssh` + 桌面图标/入口 |
| macOS | `ai-ssh-<version>-darwin-arm64.tar.gz` | `ai-ssh.app` + 裸 `ai-ssh` |

Windows 解压后双击 `ai-ssh.exe` 打开窗口；Linux 解压后运行 `./ai-ssh`，需要应用菜单图标时再运行
`install-icons.sh`；macOS 解压出 `ai-ssh.app`，首次打开可能需要在 Finder 里右键 → 打开
（包未签名/公证）。Windows 的二进制是 `windowsgui` 子系统，双击不会弹控制台黑窗。

### 从源码构建

需要 Go 1.27+（版本以 `go.mod` 为准）和 Git。Linux 构建不需要 cgo。

```bash
make build          # → build/ai-ssh（Windows：build/ai-ssh.exe），已内嵌终端库
make run            # 构建并运行
make test           # 单元 + 进程内 SSH/SFTP 端到端 + 无窗口 UI 测试
make vet
```

`make build` 会为目标平台下载并内嵌 libghostty-vt，首次构建需要网络（可用
`MYGO_MIRROR=https://ghproxy.net`）；开发时想跳过下载用 `make build-dev`。
发布包用 `make package`。

## 命令行

```bash
ai-ssh                      # 打开桌面窗口（同 ai-ssh gui）
ai-ssh gui [--vault PATH] [--endpoint ADDR]
ai-ssh mcp                  # 在 stdin/stdout 上说 MCP，无凭据
ai-ssh daemon               # 前台常驻 agent
ai-ssh agent --auto         # 内部：CLI/MCP 按需拉起
ai-ssh init [--vault PATH] [--force]
ai-ssh passwd [--vault PATH] [--endpoint ADDR]
ai-ssh config show|edit|import
ai-ssh hosts
ai-ssh ping <host>
ai-ssh exec <host> [--sudo] [--pty] [--tail] [--workdir DIR] [--timeout S] -- <cmd...>
ai-ssh upload <host> <local> <remote> [--mkdir] [--verify]
ai-ssh download <host> <remote> <local> [--verify]
ai-ssh version
```

除 `version`/`help` 外，CLI 每次输出一个 JSON 对象，诊断信息走 stderr。退出码：

| code | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 用法 / 配置错误 |
| 2 | 未解锁、连接、认证、host key 失败 |
| 3 | 远端命令返回非 0 |
| 4 | 传输失败（含校验失败） |
| 5 | 被策略阻止 |
| 6 | agent 不可用 |
| 124 | 超时 |

CLI/MCP 发现端点不存在时会自动拉起 `ai-ssh agent --auto`，由它询问主密码。

## 桌面窗口

```
┌───────────────────────────────────────────────────────────────┐
│ [⌂] ●prod × ▣+                                  ⌗  ⧉ ☾ ⚙ 🔒  │
├───────────────────────────────────────────────────────────────┤
│ prod  ✓connected                    _  📁  ⇄  ◔  ⧉  ✎        │
│ prod $ ls                                                     │
└───────────────────────────────────────────────────────────────┘
```

- **标签**：同一主机可以开多个会话；标签可拖动排序，拖到另一个标签左/右半边决定插入位置，
  拖到 `+` 移到末尾；右键可复制/断开。主页标签固定。
- **本地终端**：首页和 `+` 菜单可以直接开本机 shell。Windows 探测 PowerShell 7、
  Windows PowerShell、cmd、Git Bash/MSYS 和已安装的 WSL 发行版；macOS/Linux 探测 `$SHELL`、
  zsh、bash、fish、nushell、elvish、xonsh 等。可用 `AISSH_LOCAL_SHELLS` 增加自定义项。
  本地会话不经过 agent，也不会暴露给 AI。
- **服务器管理**：分组显示、搜索、双击/按钮连接、编辑、删除；每行可移动到分组或新建分组；
  分组可以重命名、解散，空分组会保留。分组顺序存在 `defaults.groups`。
- **文件**：SFTP 双栏，上传下载、目录创建、重命名、删除、权限/大小显示；传输有队列。
- **转发**：本地/远程/动态隧道，连接时自动建立，面板里显示状态和错误。
- **详情**：采样负载、内存、磁盘、进程、登录用户，80%/90% 变色告警。
- **主机密钥**：首次连接显示 SHA256 指纹，确认后钉进该连接的 `host_key`；密钥变化时弹窗
  并排显示新旧指纹，必须勾选“已通过可信渠道核对”才能更新并重连。

### 设置

标签条右侧的 `⚙` 打开设置窗口，左侧导航、右侧内容：

| 页面 | 内容 |
|---|---|
| 语言 | 中文 / English / 跟随系统；立即应用到窗口、菜单和对话框 |
| 外观 | 跟随系统 / 白 / 黑；太阳/月亮按钮可快速切换 |
| 终端 | 终端配色、16 色预览、字体说明 |
| 安全 | 验证当前密码并重新加密 vault（不需要锁定或重启） |
| 关于 | 版本、检查更新、自动更新开关、开源项目列表 |

`关于` 页从 GitHub Releases 检查更新（默认启动时每天一次，可关闭），也可以手动检查。
发现新版本后可以下载并安装；更新包会校验发布页的 `SHA256SUMS.txt` 和包内校验和，
替换当前可执行文件，重启后生效。自动下载安装默认关闭。

## MCP

把这段配置给支持 MCP 的 AI host：

```json
{
  "mcpServers": {
    "ssh": {
      "command": "C:\\path\\to\\ai-ssh.exe",
      "args": ["mcp"]
    }
  }
}
```

窗口右上角的 MCP 图标会复制本机实际路径的配置。

主要工具：`ssh_list_hosts`、`ssh_ping`、`ssh_exec`、`ssh_upload`、`ssh_download`。
`ssh_exec` 支持 `sudo`、PTY、超时和输出截断；所有动作受主机 `deny`/`allow`、
`approval` 和路径根限制。

## 配置

唯一的配置文件是加密的 **vault**（默认 `~/.ai-ssh/vault`，Windows 为
`C:\Users\<你>\.ai-ssh\vault`），从创建第一刻起就是密文。没有明文配置阶段。

GUI 的连接表单、`ai-ssh config edit`、`ai-ssh config import` 都可以改配置。
vault 内的 JSON 结构：

```json
{
  "defaults": {
    "groups": ["生产", "测试"],
    "timeout": "60s",
    "connect_timeout": "10s",
    "max_output": 262144,
    "idle_lock": "30m",
    "known_hosts": "~/.ssh/known_hosts",
    "approval": "auto",
    "approval_timeout": "120s",
    "upload_roots": [],
    "download_roots": [],
    "audit_log": ""
  },
  "hosts": {
    "prod": {
      "addr": "10.0.0.5:22",
      "user": "deploy",
      "group": "生产",
      "auth": {
        "type": "key",
        "key": "~/.ssh/id_ed25519",
        "passphrase": "env:AISSH_PROD_PASSPHRASE"
      },
      "host_key": "known_hosts",
      "workdir": "/srv/app",
      "deny": ["^reboot", "rm[[:space:]]+-rf[[:space:]]+/"],
      "approval": "always",
      "elevate": {
        "mode": "sudo",
        "password": "env:AISSH_PROD_SUDO"
      }
    }
  }
}
```

- `auth.type`：`key` 或 `password`。
- 口令、私钥口令、sudo 密码可以写 `env:NAME` / `file:PATH`，保存时保留引用，不会烘焙明文。
- `host_key`：`known_hosts`、`insecure`（危险，仅测试）或 `SHA256:...` 固定指纹。
- `approval`：`auto` / `elevate` / `always`。
- `defaults.groups` 决定分组顺序；主机上的 `group` 会自动补进去。

配置命令：

```bash
ai-ssh config show      # 打印（字面量密钥显示为 ***）
ai-ssh config edit      # $EDITOR 编辑后重新加密
ai-ssh config import <file|-> [--force]
```

修改主密码：

```bash
ai-ssh passwd           # 先锁定窗口或停掉 daemon
```

窗口里的「设置 → 安全」由进程内 agent 直接重加密，不需要锁定。
## 安全模型

**已经做到的：**

- vault 用 Argon2id（3 次、64 MiB、4 并行）派生密钥，AES-256-GCM 加密；
- 主密码只进入本机 agent 进程，CLI / MCP / AI 进程不持有主密码、私钥或登录口令；
- 交互式 shell 只在 GUI 进程内通过 `agent.OpenSession` 打开，不在 AI 的协议里；
- AI 只能发 `exec` / `upload` / `download` 等受限操作，受 `deny` / `allow`、提权策略、
  路径根和审批控制；
- 审批弹窗、审计日志、host key 校验都不经过 AI；
- 本地 shell 不需要 vault，也不经过 agent。

**没有做到的（诚实说明）：**

- 没有硬件根签名 / TPM / Secure Enclave 支持；
- 发布包尚未做代码签名和 macOS notarization，系统可能提示未知开发者；
- 运行中的进程和已解锁的 agent 内存仍可能被同用户权限的恶意程序读取；
- 自动更新依赖 GitHub Releases；仓库还没发布版本时只会在设置页显示提示。

## 环境变量

| 变量 | 用途 |
|---|---|
| `AISSH_VAULT` | 覆盖 vault 路径 |
| `AISSH_ENDPOINT` | 覆盖 IPC 端点（named pipe / unix socket） |
| `AISSH_MASTER_PASSWORD` | 无人值守时提供主密码（显式降级） |
| `AISSH_ASKPASS` / `SSH_ASKPASS` | 无 TTY 时的外部取密码程序 |
| `AISSH_NO_AUTOSTART` | 禁止 CLI/MCP 自动拉起 agent |
| `AISSH_LOCAL_SHELLS` | 本地 shell 自定义项：`标签=命令 参数…`，多项用系统路径分隔符 |
| `MYGO_GHOSTTY_VT` | 指定 libghostty-vt 路径（覆盖内嵌库） |
| `MYGO_MIRROR` | `make native` 的下载镜像前缀 |

## 构建与发布

Makefile 只有一个输出目录：

- **`build/` 是唯一输出目录**：开发二进制、各平台 staging、临时下载、发布压缩包、
  校验和都在这里。
- `build/` 被 `.gitignore` 忽略，不会提交。

```bash
make build                      # 自包含的 build/ai-ssh(.exe)
make build-dev                  # 快速开发构建，终端库运行时下载
make run                        # 构建并运行（ARGS=...）
make package VERSION=0.1.0         # 当前平台一个发布包
make package VERSION=0.1.0 GOOS=linux GOARCH=arm64
make checksums                  # build/SHA256SUMS.txt
make icon                       # logo.svg → ico/icns/png（+ Windows syso）
make native / make embed        # 只取/嵌入终端库
make test / vet / fmt
make clean
```

`build/` 里可能长这样：

```text
build/
├── ai-ssh(.exe)                                  # make build
├── ai-ssh-0.1.0-windows-amd64.zip                # make package
├── ai-ssh-0.1.0-linux-amd64.tar.gz
├── ai-ssh-0.1.0-linux-arm64.tar.gz
├── ai-ssh-0.1.0-darwin-arm64.tar.gz
├── SHA256SUMS.txt                                # make checksums
├── <goos>-<goarch>/ai-ssh(.exe)                  # 打包用中间二进制
├── pkg/<goos>-<goarch>/                          # staging
└── native/<goos>-<goarch>/                       # 下载的终端库
```

压缩包内可执行文件不带平台后缀，平台/版本只在外层包名：

```text
# Windows zip
ai-ssh.exe  logo.ico  README.md  README.en.md  OFL-*.txt  SHA256SUMS.txt

# Linux tar.gz
ai-ssh  ai-ssh.desktop  ai-ssh.png  install-icons.sh
README.md  README.en.md  OFL-Inter.txt  OFL-JetBrainsMono.txt  SHA256SUMS.txt

# macOS tar.gz
ai-ssh  ai-ssh.app/  README.md  README.en.md  OFL-*.txt  SHA256SUMS.txt
ai-ssh.app/Contents/{Info.plist,MacOS/ai-ssh,Resources/logo.icns}
```

包内 `SHA256SUMS.txt` 覆盖包内可执行文件；发布页的 `SHA256SUMS.txt`（本地在 `build/SHA256SUMS.txt`）覆盖全部压缩包。

### 终端库（libghostty-vt）

自包含构建（`make build`、`make package`）会为目标平台下载并嵌入对应的库：

| 平台 | 嵌入文件名 |
|---|---|
| Windows amd64 / arm64 | `ghostty-vt-<goos>-<goarch>.dll` |
| Linux amd64 / arm64 | `libghostty-vt-linux-<arch>.so` |
| macOS amd64 / arm64 | `libghostty-vt-darwin-<arch>.dylib` |

`make native` 用 `scripts/fetchnative` 从 mygo 的 manifest 下载并校验 sha256，
`make embed` 把它放到 `internal/vtlib/`，构建标签 `ai_ssh_embedvt` 将它编进二进制。
首次打开终端时会释放到 `~/.ai-ssh/lib/vt/<sha256>/`，之后复用；`MYGO_GHOSTTY_VT`
可以覆盖。`make build` 会先为目标平台下载并嵌入库；`make build-dev` 和 `go test`
不要求本机有库（运行终端时由 mygo 缓存/下载）。

### GitHub Actions

`.github/workflows/release.yml`：

- 推送 `v*` tag 触发；
- 先跑 `make vet test`；
- 矩阵构建 `linux/amd64`、`linux/arm64`、`darwin/arm64`、`windows/amd64`；
- 每个 job 上传自己的压缩包 artifact；
- release job 下载全部 artifact，运行 `make checksums`，自动创建 GitHub Release 并上传
  压缩包和 `SHA256SUMS.txt`。
- tag 的 `v` 前缀会在版本里去掉：`v0.1.0` → `ai-ssh-0.1.0-*`。

## 使用的开源项目

| 项目 | 许可证 | 用途 |
|---|---|---|
| [Go](https://go.dev) | BSD-3-Clause | 语言与运行时 |
| [mygo](https://github.com/egoist/mygo) | MIT | 窗口、UI 组件、终端控件 |
| [Ghostty / libghostty-vt](https://github.com/ghostty-org/ghostty) | MIT | 终端解析与渲染 |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | BSD-3-Clause | SSH 客户端、host key |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | BSD-3-Clause | 无回显密码输入 |
| [golang.org/x/sys, net, text, image](https://pkg.go.dev/golang.org/x) | BSD-3-Clause | 平台、网络、文本、图像支持 |
| [github.com/pkg/sftp](https://github.com/pkg/sftp) | BSD-2-Clause | SFTP 传输 |
| [github.com/Microsoft/go-winio](https://github.com/microsoft/go-winio) | MIT | Windows named pipe IPC |
| [github.com/ebitengine/purego](https://github.com/ebitengine/purego) | Apache-2.0 | 无 cgo 加载 libghostty-vt |
| [github.com/go-text/typesetting](https://github.com/go-text/typesetting) | BSD-3-Clause / Unlicense | 终端字体排版 |
| [github.com/kr/fs](https://github.com/kr/fs) | BSD-3-Clause | 本地文件遍历 |
| [Inter](https://rsms.me/inter/) | SIL OFL 1.1 | 界面字体 |
| [JetBrains Mono](https://www.jetbrains.com/lp/mono/) | SIL OFL 1.1 | 终端字体 |

完整的版本和传递依赖见 `go.mod` / `go.sum`；字体许可证以
`OFL-Inter.txt`、`OFL-JetBrainsMono.txt` 随发布包分发。

## 项目结构

```text
main.go                 入口：窗口、CLI、MCP、agent 按参数分发
internal/agent          解锁 vault、持有凭据、组装 daemon
internal/config         主机目录、策略、分组、secret 引用
internal/vault          Argon2id + AES-256-GCM
internal/daemon         SSH、执行、传输、转发、审批、审计
internal/gui            桌面窗口、设置、更新、本地 shell、文件面板
internal/localshell     本机 shell 探测
internal/update         GitHub Releases 检查、下载校验、替换可执行文件
internal/vtlib          嵌入 libghostty-vt 并释放
internal/mcp            MCP stdio 服务器（薄代理，无凭据）
internal/client         AI 侧唯一客户端
internal/ipc            named pipe / unix socket
internal/proto          请求、响应、错误码
scripts/buildtool       跨平台 staging、zip/tar.gz、SHA256SUMS
scripts/fetchnative     按目标平台下载终端库
scripts/genicon         logo.svg → ico / icns / png
packaging/              Linux desktop/install、macOS Info.plist
.github/workflows/      tag 自动测试、矩阵构建、发布
```

## 已知限制

- 预编译包还没有代码签名 / notarization；
- 自动更新的“安装”在下次启动生效，运行中的进程不会热替换；
- 更新需要当前用户对可执行文件所在目录有写权限；
- 从 GitHub API 检查更新受匿名速率限制（默认每天一次足够）；
- Windows 的 `windowsgui` 二进制在 PowerShell 中不会阻塞，脚本请用 `cmd /c` 或
  `Start-Process -Wait`。
