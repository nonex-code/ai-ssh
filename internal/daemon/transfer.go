package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/pkg/sftp"

	"github.com/nonex-code/ai-ssh/internal/proto"
)

func (s *Server) handleUpload(ctx context.Context, req proto.Request) proto.Response {
	h, err := s.config().Host(req.Host)
	if err != nil {
		return fail(proto.ErrUnknownHost, err.Error(), req.Host)
	}
	remote, err := cleanRemote(req.Remote)
	if err != nil {
		return fail(proto.ErrUsage, err.Error(), req.Host)
	}
	local, err := s.checkLocalPath(req.Local, s.config().Defaults.UploadRoots, true)
	if err != nil {
		code, msg := codeOf(err)
		return fail(code, msg, req.Host)
	}

	approval, denied := s.gate(ctx, h, proto.Approval{
		Op: proto.OpUpload, Local: local, Remote: remote,
	})
	if denied != nil {
		return *denied
	}

	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()

	client, err := s.dial(ctx, h)
	if err != nil {
		code, msg := codeOf(err)
		return fail(code, msg, req.Host)
	}
	defer client.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return fail(proto.ErrTransfer, "start sftp: "+err.Error(), req.Host)
	}
	defer sc.Close()

	srcInfo, err := os.Stat(local)
	if err != nil {
		return fail(proto.ErrTransfer, "stat local: "+err.Error(), req.Host)
	}
	if srcInfo.IsDir() {
		return fail(proto.ErrUnsupported, "uploading directories is not supported (tar it first)", req.Host)
	}
	src, err := os.Open(local)
	if err != nil {
		return fail(proto.ErrTransfer, "open local: "+err.Error(), req.Host)
	}
	defer src.Close()

	if req.Mkdir {
		if dir := path.Dir(remote); dir != "." && dir != "/" {
			if err := sc.MkdirAll(dir); err != nil {
				return fail(proto.ErrTransfer, "mkdir "+dir+": "+err.Error(), req.Host)
			}
		}
	}

	mode, err := parseMode(req.Mode, 0o644)
	if err != nil {
		return fail(proto.ErrUsage, err.Error(), req.Host)
	}

	dst, err := sc.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fail(proto.ErrTransfer, "open remote: "+err.Error(), req.Host)
	}

	sum := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(dst, sum), src)
	closeErr := dst.Close()
	if copyErr != nil {
		return fail(proto.ErrTransfer, "write remote: "+copyErr.Error(), req.Host)
	}
	if closeErr != nil {
		return fail(proto.ErrTransfer, "close remote: "+closeErr.Error(), req.Host)
	}
	if err := sc.Chmod(remote, mode); err != nil {
		return fail(proto.ErrTransfer, "chmod remote: "+err.Error(), req.Host)
	}

	want := hex.EncodeToString(sum.Sum(nil))
	if req.Verify {
		got, err := remoteSHA256(sc, remote)
		if err != nil {
			return fail(proto.ErrTransfer, "verify remote: "+err.Error(), req.Host)
		}
		if got != want {
			return fail(proto.ErrTransfer,
				fmt.Sprintf("checksum mismatch: local %s remote %s", want, got), req.Host)
		}
	}

	resp := ok(proto.OpUpload, req.Host)
	resp.Local = local
	resp.Remote = remote
	resp.Bytes = written
	resp.Sha256 = want
	resp.Approval = approval
	return resp
}

func (s *Server) handleDownload(ctx context.Context, req proto.Request) proto.Response {
	h, err := s.config().Host(req.Host)
	if err != nil {
		return fail(proto.ErrUnknownHost, err.Error(), req.Host)
	}
	remote, err := cleanRemote(req.Remote)
	if err != nil {
		return fail(proto.ErrUsage, err.Error(), req.Host)
	}
	local, err := s.checkLocalPath(req.Local, s.config().Defaults.DownloadRoots, false)
	if err != nil {
		code, msg := codeOf(err)
		return fail(code, msg, req.Host)
	}
	if err := os.MkdirAll(parentDir(local), 0o755); err != nil {
		return fail(proto.ErrTransfer, "create local dir: "+err.Error(), req.Host)
	}

	approval, denied := s.gate(ctx, h, proto.Approval{
		Op: proto.OpDownload, Local: local, Remote: remote,
	})
	if denied != nil {
		return *denied
	}

	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()

	client, err := s.dial(ctx, h)
	if err != nil {
		code, msg := codeOf(err)
		return fail(code, msg, req.Host)
	}
	defer client.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return fail(proto.ErrTransfer, "start sftp: "+err.Error(), req.Host)
	}
	defer sc.Close()

	remoteInfo, err := sc.Stat(remote)
	if err != nil {
		return fail(proto.ErrTransfer, "stat remote: "+err.Error(), req.Host)
	}
	if remoteInfo.IsDir() {
		return fail(proto.ErrUnsupported, "downloading directories is not supported (tar it first)", req.Host)
	}
	src, err := sc.Open(remote)
	if err != nil {
		return fail(proto.ErrTransfer, "open remote: "+err.Error(), req.Host)
	}
	defer src.Close()

	mode, err := parseMode(req.Mode, 0o644)
	if err != nil {
		return fail(proto.ErrUsage, err.Error(), req.Host)
	}

	dst, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fail(proto.ErrTransfer, "open local: "+err.Error(), req.Host)
	}

	sum := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(dst, sum), src)
	closeErr := dst.Close()
	if copyErr != nil {
		return fail(proto.ErrTransfer, "write local: "+copyErr.Error(), req.Host)
	}
	if closeErr != nil {
		return fail(proto.ErrTransfer, "close local: "+closeErr.Error(), req.Host)
	}

	want := hex.EncodeToString(sum.Sum(nil))
	if req.Verify {
		got, err := localSHA256(local)
		if err != nil {
			return fail(proto.ErrTransfer, "verify local: "+err.Error(), req.Host)
		}
		if got != want {
			return fail(proto.ErrTransfer,
				fmt.Sprintf("checksum mismatch: stream %s local %s", want, got), req.Host)
		}
	}

	resp := ok(proto.OpDownload, req.Host)
	resp.Local = local
	resp.Remote = remote
	resp.Bytes = written
	resp.Sha256 = want
	resp.Approval = approval
	return resp
}

func cleanRemote(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("remote path is required")
	}
	// Always absolute so the SFTP server's cwd cannot surprise us. Windows
	// style drive paths are left alone (only relevant for tests, real remotes
	// are POSIX).
	if !strings.HasPrefix(p, "/") && !isWindowsAbsPath(p) {
		p = "/" + p
	}
	return path.Clean(p), nil
}

func isWindowsAbsPath(p string) bool {
	if len(p) < 3 || p[1] != ':' {
		return false
	}
	c := p[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return false
	}
	return p[2] == '/' || p[2] == '\\'
}

func parseMode(s string, def os.FileMode) (os.FileMode, error) {
	if s == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "0o"), 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q (want octal like 0644)", s)
	}
	return os.FileMode(v), nil
}

func parentDir(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 {
		return "."
	}
	return p[:i]
}

func remoteSHA256(sc *sftp.Client, p string) (string, error) {
	f, err := sc.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func localSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
