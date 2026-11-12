// Package config loads the host catalogue that lives inside the vault. Nothing
// in here is ever serialised back to the client: the client sees aliases only.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Approval modes.
const (
	ApprovalAuto    = "auto"    // never ask
	ApprovalElevate = "elevate" // ask only for privileged commands
	ApprovalAlways  = "always"  // ask for every action
)

// Defaults applied when the config does not say otherwise.
const (
	DefaultTimeout         = 60 * time.Second
	DefaultConnectTimeout  = 10 * time.Second
	DefaultMaxOutput       = 256 * 1024
	DefaultIdleLock        = 30 * time.Minute
	DefaultApprovalTimeout = 120 * time.Second
)

func validateApproval(field, mode string) error {
	switch mode {
	case ApprovalAuto, ApprovalElevate, ApprovalAlways:
		return nil
	}
	return fmt.Errorf("%s must be auto, elevate or always (got %q)", field, mode)
}

// Duration is a time.Duration that unmarshals from "60s" or from plain seconds.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] != '"' {
		var secs int64
		if err := json.Unmarshal(b, &secs); err != nil {
			return fmt.Errorf("invalid duration %s: %w", b, err)
		}
		*d = Duration(time.Duration(secs) * time.Second)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON always writes time.Duration's string form ("1m30s"), so saving the
// config back to the vault does not turn it into 90000000000.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d Duration) Or(def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return time.Duration(d)
}

// Auth describes how the daemon authenticates to the remote host.
type Auth struct {
	Type       string `json:"type"` // key | password
	Key        string `json:"key"`
	Cert       string `json:"cert"`
	Passphrase string `json:"passphrase"`
	Password   string `json:"password"`
	Username   string `json:"username"`

	// rawPassphrase and rawPassword keep "env:NAME" / "file:PATH" so saving the
	// catalogue back to the vault does not bake the resolved secret into it.
	rawPassphrase string
	rawPassword   string
}

// PasswordRef is the form the user wrote: "env:NAME" instead of the secret.
func (a Auth) PasswordRef() string {
	if a.rawPassword != "" {
		return a.rawPassword
	}
	return a.Password
}

// PassphraseRef is the form the user wrote for the key passphrase.
func (a Auth) PassphraseRef() string {
	if a.rawPassphrase != "" {
		return a.rawPassphrase
	}
	return a.Passphrase
}

// PasswordRef is the form the user wrote for the sudo password.
func (e Elevate) PasswordRef() string {
	if e.rawPassword != "" {
		return e.rawPassword
	}
	return e.Password
}

// MarshalJSON restores the indirection when the secret was written that way.
func (a Auth) MarshalJSON() ([]byte, error) {
	type plain Auth
	out := plain(a)
	if a.rawPassphrase != "" {
		out.Passphrase = a.rawPassphrase
	}
	if a.rawPassword != "" {
		out.Password = a.rawPassword
	}
	return json.Marshal(out)
}

// Forward is a tunnel started with a session (listen and target are
// host:port, as in ssh -L).
type Forward struct {
	Kind   string `json:"kind"` // local | remote | dynamic
	Listen string `json:"listen"`
	Target string `json:"target,omitempty"`
}

// Elevate describes how the daemon may run a command as another user (usually
// root). The credential lives in the vault and is handed to sudo on stdin, so
// it never appears in the command line, the output or the audit log.
type Elevate struct {
	Mode      string   `json:"mode"`       // none | sudo | su
	User      string   `json:"user"`       // target user; empty = root
	Password  string   `json:"password"`   // env:/file:/literal
	Nopasswd  bool     `json:"nopasswd"`   // sudoers already allows it without a password
	WrapShell bool     `json:"wrap_shell"` // run via `sh -c` (needed for root-side redirections)
	PTY       string   `json:"pty"`        // auto | always | never
	Always    bool     `json:"always"`     // elevate every command, no --sudo flag needed
	Deny      []string `json:"deny"`
	Allow     []string `json:"allow"`

	rawPassword string

	denyRe  []*regexp.Regexp
	allowRe []*regexp.Regexp
}

// MarshalJSON restores the indirection when the password was written that way.
func (e Elevate) MarshalJSON() ([]byte, error) {
	type plain Elevate
	out := plain(e)
	if e.rawPassword != "" {
		out.Password = e.rawPassword
	}
	return json.Marshal(out)
}

