package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// checkLocalPath confines a client-supplied path to the configured roots. This
// is the guard that stops an agent from using `upload` to exfiltrate arbitrary
// local files (e.g. its own private key) and `download` to overwrite the
// ai-ssh binary or the vault.
func (s *Server) checkLocalPath(p string, roots []string, mustExist bool) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errf(proto.ErrUsage, "local path is required")
	}
	abs, err := filepath.Abs(expandHome(p))
	if err != nil {
		return "", errf(proto.ErrPathDenied, "resolve %q: %v", p, err)
	}
	abs = filepath.Clean(abs)
	real := evalExisting(abs)

	if mustExist {
		if fi, err := os.Stat(abs); err != nil {
			return "", errf(proto.ErrPathDenied, "local file %s is not readable: %v", abs, err)
		} else if fi.IsDir() {
			return "", errf(proto.ErrPathDenied, "local path %s is a directory", abs)
		}
	}

	for _, prot := range s.protected {
		if withinPath(prot, real) || withinPath(prot, abs) {
			return "", errf(proto.ErrPathDenied, "local path %s is protected", abs)
		}
	}

	for _, root := range roots {
		if withinPath(evalExisting(root), real) || withinPath(evalExisting(root), abs) {
			return abs, nil
		}
	}
	return "", errf(proto.ErrPathDenied,
		"local path %s is outside the allowed roots (%s); adjust defaults.upload_roots / download_roots",
		abs, strings.Join(roots, ", "))
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// evalExisting resolves symlinks for the longest existing prefix of p so that
// a symlink cannot be used to hop out of an allowed root.
func evalExisting(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	base := filepath.Base(p)
	dir := filepath.Dir(p)
	if dir == p {
		return p
	}
	return filepath.Join(evalExisting(dir), base)
}

func withinPath(base, target string) bool {
	if base == "" || target == "" {
		return false
	}
	b, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	t, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		b = strings.ToLower(b)
		t = strings.ToLower(t)
	}
	rel, err := filepath.Rel(b, t)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
