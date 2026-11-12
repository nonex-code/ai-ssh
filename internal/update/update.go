// Package update checks GitHub releases for a newer ai-ssh and installs the
// matching archive in place. It is used by the desktop window's settings.
//
// The release assets are the ones the Makefile produces: one archive per
// platform named ai-ssh-<version>-<goos>-<goarch>.zip|tar.gz, plus a
// SHA256SUMS.txt over all of them.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are read from.
const Repo = "nonex-code/ai-ssh"

const sumsAsset = "SHA256SUMS.txt"

// ErrNoReleases means the repository has no published release yet.
var ErrNoReleases = errors.New("no releases published yet")

// Release is the latest release, as far as this platform is concerned.
type Release struct {
	Version      string    `json:"version"`
	Tag          string    `json:"tag"`
	PageURL      string    `json:"page_url"`
	Notes        string    `json:"notes,omitempty"`
	PublishedAt  time.Time `json:"published_at"`
	ArchiveName  string    `json:"archive_name"`
	ArchiveURL   string    `json:"archive_url"`
	ArchiveSize  int64     `json:"archive_size"`
	ChecksumsURL string    `json:"-"`
	// Available is true when Version is newer than the running build.
	Available bool `json:"available"`
}

type releasePayload struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Check asks GitHub for the latest release and picks this platform's archive.
func Check(ctx context.Context, current string) (*Release, error) {
	url := "https://api.github.com/repos/" + Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ai-ssh/"+current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoReleases
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload releasePayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	tag := strings.TrimSpace(payload.TagName)
	version := strings.TrimPrefix(tag, "v")
	if version == "" {
		return nil, fmt.Errorf("release has no tag")
	}
	want := ArchiveName(version, runtime.GOOS, runtime.GOARCH)
	rel := &Release{
		Version:     version,
		Tag:         tag,
		PageURL:     payload.HTMLURL,
		Notes:       strings.TrimSpace(payload.Body),
		PublishedAt: payload.PublishedAt,
		Available:   compare(version, current) > 0,
	}
	for _, a := range payload.Assets {
		switch a.Name {
		case want:
			rel.ArchiveName, rel.ArchiveURL, rel.ArchiveSize = a.Name, a.URL, a.Size
		case sumsAsset:
			rel.ChecksumsURL = a.URL
		}
	}
	if rel.ArchiveURL == "" {
		return nil, fmt.Errorf("release %s has no archive for %s/%s", tag, runtime.GOOS, runtime.GOARCH)
	}
	return rel, nil
}

// ArchiveName is the release asset name for a version and platform.
func ArchiveName(version, goos, goarch string) string {
	name := fmt.Sprintf("ai-ssh-%s-%s-%s", version, goos, goarch)
	if goos == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

// current is the running version; "0.1.0+dev" compares as 0.1.0.
func compare(a, b string) int {
	parse := func(s string) ([]int, string) {
		s = strings.TrimPrefix(strings.TrimSpace(s), "v")
		s = strings.SplitN(s, "+", 2)[0]
		pre := ""
		if i := strings.IndexByte(s, '-'); i >= 0 {
			pre, s = s[i+1:], s[:i]
		}
		var nums []int
		for _, part := range strings.Split(s, ".") {
			n, _ := strconv.Atoi(part)
			nums = append(nums, n)
		}
		return nums, pre
	}
	an, apre := parse(a)
	bn, bpre := parse(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	// A release without a pre-release suffix is newer than one with it.
	switch {
	case apre == "" && bpre != "":
		return 1
	case apre != "" && bpre == "":
		return -1
	case apre < bpre:
		return -1
	case apre > bpre:
		return 1
	}
	return 0
}

// Download fetches the archive and verifies it against the release's
// SHA256SUMS.txt when the release carries one.
func Download(ctx context.Context, rel *Release, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, rel.ArchiveName)
	sum, err := download(ctx, rel.ArchiveURL, dst)
	if err != nil {
		return "", err
	}
	if rel.ChecksumsURL != "" {
		want, err := remoteSum(ctx, rel.ChecksumsURL, rel.ArchiveName)
		if err != nil {
			return "", err
		}
		if want != "" && !strings.EqualFold(want, sum) {
			_ = os.Remove(dst)
			return "", fmt.Errorf("checksum mismatch for %s", rel.ArchiveName)
		}
	}
	return dst, nil
}

func download(ctx context.Context, url, dst string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ai-ssh-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func remoteSum(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ai-ssh-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download checksums: %s", resp.Status)
	}
	return findSum(resp.Body, name), nil
}

// findSum reads "hash  name" lines and returns the hash for name.
func findSum(r io.Reader, name string) string {
	sc := bufio.NewScanner(io.LimitReader(r, 1<<20))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == name {
			return fields[0]
		}
	}
	return ""
}