// Host is one configured remote server.
type Host struct {
	Alias string `json:"-"`
	Addr  string `json:"addr"`
	Port  int    `json:"port"`
	User  string `json:"user"`
	Group string `json:"group,omitempty"`
	// Favorite keeps a host at the front of the bookmarks bar.
	Favorite bool `json:"favorite,omitempty"`
	// Order is the position in the bookmarks bar (lower first).
	Order int `json:"order,omitempty"`
	// Forwards are tunnels opened with the session.
	Forwards []Forward `json:"forwards,omitempty"`
	Auth     Auth      `json:"auth"`
	HostKey  string    `json:"host_key"` // "known_hosts" (default) | "insecure" | "SHA256:..."
	Workdir  string    `json:"workdir"`
	Timeout  Duration  `json:"timeout"`
	Deny     []string  `json:"deny"`
	Allow    []string  `json:"allow"`
	Elevate  *Elevate  `json:"elevate,omitempty"`
	// Approval overrides defaults.approval for this host.
	Approval string `json:"approval,omitempty"`

	denyRe  []*regexp.Regexp
	allowRe []*regexp.Regexp
}

// Defaults are the global settings.
type Defaults struct {
	Timeout        Duration `json:"timeout"`
	ConnectTimeout Duration `json:"connect_timeout"`
	MaxOutput      int64    `json:"max_output"`
	IdleLock       Duration `json:"idle_lock"`
	KnownHosts     string   `json:"known_hosts"`
	UploadRoots    []string `json:"upload_roots"`
	DownloadRoots  []string `json:"download_roots"`
	AuditLog       string   `json:"audit_log"`
	// Approval decides when a human must confirm an AI action:
	// auto (never), elevate (only privileged), always.
	// Bookmarks is the order of the bookmarks bar; aliases not listed follow
	// in catalogue order.
	Bookmarks []string `json:"bookmarks,omitempty"`
	// Groups is the order of the server groups in the manager and the new-tab
	// menu. Groups that only exist on a host (an imported config, say) are
	// appended when the catalogue is parsed, and a group with no hosts still
	// shows, so it can be filled later.
	Groups []string `json:"groups,omitempty"`

	Approval        string   `json:"approval"`
	ApprovalTimeout Duration `json:"approval_timeout"`
}

// Config is the whole vault payload.
type Config struct {
	Defaults Defaults         `json:"defaults"`
	Hosts    map[string]*Host `json:"hosts"`

	// Derived, never marshalled.
	Dir     string `json:"-"`
	secrets []string
}

