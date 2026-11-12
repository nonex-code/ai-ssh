# ai-ssh

[English](README.en.md) | [中文](README.md)

<img src="internal/gui/assets/logo.svg" width="112" height="112" alt="ai-ssh: an AI spark and a terminal prompt inside a shield">

**SSH for AI agents. Credentials never leave this machine.**

ai-ssh is a single-binary SSH client: a desktop window, an AI-facing CLI, an
MCP server and the credential-holding agent are one program. The AI can only
reach servers through policy-checked `exec` / upload / download operations;
the master password, private keys, login passwords and sudo passwords exist
only in the local agent and in an Argon2id + AES-256-GCM encrypted vault.

> Pre-1.0. The CLI's JSON shapes and exit codes are already treated as a
> stable contract.

## Features

- **One binary**: no arguments opens the window; arguments run the CLI, MCP or
  agent.
- **Credentials stay local**: AI processes never read the master password or a
  private key.
- **Real terminal**: the GUI embeds Ghostty's libghostty-vt with alternate
  screen, mouse reporting, bracketed paste, hyperlinks, IME, zoom and
  scrollback. Every release embeds the native library for its platform.
- **Desktop workflow**: draggable tabs, session groups, local shells
  (PowerShell / cmd / WSL / Git Bash / `$SHELL`), dual-pane SFTP, port
  forwarding and a host monitor.
- **Auditable**: actions and results go to an audit log; AI actions can require
  an approval window.
- **Updates**: manual and automatic checks against GitHub Releases, with
  SHA256 verification before replacing the executable.
- **Bilingual UI**: Chinese, English, or follow the system.

## Install