// Install replaces the running executable with the one in archive. The caller
// must restart the app afterwards.
func Install(archive string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return installTo(archive, exe)
}

// installTo is Install with the target named, so tests do not replace the
// running test binary.
func installTo(archive, exe string) error {
	entry := entryName(exe)
	data, sums, err := readArchive(archive, entry)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("%s is not in %s", entry, filepath.Base(archive))
	}
	if want := findSum(strings.NewReader(sums), entry); want != "" {
		got := sha256.Sum256(data)
		if !strings.EqualFold(want, hex.EncodeToString(got[:])) {
			return fmt.Errorf("checksum mismatch for %s", entry)
		}
	} else {
		return fmt.Errorf("%s has no checksum for %s", filepath.Base(archive), entry)
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".ai-ssh-update-*")
	if err != nil {
		return fmt.Errorf("write next to %s: %w", exe, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return fmt.Errorf("rename running executable: %w", err)
		}
		if err := os.Rename(tmpName, exe); err != nil {
			_ = os.Rename(old, exe) // put the old one back
			return fmt.Errorf("install new executable: %w", err)
		}
		return nil
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return fmt.Errorf("install new executable: %w", err)
	}
	return nil
}

// CleanupOld removes the previous executable a Windows update renamed aside.
// It is a no-op elsewhere.
func CleanupOld() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}

func entryName(exe string) string {
	if runtime.GOOS == "windows" {
		return "ai-ssh.exe"
	}
	if runtime.GOOS == "darwin" && strings.Contains(filepath.ToSlash(exe), ".app/Contents/MacOS/") {
		return "ai-ssh.app/Contents/MacOS/ai-ssh"
	}
	return "ai-ssh"
}

// readArchive returns the named executable and the archive's inner
// SHA256SUMS.txt.
func readArchive(path, entry string) ([]byte, string, error) {
	switch {
	case strings.HasSuffix(path, ".zip"):
		return readZip(path, entry)
	case strings.HasSuffix(path, ".tar.gz"):
		return readTarGz(path, entry)
	default:
		return nil, "", fmt.Errorf("unsupported archive %s", filepath.Base(path))
	}
}

func readZip(path, entry string) ([]byte, string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, "", err
	}
	defer zr.Close()
	var data []byte
	var sums string
	for _, f := range zr.File {
		switch f.Name {
		case entry:
			rc, err := f.Open()
			if err != nil {
				return nil, "", err
			}
			data, err = io.ReadAll(io.LimitReader(rc, 1<<30))
			rc.Close()
			if err != nil {
				return nil, "", err
			}
		case sumsAsset:
			rc, err := f.Open()
			if err != nil {
				return nil, "", err
			}
			b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
			rc.Close()
			if err != nil {
				return nil, "", err
			}
			sums = string(b)
		}
	}
	return data, sums, nil
}

func readTarGz(path, entry string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var data []byte
	var sums string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}
		switch hdr.Name {
		case entry:
			data, err = io.ReadAll(io.LimitReader(tr, 1<<30))
			if err != nil {
				return nil, "", err
			}
		case sumsAsset:
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, "", err
			}
			sums = string(b)
		}
	}
	return data, sums, nil
}