// Parse is Load for an in-memory JSON payload.
func Parse(data []byte, dir string) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config (JSON): %w", err)
	}
	c.Dir = dir
	if err := c.prepare(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Marshal renders the config as the JSON payload that goes inside the vault.
func (c *Config) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// EmptyPayload is what a freshly created vault holds: the defaults spelled out,
// so `ai-ssh config edit` shows every knob, and no hosts yet. The GUI's
// connection form or `config edit` adds the first one.
const EmptyPayloadJSON = `{
  "defaults": {
    "groups": [],
    "timeout": "60s",
    "connect_timeout": "10s",
    "max_output": 262144,
    "idle_lock": "30m",
    "known_hosts": "~/.ssh/known_hosts",
    "upload_roots": [],
    "download_roots": [],
    "audit_log": "",
    "approval": "auto",
    "approval_timeout": "120s"
  },
  "hosts": {}
}
`

// EmptyPayload validates and returns EmptyPayloadJSON.
func EmptyPayload() ([]byte, error) {
	plain := []byte(EmptyPayloadJSON)
	if _, err := Parse(plain, ""); err != nil {
		return nil, fmt.Errorf("built-in empty config is invalid: %w", err)
	}
	return plain, nil
}

// HostCount reports how many hosts a vault payload declares, without resolving
// secrets or validating the rest of the catalogue. It is used to avoid
// clobbering an existing catalogue by accident.
func HostCount(plain []byte) int {
	var probe struct {
		Hosts map[string]json.RawMessage `json:"hosts"`
	}
	if err := json.Unmarshal(plain, &probe); err != nil {
		return 0
	}
	return len(probe.Hosts)
}

// Redacted returns a copy that is safe to print: literal secrets become
// "***", while env:/file: references are kept so the user can still see where
// the secret comes from.
func (c *Config) Redacted() *Config {
	out := &Config{Defaults: c.Defaults, Dir: c.Dir, Hosts: make(map[string]*Host, len(c.Hosts))}
	for alias, h := range c.Hosts {
		if h == nil {
			continue
		}
		hc := *h
		if hc.Auth.rawPassword == "" && hc.Auth.Password != "" {
			hc.Auth.Password = "***"
		}
		if hc.Auth.rawPassphrase == "" && hc.Auth.Passphrase != "" {
			hc.Auth.Passphrase = "***"
		}
		if h.Elevate != nil {
			e := *h.Elevate
			if e.rawPassword == "" && e.Password != "" {
				e.Password = "***"
			}
			hc.Elevate = &e
		}
		out.Hosts[alias] = &hc
	}
	return out
}

func (c *Config) prepare() error {
	if c.Hosts == nil {
		c.Hosts = map[string]*Host{}
	}
	if c.Defaults.Timeout == 0 {
		c.Defaults.Timeout = Duration(DefaultTimeout)
	}
	if c.Defaults.ConnectTimeout == 0 {
		c.Defaults.ConnectTimeout = Duration(DefaultConnectTimeout)
	}
	if c.Defaults.MaxOutput <= 0 {
		c.Defaults.MaxOutput = DefaultMaxOutput
	}
	if c.Defaults.IdleLock == 0 {
		c.Defaults.IdleLock = Duration(DefaultIdleLock)
	}
	if c.Defaults.Approval == "" {
		c.Defaults.Approval = ApprovalAuto
	}
	if c.Defaults.ApprovalTimeout == 0 {
		c.Defaults.ApprovalTimeout = Duration(DefaultApprovalTimeout)
	}
	if err := validateApproval("defaults.approval", c.Defaults.Approval); err != nil {
		return err
	}
	if c.Defaults.KnownHosts == "" {
		c.Defaults.KnownHosts = "~/.ssh/known_hosts"
	}
	c.Defaults.KnownHosts = c.expand(c.Defaults.KnownHosts)
	c.Defaults.AuditLog = c.expand(c.Defaults.AuditLog)
	c.Defaults.UploadRoots = c.expandAll(c.Defaults.UploadRoots)
	c.Defaults.DownloadRoots = c.expandAll(c.Defaults.DownloadRoots)

	for alias, h := range c.Hosts {
		if h == nil {
			return fmt.Errorf("host %q is empty", alias)
		}
		h.Alias = alias
		if err := c.prepareHost(h); err != nil {
			return fmt.Errorf("host %q: %w", alias, err)
		}
	}

	// Keep the group order explicit, and fold in groups that only exist on a
	// host (an imported config) so the manager and the menu can show them.
	c.Defaults.Groups = normalizeGroupNames(c.Defaults.Groups)
	var derived []string
	for _, h := range c.Hosts {
		if h.Group != "" && !c.hasGroup(h.Group) {
			derived = append(derived, h.Group)
		}
	}
	sort.Strings(derived)
	c.Defaults.Groups = append(c.Defaults.Groups, normalizeGroupNames(derived)...)
	return nil
}

// normalizeGroupNames trims, drops empty names and removes exact duplicates,
// keeping the order the user chose.
func normalizeGroupNames(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

// hasGroup reports whether name is already in the group order (exact match).
func (c *Config) hasGroup(name string) bool {
	for _, g := range c.Defaults.Groups {
		if g == name {
			return true
		}
	}
	return false
}

// GroupExists reports whether a group with name (ignoring case) exists.
func (c *Config) GroupExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, g := range c.Defaults.Groups {
		if strings.EqualFold(g, name) {
			return true
		}
	}
	return false
}

// AddGroup records name at the end of the group order and reports whether it
// was new.
func (c *Config) AddGroup(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || c.hasGroup(name) {
		return false
	}
	c.Defaults.Groups = append(c.Defaults.Groups, name)
	return true
}

// RenameGroup renames a group in the order and on every host that uses it. A
// name that differs only in case from an existing group is refused, so the
// manager cannot end up with two sections that look the same.
func (c *Config) RenameGroup(old, next string) error {
	old, next = strings.TrimSpace(old), strings.TrimSpace(next)
	if next == "" {
		return fmt.Errorf("group name is required")
	}
	if old == next {
		return nil
	}
	for _, g := range c.Defaults.Groups {
		if g != old && strings.EqualFold(g, next) {
			return fmt.Errorf("group %q already exists", next)
		}
	}
	found := false
	for i, g := range c.Defaults.Groups {
		if g == old {
			c.Defaults.Groups[i] = next
			found = true
		}
	}
	if !found {
		c.AddGroup(next)
	}
	for _, h := range c.Hosts {
		if h.Group == old {
			h.Group = next
		}
	}
	return nil
}

// DeleteGroup forgets a group and leaves its hosts ungrouped; the servers are
// never deleted with it.
func (c *Config) DeleteGroup(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	kept := c.Defaults.Groups[:0]
	for _, g := range c.Defaults.Groups {
		if g != name {
			kept = append(kept, g)
		}
	}
	c.Defaults.Groups = kept
	for _, h := range c.Hosts {
		if h.Group == name {
			h.Group = ""
		}
	}
}

func (c *Config) prepareHost(h *Host) error {
	if h.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	if h.Port > 0 && !strings.Contains(h.Addr, ":") {
		h.Addr = net.JoinHostPort(h.Addr, strconv.Itoa(h.Port))
	}
	if _, _, err := net.SplitHostPort(h.Addr); err != nil {
		return fmt.Errorf("addr %q must be host:port: %w", h.Addr, err)
	}
	if h.User == "" {
		return fmt.Errorf("user is required")
	}
	if h.HostKey == "" {
		h.HostKey = "known_hosts"
	}
	if h.Approval != "" {
		if err := validateApproval(fmt.Sprintf("host %q approval", h.Alias), h.Approval); err != nil {
			return err
		}
	}
	// Workdir is a *remote* path: never resolve it against the local FS.
	h.Workdir = strings.TrimSpace(h.Workdir)
	h.Group = strings.TrimSpace(h.Group)

	// Keep the indirection (env:/file:) so saving the catalogue back to the
	// vault does not bake the resolved secret into it. A literal secret needs no
	// indirection: Marshal writes the value as it is.
	h.Auth.rawPassphrase = refOnly(h.Auth.Passphrase)
	h.Auth.rawPassword = refOnly(h.Auth.Password)

	switch h.Auth.Type {
	case "key":
		if h.Auth.Key == "" {
			return fmt.Errorf("auth.key is required for auth.type=key")
		}
		h.Auth.Key = c.expand(h.Auth.Key)
		h.Auth.Cert = c.expand(h.Auth.Cert)
	case "password":
		pw, err := c.resolve(h.Auth.Password)
		if err != nil {
			return fmt.Errorf("auth.password: %w", err)
		}
		h.Auth.Password = pw
		c.addSecret(pw)
	case "":
		return fmt.Errorf("auth.type is required (key or password)")
	default:
		return fmt.Errorf("unknown auth.type %q", h.Auth.Type)
	}

	if h.Auth.Passphrase != "" {
		pp, err := c.resolve(h.Auth.Passphrase)
		if err != nil {
			return fmt.Errorf("auth.passphrase: %w", err)
		}
		h.Auth.Passphrase = pp
		c.addSecret(pp)
	}

	if h.Elevate != nil {
		if err := c.prepareElevate(h); err != nil {
			return err
		}
	}
	for i := range h.Forwards {
		fw := &h.Forwards[i]
		switch fw.Kind {
		case "local", "remote", "dynamic":
		case "":
			fw.Kind = "local"
		default:
			return fmt.Errorf("forwards[%d]: kind must be local, remote or dynamic", i)
		}
		if fw.Listen == "" {
			return fmt.Errorf("forwards[%d]: listen is required", i)
		}
		if fw.Kind != "dynamic" && fw.Target == "" {
			return fmt.Errorf("forwards[%d]: target is required for a %s forward", i, fw.Kind)
		}
	}

	var err error
	if h.denyRe, err = compileAll(h.Deny); err != nil {
		return fmt.Errorf("deny: %w", err)
	}
	if h.allowRe, err = compileAll(h.Allow); err != nil {
		return fmt.Errorf("allow: %w", err)
	}
	return nil
}

var userNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func (c *Config) prepareElevate(h *Host) error {
	e := h.Elevate
	if e.Mode == "" || e.Mode == "none" {
		return nil
	}
	switch e.Mode {
	case "sudo", "su":
	default:
		return fmt.Errorf("elevate.mode must be sudo, su or none (got %q)", e.Mode)
	}
	if e.User != "" && !userNameRe.MatchString(e.User) {
		return fmt.Errorf("elevate.user %q is not a valid user name", e.User)
	}
	e.rawPassword = refOnly(e.Password)
	if e.Password != "" {
		pw, err := c.resolve(e.Password)
		if err != nil {
			return fmt.Errorf("elevate.password: %w", err)
		}
		e.Password = pw
		c.addSecret(pw)
	}
	if e.Mode == "su" && e.Password == "" {
		return fmt.Errorf("elevate.mode=su requires elevate.password (su always prompts)")
	}
	if e.PTY == "" {
		if e.Mode == "su" {
			e.PTY = "always"
		} else {
			e.PTY = "auto"
		}
	}
	switch e.PTY {
	case "auto", "always", "never":
	default:
		return fmt.Errorf("elevate.pty must be auto, always or never (got %q)", e.PTY)
	}

	var err error
	if e.denyRe, err = compileAll(e.Deny); err != nil {
		return fmt.Errorf("elevate.deny: %w", err)
	}
	if e.allowRe, err = compileAll(e.Allow); err != nil {
		return fmt.Errorf("elevate.allow: %w", err)
	}
	return nil
}

func compileAll(exprs []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(exprs))
	for _, e := range exprs {
		re, err := regexp.Compile(e)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", e, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Allowed checks the allow/deny lists for a remote command string.
func (h *Host) Allowed(cmd string) (bool, string) {
	for _, re := range h.denyRe {
		if re.MatchString(cmd) {
			return false, fmt.Sprintf("command matches deny rule %q", re.String())
		}
	}
	if len(h.allowRe) > 0 {
		for _, re := range h.allowRe {
			if re.MatchString(cmd) {
				return true, ""
			}
		}
		return false, "command does not match any allow rule"
	}
	return true, ""
}

// AllowedElevated applies the elevation specific policy. The host level
// allow/deny lists are checked separately and always apply.
func (h *Host) AllowedElevated(cmd string) (bool, string) {
	e := h.Elevate
	if e == nil || e.Mode == "" || e.Mode == "none" {
		return false, "elevation is not enabled for this host (configure elevate.mode)"
	}
	for _, re := range e.denyRe {
		if re.MatchString(cmd) {
			return false, fmt.Sprintf("command matches elevate.deny rule %q", re.String())
		}
	}
	if len(e.allowRe) > 0 {
		for _, re := range e.allowRe {
			if re.MatchString(cmd) {
				return true, ""
			}
		}
		return false, "command does not match any elevate.allow rule"
	}
	return true, ""
}

// NeedsApproval reports whether a human must confirm this action, and why.
func (c *Config) NeedsApproval(h *Host, elevated bool) (bool, string) {
	mode := h.Approval
	if mode == "" {
		mode = c.Defaults.Approval
	}
	switch mode {
	case ApprovalAlways:
		return true, "approval policy: every action on this host"
	case ApprovalElevate:
		if elevated {
			return true, "approval policy: privileged commands"
		}
	}
	return false, ""
}

// ApprovalWait is how long the daemon waits for a human decision.
func (c *Config) ApprovalWait() time.Duration {
	return c.Defaults.ApprovalTimeout.Or(DefaultApprovalTimeout)
}

// Timeout resolves the per-host timeout with fallbacks.
func (c *Config) Timeout(h *Host) time.Duration {
	return h.Timeout.Or(c.Defaults.Timeout.Or(DefaultTimeout))
}

func (c *Config) expand(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) && c.Dir != "" {
		p = filepath.Join(c.Dir, p)
	}
	return filepath.Clean(p)
}

func (c *Config) expandAll(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, c.expand(p))
	}
	return out
}

