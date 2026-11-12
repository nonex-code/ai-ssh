package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

// FileEntry is one row of the remote directory listing.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	Link    bool      `json:"link"`
}

// Files is a remote file browser: an SFTP session owned by the window.
//
// Like Shell it is deliberately not reachable through the request protocol. An
// AI gets upload/download with the path policy; a human browses freely, because
// they are the owner and the paths come from a file picker.
type Files struct {
	srv    *Server
	host   string
	client *ssh.Client
	sc     *sftp.Client

	mu     sync.Mutex
	closed bool
}

// OpenFiles dials a dedicated SFTP session for one window tab.
func (s *Server) OpenFiles(ctx context.Context, alias string) (*Files, error) {
	h, err := s.config().Host(alias)
	if err != nil {
		return nil, errf(proto.ErrUnknownHost, "%v", err)
	}
	client, err := s.dial(ctx, h)
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, errf(proto.ErrTransfer, "start sftp: %v", err)
	}
	return &Files{srv: s, host: h.Alias, client: client, sc: sc}, nil
}

// Home is the directory the pane opens at: the host's workdir, else the login
// directory.
func (f *Files) Home() string {
	if h, err := f.srv.config().Host(f.host); err == nil && h.Workdir != "" {
		return h.Workdir
	}
	if wd, err := f.sc.Getwd(); err == nil && wd != "" {
		return wd
	}
	return "/"
}

// Close ends the SFTP session. Safe to call twice.
func (f *Files) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()

	_ = f.sc.Close()
	return f.client.Close()
}

// List returns a directory's entries, directories first.
func (f *Files) List(dir string) ([]FileEntry, error) {
	dir = cleanRemotePath(dir)
	infos, err := f.sc.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, FileEntry{
			Name:    fi.Name(),
			Path:    path.Join(dir, fi.Name()),
			Dir:     fi.IsDir(),
			Size:    fi.Size(),
			Mode:    fi.Mode().String(),
			ModTime: fi.ModTime(),
			Link:    fi.Mode()&os.ModeSymlink != 0,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (f *Files) Mkdir(p string) error {
	p = cleanRemotePath(p)
	if err := f.sc.MkdirAll(p); err != nil {
		return err
	}
	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "mkdir", Host: f.host, Remote: p, OK: true,
	})
	return nil
}

func (f *Files) Rename(from, to string) error {
	from, to = cleanRemotePath(from), cleanRemotePath(to)
	if err := f.sc.Rename(from, to); err != nil {
		return err
	}
	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "rename", Host: f.host, Remote: from + " → " + to, OK: true,
	})
	return nil
}

// Remove deletes a file, or a directory and everything under it.
func (f *Files) Remove(p string, recursive bool) error {
	p = cleanRemotePath(p)
	fi, err := f.sc.Stat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if !recursive {
			if err := f.sc.RemoveDirectory(p); err != nil {
				return err
			}
		} else {
			// Walk collects paths first, then removes deepest-first, because
			// Walk itself descends while removing.
			var paths []string
			w := f.sc.Walk(p)
			for w.Step() {
				if err := w.Err(); err != nil {
					return err
				}
				paths = append(paths, w.Path())
			}
			for i := len(paths) - 1; i >= 0; i-- {
				if paths[i] == p {
					if err := f.sc.RemoveDirectory(paths[i]); err != nil {
						return err
					}
					continue
				}
				if err := f.sc.Remove(paths[i]); err != nil {
					return err
				}
			}
		}
	} else if err := f.sc.Remove(p); err != nil {
		return err
	}

	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "remove", Host: f.host, Remote: p, OK: true,
	})
	return nil
}

// Progress reports bytes moved so far.
type Progress func(done, total int64)

// Transfer tunes one upload or download. Resume continues a partial file
// instead of starting over.
type Transfer struct {
	Progress Progress
	Resume   bool
}

// Upload copies a local file to the remote path.
func (f *Files) Upload(ctx context.Context, local, remote string, opts Transfer) (int64, error) {
	src, err := os.Open(local)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return 0, err
	}
	if fi.IsDir() {
		return 0, fmt.Errorf("%s is a directory", local)
	}

	remote = cleanRemotePath(remote)
	if dir := path.Dir(remote); dir != "." && dir != "/" {
		if err := f.sc.MkdirAll(dir); err != nil {
			return 0, err
		}
	}

	// O_APPEND is not reliable through the SFTP protocol, so a resumed upload
	// opens without O_TRUNC and seeks the remote handle instead.
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	var offset int64
	if opts.Resume {
		if rfi, err := f.sc.Stat(remote); err == nil && rfi.Size() > 0 && rfi.Size() < fi.Size() {
			offset = rfi.Size()
			flags = os.O_WRONLY | os.O_CREATE
		}
	}
	if offset > 0 {
		if _, err := src.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
	}

	dst, err := f.sc.OpenFile(remote, flags)
	if err != nil {
		return 0, err
	}
	if offset > 0 {
		if _, err := dst.Seek(offset, io.SeekStart); err != nil {
			_ = dst.Close()
			return 0, err
		}
	}

	n, err := copyWithProgress(ctx, dst, src, fi.Size(), offset, opts.Progress)
	cerr := dst.Close()
	if err != nil {
		return n, err
	}
	if cerr != nil {
		return n, cerr
	}
	_ = f.sc.Chmod(remote, fi.Mode().Perm())
	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "upload", Host: f.host, Remote: remote, OK: true, Bytes: n,
	})
	return n, nil
}

