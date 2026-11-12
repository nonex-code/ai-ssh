package gui

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nonex-code/ai-ssh/internal/agent"
)

// localList reads a local directory into the same shape the remote pane uses,
// so one renderer draws both.
func localList(dir string) ([]agent.FileEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]agent.FileEntry, 0, len(items))
	for _, it := range items {
		info, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, agent.FileEntry{
			Name:    it.Name(),
			Path:    filepath.Join(dir, it.Name()),
			Dir:     it.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime(),
			Link:    info.Mode()&os.ModeSymlink != 0,
		})
	}
	sortEntries(out)
	return out, nil
}

func localHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func localMkdir(p string) error { return os.MkdirAll(p, 0o755) }

func localRename(from, to string) error { return os.Rename(from, to) }

func localRemove(p string, recursive bool) error {
	if !recursive {
		return os.Remove(p)
	}
	return os.RemoveAll(p)
}

func sortEntries(list []agent.FileEntry) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Dir != list[j].Dir {
			return list[i].Dir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

// parentOf returns the parent directory of p, or p itself at the root.
func parentOf(p string, remote bool) string {
	var up string
	if remote {
		up = path.Dir(p)
	} else {
		up = filepath.Dir(p)
	}
	if up == p || up == "" {
		return p
	}
	return up
}

// joinPath joins a directory and a name with the right separator.
func joinPath(dir, name string, remote bool) string {
	if remote {
		return path.Join(dir, name)
	}
	return filepath.Join(dir, name)
}

func baseOf(p string, remote bool) string {
	if remote {
		return path.Base(p)
	}
	return filepath.Base(p)
}

var _ = time.Now