// refOnly keeps "env:NAME" / "file:PATH" references and drops literal values.
func refOnly(v string) string {
	if strings.HasPrefix(v, "env:") || strings.HasPrefix(v, "file:") {
		return v
	}
	return ""
}

// resolve turns "env:NAME" / "file:PATH" into the actual secret value.
func (c *Config) resolve(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, "env:"):
		name := strings.TrimPrefix(v, "env:")
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment variable %s is not set", name)
		}
		return val, nil
	case strings.HasPrefix(v, "file:"):
		path := c.expand(strings.TrimPrefix(v, "file:"))
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	default:
		return v, nil
	}
}

func (c *Config) addSecret(s string) {
	if len(s) < 4 {
		return
	}
	for _, existing := range c.secrets {
		if existing == s {
			return
		}
	}
	c.secrets = append(c.secrets, s)
}

// Secrets returns every literal secret the daemon must scrub from output.
func (c *Config) Secrets() []string { return c.secrets }

// Aliases returns the sorted set of host names.
func (c *Config) Aliases() []string {
	out := make([]string, 0, len(c.Hosts))
	for k := range c.Hosts {
		out = append(out, k)
	}
	return out
}

// Host resolves an alias.
func (c *Config) Host(alias string) (*Host, error) {
	h, ok := c.Hosts[alias]
	if !ok {
		return nil, fmt.Errorf("unknown host %q", alias)
	}
	return h, nil
}
