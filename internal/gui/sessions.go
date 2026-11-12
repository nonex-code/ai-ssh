package gui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/nonex-code/ai-ssh/internal/config"
	"github.com/nonex-code/ai-ssh/internal/proto"
	"github.com/nonex-code/ai-ssh/internal/sshconfig"
)

// favoriteOrder puts the starred connections first, then follows the saved
// bookmark order, then whatever is left in catalogue order.
func favoriteOrder(hosts []proto.HostInfo, order []string) []proto.HostInfo {
	byAlias := map[string]proto.HostInfo{}
	for _, h := range hosts {
		byAlias[h.Alias] = h
	}

	var sorted []proto.HostInfo
	seen := map[string]bool{}
	for _, alias := range order {
		if h, ok := byAlias[alias]; ok && !seen[alias] {
			sorted = append(sorted, h)
			seen[alias] = true
		}
	}
	for _, h := range hosts {
		if !seen[h.Alias] {
			sorted = append(sorted, h)
		}
	}

	out := make([]proto.HostInfo, 0, len(sorted))
	for _, h := range sorted {
		if h.Favorite {
			out = append(out, h)
		}
	}
	for _, h := range sorted {
		if !h.Favorite {
			out = append(out, h)
		}
	}
	return out
}

// bookmarkOrder is the saved bar order.
func (a *App) bookmarkOrder() []string {
	ag := a.agentRef()
	if ag == nil {
		return nil
	}
	return ag.Config().Defaults.Bookmarks
}

func (a *App) quickConnect() {
	raw := strings.TrimSpace(a.quick)
	if raw == "" {
		return
	}
	user, host, port, ok := parseQuick(raw)
	if !ok {
		a.flash = tr("写不成连接：") + raw + tr("，试试 user@host:22")
		a.flashStyle = flashWarning
		a.invalidate()
		return
	}

	draft := editorDraft{
		alias:   host,
		addr:    host + ":" + port,
		user:    user,
		auth:    "key",
		key:     "~/.ssh/id_ed25519",
		hostKey: "known_hosts",
		elev:    "none",
	}
	a.quick = ""
	a.flash = ""
	a.flashStyle = flashInfo
	a.openEditorDraft(draft, "", true)
}

// parseQuick splits user@host[:port], defaulting the port to 22 and the user to
// this machine's login name.
func parseQuick(raw string) (user, host, port string, ok bool) {
	port = "22"
	rest := strings.TrimSpace(raw)
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		user, rest = rest[:i], rest[i+1:]
	}
	if rest == "" {
		return "", "", "", false
	}
	host = rest
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		if n, err := strconv.Atoi(rest[i+1:]); err == nil && n > 0 && n < 65536 {
			host, port = rest[:i], rest[i+1:]
		}
	}
	if host == "" {
		return "", "", "", false
	}
	if user == "" {
		user = osUser()
	}
	return user, host, port, true
}

func osUser() string {
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	if v := os.Getenv("USERNAME"); v != "" {
		return v
	}
	return "root"
}

// importSSHConfig merges ~/.ssh/config into the catalogue. Existing aliases are
// left alone.
func (a *App) importSSHConfig() {
	ag := a.agentRef()
	if ag == nil {
		a.flash = tr("agent 未就绪")
		a.flashStyle = flashWarning
		a.invalidate()
		return
	}

	entries, err := sshconfig.Load(sshconfig.DefaultPath())
	if err != nil {
		a.flash = tr("读取 ~/.ssh/config 失败：") + err.Error()
		a.flashStyle = flashWarning
		a.invalidate()
		return
	}
	if len(entries) == 0 {
		a.flash = tr("~/.ssh/config 里没有可导入的主机")
		a.flashStyle = flashWarning
		a.invalidate()
		return
	}

	a.flash = tr("正在导入…")
	a.flashStyle = flashInfo
	a.invalidate()

	go func() {
		added, skipped := 0, 0
		err := ag.UpdateConfig(func(cfg *config.Config) error {
			added, skipped = mergeSSHConfig(cfg, entries)
			return nil
		})
		a.onMain(func() {
			if err != nil {
				a.flash = tr("导入失败：") + err.Error()
				a.flashStyle = flashWarning
				a.invalidate()
				return
			}
			if added == 0 {
				a.flash = fmt.Sprintf(tr("没有新主机（%d 个已存在）"), skipped)
				a.flashStyle = flashWarning
			} else {
				a.flash = fmt.Sprintf(tr("导入了 %d 个主机，跳过 %d 个已存在的"), added, skipped)
				a.flashStyle = flashSuccess
			}
			a.refreshHosts()
			a.invalidate()
		})
	}()
}

// mergeSSHConfig adds the entries the catalogue does not already have.
func mergeSSHConfig(cfg *config.Config, entries []sshconfig.Entry) (added, skipped int) {
	for _, e := range entries {
		if _, exists := cfg.Hosts[e.Alias]; exists {
			skipped++
			continue
		}
		key := e.IdentityFile
		if key == "" {
			key = "~/.ssh/id_ed25519"
		}
		cfg.Hosts[e.Alias] = &config.Host{
			Addr:    e.Address(),
			User:    e.User,
			Auth:    config.Auth{Type: "key", Key: key},
			HostKey: "known_hosts",
			Group:   "ssh-config",
		}
		added++
	}
	return added, skipped
}

func (a *App) flashLine(c *ui.Context, t *ui.Theme) {
	if a.flash == "" {
		return
	}
	color := t.TextMuted
	switch a.flashStyle {
	case flashWarning:
		color = t.Warning
	case flashSuccess:
		color = t.Success
	}
	ui.Text(c, a.flash).FontSize(fontSmall).TextColor(color)
}
