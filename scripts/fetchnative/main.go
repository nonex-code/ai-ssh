//go:build ignore

// fetchnative downloads the native library the terminal widget loads
// (libghostty-vt) next to the GUI binary, so the app never has to download it at
// startup. It reads the checksums from the mygo module itself, so it survives
// plugin upgrades.
//
//	go run scripts/fetchnative/main.go -out build/native
//	MYGO_MIRROR=https://ghproxy.net go run scripts/fetchnative/main.go -out build/native
//
// build/native keeps a copy for development; release builds embed it into the
// executable and extract it to ~/.ai-ssh/lib/vt on first use.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type manifest struct {
	Libraries []struct {
		Name  string `json:"name"`
		Files map[string]struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
			URL    string `json:"url"`
			// Some manifests call it "source" instead.
		} `json:"files"`
	} `json:"libraries"`
}

func main() {
	out := flag.String("out", "build/native", "directory to write the library into")
	mirror := flag.String("mirror", os.Getenv("MYGO_MIRROR"), "mirror prefix, e.g. https://ghproxy.net")
	goos := flag.String("goos", runtime.GOOS, "target GOOS (default: this machine)")
	goarch := flag.String("goarch", runtime.GOARCH, "target GOARCH (default: this machine)")
	flag.Parse()

	dir, err := moduleDir("github.com/egoist/mygo")
	if err != nil {
		fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plugins", "terminal", "mygo-plugin.json"))
	if err != nil {
		fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fatal(err)
	}
	if len(m.Libraries) == 0 {
		fatal(fmt.Errorf("no libraries in the manifest"))
	}

	key := *goos + "-" + *goarch
	file, ok := m.Libraries[0].Files[key]
	if !ok {
		fatal(fmt.Errorf("%s has no build of %s", key, m.Libraries[0].Name))
	}

	url := file.URL
	if *mirror != "" {
		url = strings.TrimRight(*mirror, "/") + "/" + url
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	dst := filepath.Join(*out, file.Name)
	// A release build runs this after the library is already in build/native/. Do not
	// download again (and do not fail on Windows because a running app has the
	// DLL loaded): the file is content-addressed by the manifest checksum.
	if existing, err := os.ReadFile(dst); err == nil {
		sum := sha256.Sum256(existing)
		if strings.EqualFold(hex.EncodeToString(sum[:]), file.SHA256) {
			fmt.Printf("%s is already present (sha256 %s)\n", dst, file.SHA256[:16]+"…")
			return
		}
		fmt.Fprintf(os.Stderr, "fetchnative: %s does not match the manifest; if an ai-ssh window is running, close it first\n", dst)
	}
	fmt.Printf("downloading %s\n  from %s\n", file.Name, url)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		fatal(fmt.Errorf("%w\nhint: set MYGO_MIRROR=https://ghproxy.net if github.com is blocked", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("%s: %s", url, resp.Status))
	}

	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		fatal(err)
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		fatal(err)
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(got, file.SHA256) {
		os.Remove(tmp)
		fatal(fmt.Errorf("checksum mismatch: got %s, want %s", got, file.SHA256))
	}
	if err := os.Rename(tmp, dst); err != nil {
		fatal(fmt.Errorf("%w (if an ai-ssh window is running, close it and retry)", err))
	}
	fmt.Printf("wrote %s (%d bytes, sha256 %s)\n", dst, n, got[:16]+"…")
}

func moduleDir(path string) (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", path).Output()
	if err != nil {
		return "", fmt.Errorf("go list %s: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "fetchnative: %v\n", err)
	os.Exit(1)
}
