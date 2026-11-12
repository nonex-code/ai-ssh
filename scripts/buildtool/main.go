// Command buildtool does the shell-independent parts of the release build:
// staging a platform's files, writing the archive, generating checksums,
// copying the native terminal library and cleaning build output.
//
// It is run through the Makefile with `go run ./scripts/buildtool`, so the
// same recipes work on Windows, Linux and macOS runners.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "build":
		cmdBuild(os.Args[2:])
	case "package":
		cmdPackage(os.Args[2:])
	case "checksums":
		cmdChecksums(os.Args[2:])
	case "embedvt":
		cmdEmbedVT(os.Args[2:])
	case "mkdir":
		cmdMkdir(os.Args[2:])
	case "clean":
		cmdClean(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `buildtool:
  build     -goos OS -goarch ARCH -version V -out PATH [-tags T] [-windowsgui]
  package   -goos OS -goarch ARCH -version V -bin PATH -stage DIR -out DIR
  checksums -dir DIR
  embedvt   -goos OS -goarch ARCH -native DIR -out DIR
  mkdir     DIR...
  clean
`)
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "buildtool: %v\n", err)
	os.Exit(1)
}

// cmdBuild runs `go build` with the target platform in its environment. Keeping
// GOOS/GOARCH off the make process itself lets `go run ./scripts/buildtool`
// build its host binary even when the target is another OS.
func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	goos := fs.String("goos", "", "target GOOS")
	goarch := fs.String("goarch", "", "target GOARCH")
	version := fs.String("version", "", "version to inject")
	out := fs.String("out", "", "output executable")
	tags := fs.String("tags", "", "build tags")
	windowsgui := fs.Bool("windowsgui", false, "use the Windows GUI subsystem")
	if err := fs.Parse(args); err != nil {
		fatal(err)
	}
	switch {
	case *goos == "":
		fatal(fmt.Errorf("build needs -goos"))
	case *goarch == "":
		fatal(fmt.Errorf("build needs -goarch"))
	case *version == "":
		fatal(fmt.Errorf("build needs -version"))
	case *out == "":
		fatal(fmt.Errorf("build needs -out"))
	}

	goArgs := []string{"build", "-trimpath"}
	if *tags != "" {
		goArgs = append(goArgs, "-tags", *tags)
	}
	ver := strings.TrimPrefix(*version, "v")
	ld := "-s -w -X github.com/nonex-code/ai-ssh/internal/version.Value=" + ver
	if *windowsgui {
		ld += " -H=windowsgui"
	}
	goArgs = append(goArgs, "-ldflags", ld, "-o", *out, ".")

	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GOOS="),
			strings.HasPrefix(kv, "GOARCH="),
			strings.HasPrefix(kv, "CGO_ENABLED="):
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GOOS="+*goos, "GOARCH="+*goarch, "CGO_ENABLED=0")

	fmt.Printf("build: GOOS=%s GOARCH=%s -> %s\n", *goos, *goarch, *out)
	cmd := exec.Command("go", goArgs...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(err)
	}
}

func cmdMkdir(args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("mkdir needs at least one directory"))
	}
	for _, dir := range args {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatal(err)
		}
	}
}

// cmdEmbedVT copies the fetched terminal library into internal/vtlib under the
// name its platform build tag embeds, so one tree can carry every platform.
func cmdEmbedVT(args []string) {
	fs := flag.NewFlagSet("embedvt", flag.ExitOnError)
	goos := fs.String("goos", "", "target GOOS")
	goarch := fs.String("goarch", "", "target GOARCH")
	native := fs.String("native", "", "directory holding the fetched library")
	out := fs.String("out", "internal/vtlib", "embed output directory")
	if err := fs.Parse(args); err != nil {
		fatal(err)
	}
	if *goos == "" || *goarch == "" || *native == "" {
		fatal(fmt.Errorf("embedvt needs -goos, -goarch and -native"))
	}
	var name, embedded string
	switch *goos {
	case "windows":
		name, embedded = "ghostty-vt.dll", "ghostty-vt-"+*goos+"-"+*goarch+".dll"
	case "darwin":
		name, embedded = "libghostty-vt.dylib", "libghostty-vt-"+*goos+"-"+*goarch+".dylib"
	case "linux":
		name, embedded = "libghostty-vt.so", "libghostty-vt-"+*goos+"-"+*goarch+".so"
	default:
		fatal(fmt.Errorf("no embedded terminal library for %s/%s", *goos, *goarch))
	}
	src := filepath.Join(*native, name)
	dst := filepath.Join(*out, embedded)
	if err := copyFile(src, dst, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("embedded %s -> %s\n", src, dst)
}

func cmdChecksums(args []string) {
	fs := flag.NewFlagSet("checksums", flag.ExitOnError)
	dir := fs.String("dir", "build", "directory holding the release archives")
	if err := fs.Parse(args); err != nil {
		fatal(err)
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		fatal(fmt.Errorf("no archives in %s", *dir))
	}
	sort.Strings(names)

	var out strings.Builder
	for _, name := range names {
		sum, err := fileSHA256(filepath.Join(*dir, name))
		if err != nil {
			fatal(err)
		}
		fmt.Fprintf(&out, "%s  %s\n", sum, name)
	}
	dst := filepath.Join(*dir, "SHA256SUMS.txt")
	if err := os.WriteFile(dst, []byte(out.String()), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s (%d archives)\n", dst, len(names))
}

func cmdClean(args []string) {
	var failed []string
	for _, p := range []string{
		"build", "ai-ssh", "ai-ssh.exe",
	} {
		if err := os.RemoveAll(p); err != nil {
			// A running ai-ssh can hold build/*.exe open on Windows; keep
			// removing the rest, then say what is left.
			failed = append(failed, fmt.Sprintf("%s: %v", p, err))
		}
	}
	for _, pattern := range []string{"internal/vtlib/*.dll", "internal/vtlib/*.so", "internal/vtlib/*.dylib"} {
		paths, _ := filepath.Glob(pattern)
		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", p, err))
			}
		}
	}
	if len(failed) > 0 {
		fatal(fmt.Errorf("could not fully clean (close the app and retry): %s", strings.Join(failed, "; ")))
	}
	fmt.Println("cleaned build/ and old root binaries")
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
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

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
