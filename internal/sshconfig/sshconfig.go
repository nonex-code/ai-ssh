// Package sshconfig reads OpenSSH client configuration, enough of it to import
// the hosts someone already has.
package sshconfig

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one concrete host block.
type Entry struct {
	Alias        string
	HostName     string
	User         string
	Port         string
	IdentityFile string
}

type block struct {
	patterns []string
	values   map[string]string
}

// Load reads a config file and the files it Includes. A missing file is not an
// error: it returns no entries, so the window can offer the import harmlessly.
func Load(path string) ([]Entry, error) {
	text, err := expand(path, 0, map[string]bool{})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	return Parse(strings.NewReader(text))
}

// expand inlines Include directives, as OpenSSH does: relative paths are
// relative to ~/.ssh, patterns glob, and a file is read once.
func expand(path string, depth int, seen map[string]bool) (string, error) {
	if depth > 8 || seen[path] {
		return "", nil
	}
	seen[path] = true

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && depth > 0 {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	var out strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		key, value := splitDirective(strings.TrimSpace(line))
		if !strings.EqualFold(key, "include") {
			out.WriteString(line)
			out.WriteByte(10)
			continue
		}
		for _, pattern := range strings.Fields(value) {
			for _, file := range resolveInclude(pattern) {
				sub, err := expand(file, depth+1, seen)
				if err != nil {
					return "", err
				}
				out.WriteString(sub)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// resolveInclude expands ~ and relative patterns, then globs them.
func resolveInclude(pattern string) []string {
	if strings.HasPrefix(pattern, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			pattern = filepath.Join(home, pattern[1:])
		}
	}
	if !filepath.IsAbs(pattern) {
		if home, err := os.UserHomeDir(); err == nil {
			pattern = filepath.Join(home, ".ssh", pattern)
		}
	}
	if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
		return matches
	}
	return []string{pattern}
}

// Parse reads OpenSSH configuration: Host blocks with HostName, User, Port and
// IdentityFile, and the defaults of a `Host *` block.
func Parse(r io.Reader) ([]Entry, error) {
	var blocks []*block
	var cur *block
	defaults := map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value := splitDirective(line)
		switch strings.ToLower(key) {
		case "host":
			cur = &block{patterns: strings.Fields(value), values: map[string]string{}}
			blocks = append(blocks, cur)
		case "match":
			cur = nil // unsupported: stop collecting into the previous block
		default:
			if cur == nil {
				continue
			}
			cur.values[strings.ToLower(key)] = unquote(value)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// `Host *` blocks are defaults for everything else.
	for _, b := range blocks {
		if hasPattern(b, "*") {
			for k, v := range b.values {
				defaults[k] = v
			}
		}
	}

	var out []Entry
	seen := map[string]bool{}
	for _, b := range blocks {
		for _, pattern := range b.patterns {
			if !concrete(pattern) || seen[pattern] {
				continue
			}
			seen[pattern] = true

			entry := Entry{Alias: pattern}
			entry.HostName = value(b, defaults, "hostname")
			if entry.HostName == "" {
				entry.HostName = pattern
			}
			entry.User = value(b, defaults, "user")
			entry.Port = value(b, defaults, "port")
			entry.IdentityFile = value(b, defaults, "identityfile")
			out = append(out, entry)
		}
	}
	return out, nil
}

func value(b *block, defaults map[string]string, key string) string {
	if v, ok := b.values[key]; ok {
		return v
	}
	return defaults[key]
}

func hasPattern(b *block, want string) bool {
	for _, p := range b.patterns {
		if p == want {
			return true
		}
	}
	return false
}

// concrete reports whether a Host pattern names one host, not a set.
func concrete(p string) bool {
	if p == "" || p == "*" {
		return false
	}
	return !strings.ContainsAny(p, "*?![")
}

func splitDirective(line string) (key, value string) {
	if i := strings.IndexByte(line, '='); i > 0 {
		k := strings.TrimSpace(line[:i])
		if !strings.ContainsAny(k, " \t") {
			return k, strings.TrimSpace(line[i+1:])
		}
	}
	fields := strings.SplitN(line, " ", 2)
	if len(fields) == 1 {
		return fields[0], ""
	}
	return fields[0], strings.TrimSpace(fields[1])
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// DefaultPath is where OpenSSH keeps the user's client config.
func DefaultPath() string {
	if p := os.Getenv("AISSH_SSH_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// Address renders host:port with the OpenSSH default.
func (e Entry) Address() string {
	port := e.Port
	if port == "" {
		port = "22"
	}
	return fmt.Sprintf("%s:%s", e.HostName, port)
}