// Download copies a remote file to the local path.
func (f *Files) Download(ctx context.Context, remote, local string, opts Transfer) (int64, error) {
	remote = cleanRemotePath(remote)
	src, err := f.sc.Open(remote)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return 0, err
	}
	if fi.IsDir() {
		return 0, fmt.Errorf("%s is a directory", remote)
	}

	if err := os.MkdirAll(localDir(local), 0o755); err != nil {
		return 0, err
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	var offset int64
	if opts.Resume {
		if lfi, err := os.Stat(local); err == nil && lfi.Size() > 0 && lfi.Size() < fi.Size() {
			offset = lfi.Size()
			flags = os.O_WRONLY | os.O_APPEND
		}
	}
	if offset > 0 {
		if _, err := src.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
	}
	dst, err := os.OpenFile(local, flags, fi.Mode().Perm())
	if err != nil {
		return 0, err
	}

	n, err := copyWithProgress(ctx, dst, src, fi.Size(), offset, opts.Progress)
	cerr := dst.Close()
	if err != nil {
		return n, err
	}
	if cerr != nil {
		return n, cerr
	}
	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "download", Host: f.host, Remote: remote, Local: local, OK: true, Bytes: n,
	})
	return n, nil
}

// copyWithProgress is io.Copy with a callback and cancellation.
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, total, done int64, progress Progress) (int64, error) {
	buf := make([]byte, 128<<10)
	if progress != nil {
		progress(done, total)
	}
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return done, werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			return done, nil
		}
		if rerr != nil {
			return done, rerr
		}
	}
}

// localDir is filepath.Dir that copes with both separators.
func localDir(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i > 0 {
		return p[:i]
	}
	return "."
}

func cleanRemotePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") && !isWindowsAbsPath(p) {
		p = "/" + p
	}
	return path.Clean(p)
}

// UploadDir copies a whole local directory tree into remoteDir, creating it.
func (f *Files) UploadDir(ctx context.Context, localDir, remoteDir string, opts Transfer) (files int64, bytes int64, err error) {
	type item struct {
		local, remote string
		dir           bool
		size          int64
	}
	var items []item
	var total int64

	root, err := filepath.Abs(localDir)
	if err != nil {
		return 0, 0, err
	}
	err = filepath.Walk(root, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		target := cleanRemotePath(path.Join(remoteDir, filepath.ToSlash(rel)))
		if info.IsDir() {
			items = append(items, item{local: p, remote: target, dir: true})
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil // sockets, devices and links are not copied
		}
		total += info.Size()
		items = append(items, item{local: p, remote: target, size: info.Size()})
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	var done int64
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return files, done, err
		}
		if it.dir {
			if err := f.sc.MkdirAll(it.remote); err != nil {
				return files, done, err
			}
			continue
		}
		base := done
		n, uerr := f.Upload(ctx, it.local, it.remote, Transfer{
			Progress: func(d, _ int64) {
				if opts.Progress != nil {
					opts.Progress(base+d, total)
				}
			},
		})
		if uerr != nil {
			return files, done + n, uerr
		}
		done += n
		files++
	}
	if opts.Progress != nil {
		opts.Progress(done, total) // already at 100%
	}
	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "upload", Host: f.host, Local: root, Remote: remoteDir, OK: true, Bytes: done,
	})
	return files, done, nil
}

// DownloadDir copies a remote directory tree into localDir, creating it.
func (f *Files) DownloadDir(ctx context.Context, remoteDir, localDir string, opts Transfer) (files int64, bytes int64, err error) {
	type item struct {
		local, remote string
		dir           bool
		size          int64
	}
	remoteDir = cleanRemotePath(remoteDir)

	var items []item
	var total int64
	walker := f.sc.Walk(remoteDir)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return 0, 0, err
		}
		p := walker.Path()
		info := walker.Stat()
		if info == nil {
			continue
		}
		rel, rerr := filepath.Rel(filepath.FromSlash(remoteDir), filepath.FromSlash(p))
		if rerr != nil {
			return 0, 0, rerr
		}
		target := filepath.Join(localDir, rel)
		if info.IsDir() {
			items = append(items, item{local: target, remote: p, dir: true})
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		items = append(items, item{local: target, remote: p, size: info.Size()})
	}

	var done int64
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return files, done, err
		}
		if it.dir {
			if err := os.MkdirAll(it.local, 0o755); err != nil {
				return files, done, err
			}
			continue
		}
		base := done
		n, derr := f.Download(ctx, it.remote, it.local, Transfer{
			Progress: func(d, _ int64) {
				if opts.Progress != nil {
					opts.Progress(base+d, total)
				}
			},
		})
		if derr != nil {
			return files, done + n, derr
		}
		done += n
		files++
	}
	if opts.Progress != nil {
		opts.Progress(done, total)
	}
	f.srv.audit.write(auditEntry{
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		Op:   "download", Host: f.host, Remote: remoteDir, Local: localDir, OK: true, Bytes: done,
	})
	return files, done, nil
}