Download the archive for your platform from
[GitHub Releases](https://github.com/nonex-code/ai-ssh/releases):

| Platform | File | After extraction |
|---|---|---|
| Windows | `ai-ssh-<version>-windows-amd64.zip` | `ai-ssh.exe` + `logo.ico` |
| Linux | `ai-ssh-<version>-linux-{amd64,arm64}.tar.gz` | `ai-ssh` + desktop files |
| macOS | `ai-ssh-<version>-darwin-arm64.tar.gz` | `ai-ssh.app` + a raw `ai-ssh` |

Windows: unzip and double-click `ai-ssh.exe`. Linux: extract and run
`./ai-ssh`; run `install-icons.sh` for an application-menu icon. macOS: extract
`ai-ssh.app`; the first open may need Finder → right-click → Open because the
bundle is not signed or notarized. The Windows binary uses the `windowsgui`
subsystem, so double-clicking does not open a console.

## Build

Go 1.27+ (see `go.mod`) and Git. Linux builds need no cgo.

```bash
make build          # build/ai-ssh(.exe), self-contained (embedded terminal library)
make build-dev      # fast build; terminal library is fetched at runtime
make run            # build and run
make test           # unit + in-process SSH/SFTP e2e + headless UI tests
make vet
```

`make build` downloads and embeds libghostty-vt for the target platform on the
first run (`MYGO_MIRROR=https://ghproxy.net` if GitHub is blocked).

## CLI

```bash
ai-ssh                      # open the desktop window (same as ai-ssh gui)
ai-ssh gui [--vault PATH] [--endpoint ADDR]
ai-ssh mcp                  # MCP on stdin/stdout, no credentials
ai-ssh daemon               # foreground agent
ai-ssh agent --auto         # internal: on-demand agent
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

Except `version` and `help`, every reply is one JSON object on stdout;
diagnostics go to stderr.

| exit | meaning |
|---|---|
| 0 | success |
| 1 | usage / config error |
| 2 | unlock, connect, auth or host key failure |
| 3 | remote command returned non-zero |
| 4 | transfer or checksum failure |
| 5 | blocked by policy |
| 6 | daemon unavailable |
| 124 | timeout |

## Desktop app

```
┌───────────────────────────────────────────────────────────────┐
│ [⌂] ●prod × ▣+                                  ⌗  ⧉ ☾ ⚙ 🔒  │
├───────────────────────────────────────────────────────────────┤
│ prod  ✓connected                    _  📁  ⇄  ◔  ⧉  ✎        │
│ prod $ ls                                                     │
└───────────────────────────────────────────────────────────────┘
```

- **Tabs**: several sessions per host; drag to reorder, drop on the left/right
  half of another tab to insert before/after, drop on `+` to move to the end.
- **Local shells**: the home page and `+` menu open a local shell. Windows
  detects PowerShell 7, Windows PowerShell, cmd, Git Bash/MSYS and installed
  WSL distributions; macOS/Linux detect `$SHELL`, zsh, bash, fish, nushell,
  elvish and xonsh. `AISSH_LOCAL_SHELLS` adds custom entries. Local sessions
  never touch the agent or the vault.
- **Servers**: grouped list with search, connect/edit/delete, create/rename/
  ungroup groups, and move a host between groups from its row.
- **Files**: dual-pane SFTP with upload/download, mkdir, rename, delete,
  permissions and a transfer queue.
- **Forwarding**: local/remote/dynamic tunnels, started with a session and
  shown with their status and errors.
- **Monitor**: load, memory, disk, processes and logged-in users with
  80%/90% warning colours.
- **Host keys**: the first connection shows a SHA256 fingerprint to pin; a
  changed key shows both old and new and requires a confirmation checkbox
  after checking it out of band.

### Settings

The gear button opens a two-pane settings window:

| Page | Contents |
|---|---|
| Language | 中文 / English / Follow system; applies immediately |
| Appearance | Follow system / Light / Dark; the sun/moon button is the quick switch |
| Terminal | Palette, 16-colour preview, font notes |
| Security | Change the vault master password in place |
| About | Version, update check, auto-update options and the open-source list |

Updates are checked against GitHub Releases (once a day by default; dev builds
skip automatic checks). Manual checks are in About. If an update is available,
the app downloads the matching archive, verifies the release `SHA256SUMS.txt`
and the archive's inner checksum, installs the new executable and asks for a
restart. Automatic download/install is opt-in.

## MCP

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

Tools: `ssh_list_hosts`, `ssh_ping`, `ssh_exec`, `ssh_upload`,
`ssh_download`. `ssh_exec` supports `sudo`, PTY, timeouts and output limits;
every action is checked against the host's `deny`/`allow`, `approval` and path
roots.

## Configuration

The only configuration file is the encrypted **vault** (default
`~/.ai-ssh/vault`; on Windows `C:\Users\<you>\.ai-ssh\vault`). There is no
plaintext configuration stage.

Change it from the GUI form, `ai-ssh config edit` or `ai-ssh config import`.
The JSON inside the vault looks like this:

```json
{
  "defaults": {
    "groups": ["Production", "Test"],
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
      "group": "Production",
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

- `auth.type`: `key` or `password`.
- Passwords and passphrases may use `env:NAME` or `file:PATH`; the reference is
  kept when saving.
- `host_key`: `known_hosts`, `insecure` (dangerous, tests only) or a
  `SHA256:...` pin.
- `approval`: `auto`, `elevate` or `always`.
- `defaults.groups` orders the groups; host `group` values are added to it.

Commands:

```bash
ai-ssh config show      # print, literal secrets masked as ***
ai-ssh config edit      # $EDITOR, then re-encrypt
ai-ssh config import <file|-> [--force]
```

Changing the master password:

```bash
ai-ssh passwd           # lock the window or stop the daemon first
```

In the window, Settings → Security re-encrypts through the in-process agent
without locking.

## Security model

Implemented:

- Argon2id (3 passes, 64 MiB, 4 lanes) + AES-256-GCM vault;
- the master password only enters the local agent; the CLI, MCP and AI
  processes hold no credentials;
- interactive shells exist only inside the GUI process, never on the AI
  socket;
- AI operations are checked by `deny`/`allow`, elevation policy, path roots and
  approvals;
- host key checks and approvals never pass through the AI.

Not implemented:

- TPM / Secure Enclave / hardware signing;
- code signing or macOS notarization (the OS may warn on first open);
- protection from a same-user process reading the agent's memory;
- update checks require GitHub; before the first release the settings page
  says there is no release yet.

## Environment

| Variable | Purpose |
|---|---|
| `AISSH_VAULT` | override the vault path |
| `AISSH_ENDPOINT` | override the IPC endpoint |
| `AISSH_MASTER_PASSWORD` | unattended master password (explicit downgrade) |
| `AISSH_ASKPASS` / `SSH_ASKPASS` | external password helper |
| `AISSH_NO_AUTOSTART` | do not auto-start the agent from CLI/MCP |
| `AISSH_LOCAL_SHELLS` | add local shells: `label=command args…`, OS path-list separator |
| `MYGO_GHOSTTY_VT` | override the libghostty-vt path |
| `MYGO_MIRROR` | mirror prefix for `make native` |

## Build and release

`build/` is the only output directory: development binary, per-platform
staging, fetched terminal libraries, release archives and checksums all live
there.

```bash
make build                      # self-contained build/ai-ssh(.exe)
make build-dev                  # fast build, terminal library at runtime
make package VERSION=0.1.0      # one platform's archive in build/
make checksums                  # build/SHA256SUMS.txt
make icon                       # logo.svg → ico/icns/png (+ Windows syso)
make native / make embed        # only fetch/embed the terminal library
make test / vet / fmt
make clean
```

A tag like `v0.1.0` is stripped to `0.1.0`; the executable inside every archive
is always `ai-ssh` (`ai-ssh.exe` on Windows), and only the archive name carries
the platform and version:

```text
build/
├── ai-ssh(.exe)                                  # make build
├── ai-ssh-0.1.0-windows-amd64.zip
├── ai-ssh-0.1.0-linux-amd64.tar.gz
├── ai-ssh-0.1.0-linux-arm64.tar.gz
├── ai-ssh-0.1.0-darwin-arm64.tar.gz
└── SHA256SUMS.txt
```

Each package contains its own `SHA256SUMS.txt` for the executable(s); the
release-level `SHA256SUMS.txt` covers the archives.

### Terminal library

`make build` and `make package` fetch the target platform's libghostty-vt and
embed it in the executable. On first use it is extracted to
`~/.ai-ssh/lib/vt/<sha256>/` and `MYGO_GHOSTTY_VT` points at it. The same
library is reused on later starts; a different version gets a different hash
directory. `MYGO_GHOSTTY_VT` overrides this. Embedded platforms: Windows
amd64/arm64, Linux amd64/arm64, macOS amd64/arm64.

### GitHub Actions

`.github/workflows/release.yml` triggers on `v*` tags:

1. test job: `make vet` + `make test` on Ubuntu;
2. matrix build: linux/amd64, linux/arm64, darwin/arm64, windows/amd64;
3. each job uploads its archive;
4. release job downloads all artifacts, runs `make checksums`, creates a
   GitHub Release with the archives and `SHA256SUMS.txt`.

## Open-source projects

| Project | License | Used for |
|---|---|---|
| [Go](https://go.dev) | BSD-3-Clause | language and runtime |
| [mygo](https://github.com/egoist/mygo) | MIT | window, UI toolkit, terminal widget |
| [Ghostty / libghostty-vt](https://github.com/ghostty-org/ghostty) | MIT | terminal parser and renderer |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | BSD-3-Clause | SSH client and host keys |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | BSD-3-Clause | password prompts |
| [golang.org/x/sys, net, text, image](https://pkg.go.dev/golang.org/x) | BSD-3-Clause | platform, network, text and image support |
| [github.com/pkg/sftp](https://github.com/pkg/sftp) | BSD-2-Clause | SFTP transfers |
| [github.com/Microsoft/go-winio](https://github.com/microsoft/go-winio) | MIT | Windows named-pipe IPC |
| [github.com/ebitengine/purego](https://github.com/ebitengine/purego) | Apache-2.0 | loading libghostty-vt without cgo |
| [github.com/go-text/typesetting](https://github.com/go-text/typesetting) | BSD-3-Clause / Unlicense | terminal font shaping |
| [github.com/kr/fs](https://github.com/kr/fs) | BSD-3-Clause | walking local files |
| [Inter](https://rsms.me/inter/) | SIL OFL 1.1 | interface font |
| [JetBrains Mono](https://www.jetbrains.com/lp/mono/) | SIL OFL 1.1 | terminal font |

Exact versions and transitive dependencies are in `go.mod` / `go.sum`; the
font licences ship as `OFL-Inter.txt` and `OFL-JetBrainsMono.txt`.

## Layout

```text
main.go                 entry: window, CLI, MCP, agent
internal/agent          vault unlock, credentials, daemon
internal/config         hosts, policy, groups, secret references
internal/vault          Argon2id + AES-256-GCM
internal/daemon         SSH, exec, transfer, forward, approval, audit
internal/gui            desktop window, settings, updates, local shells
internal/localshell     local shell detection
internal/update         GitHub Releases, checksums, executable replacement
internal/vtlib          embed libghostty-vt and extract it
internal/mcp            MCP stdio server (thin, no credentials)
internal/client         the AI-side client
internal/ipc            named pipe / unix socket
internal/proto          requests, responses, error codes
scripts/buildtool       cross-platform staging, zip/tar.gz, checksums
scripts/fetchnative     fetch the terminal library for a target
scripts/genicon         logo.svg → ico / icns / png
packaging/              Linux desktop files, macOS Info.plist
.github/workflows/      tag test, matrix build, release
```

## Known limitations

- Release binaries are not signed or notarized;
- updates take effect on the next start, not in a running process;
- updating needs write permission to the executable's directory;
- GitHub's anonymous API rate limit applies (daily checks are well within it);
- the Windows `windowsgui` binary does not block PowerShell; use `cmd /c` or
  `Start-Process -Wait` in scripts.