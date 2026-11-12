package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// cmdPackage lays out one platform's release directory and archives it.
//
// The archive is named with the version and platform, and holds the files at
// its root: the executable is always ai-ssh (ai-ssh.exe on Windows), never a
// platform-suffixed name.
func cmdPackage(args []string) {
	fs := flag.NewFlagSet("package", flag.ExitOnError)
	goos := fs.String("goos", "", "target GOOS")
	goarch := fs.String("goarch", "", "target GOARCH")
	version := fs.String("version", "", "release version, without a leading v")
	bin := fs.String("bin", "", "built executable")
	stage := fs.String("stage", "", "staging directory")
	out := fs.String("out", "build", "output directory")
	if err := fs.Parse(args); err != nil {
		fatal(err)
	}
	switch {
	case *goos == "":
		fatal(fmt.Errorf("package needs -goos"))
	case *goarch == "":
		fatal(fmt.Errorf("package needs -goarch"))
	case *version == "":
		fatal(fmt.Errorf("package needs -version"))
	case *bin == "":
		fatal(fmt.Errorf("package needs -bin"))
	case *stage == "":
		fatal(fmt.Errorf("package needs -stage"))
	}
	ver := strings.TrimPrefix(*version, "v")

	if err := os.RemoveAll(*stage); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*stage, 0o755); err != nil {
		fatal(err)
	}

	binName := "ai-ssh"
	if *goos == "windows" {
		binName = "ai-ssh.exe"
	}
	if err := copyFile(*bin, filepath.Join(*stage, binName), 0o755); err != nil {
		fatal(err)
	}

	common := []string{
		"README.md",
		"README.en.md",
		"internal/gui/assets/fonts/OFL-Inter.txt",
		"internal/gui/assets/fonts/OFL-JetBrainsMono.txt",
	}
	for _, name := range common {
		if err := copyFile(name, filepath.Join(*stage, filepath.Base(name)), 0o644); err != nil {
			fatal(err)
		}
	}

	// Executables whose hashes go into the archive's SHA256SUMS.txt.
	sums := []string{binName}
	switch *goos {
	case "windows":
		if err := copyFile("internal/gui/assets/logo.ico", filepath.Join(*stage, "logo.ico"), 0o644); err != nil {
			fatal(err)
		}

	case "darwin":
		if err := stageMacApp(*stage, *bin, ver); err != nil {
			fatal(err)
		}
		// The raw binary is included too, for people who run it from a shell.
		if err := copyFile(*bin, filepath.Join(*stage, "ai-ssh"), 0o755); err != nil {
			fatal(err)
		}
		sums = append(sums, "ai-ssh.app/Contents/MacOS/ai-ssh")

	case "linux":
		files := map[string]string{
			"packaging/linux/ai-ssh.desktop":   "ai-ssh.desktop",
			"packaging/linux/install.sh":       "install-icons.sh",
			"internal/gui/assets/logo-512.png": "ai-ssh.png",
		}
		for src, name := range files {
			mode := os.FileMode(0o644)
			if strings.HasSuffix(name, ".sh") {
				mode = 0o755
			}
			if err := copyFile(src, filepath.Join(*stage, name), mode); err != nil {
				fatal(err)
			}
		}

	default:
		fatal(fmt.Errorf("unsupported GOOS %q", *goos))
	}

	if err := writeInnerChecksums(*stage, sums); err != nil {
		fatal(err)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	archive := filepath.Join(*out, fmt.Sprintf("ai-ssh-%s-%s-%s", ver, *goos, *goarch))
	if *goos == "windows" {
		archive += ".zip"
		if err := writeZip(*stage, archive); err != nil {
			fatal(err)
		}
	} else {
		archive += ".tar.gz"
		if err := writeTarGz(*stage, archive); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("package: %s\n", archive)
}

// stageMacApp builds ai-ssh.app inside the staging directory.
func stageMacApp(stage, bin, version string) error {
	app := filepath.Join(stage, "ai-ssh.app")
	macOS := filepath.Join(app, "Contents", "MacOS")
	resources := filepath.Join(app, "Contents", "Resources")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(resources, 0o755); err != nil {
		return err
	}
	if err := copyFile(bin, filepath.Join(macOS, "ai-ssh"), 0o755); err != nil {
		return err
	}
	if err := copyFile("internal/gui/assets/logo.icns", filepath.Join(resources, "logo.icns"), 0o644); err != nil {
		return err
	}
	tmpl, err := os.ReadFile("packaging/macos/Info.plist.in")
	if err != nil {
		return err
	}
	plist := strings.ReplaceAll(string(tmpl), "@VERSION@", version)
	return os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644)
}

// writeInnerChecksums writes the SHA256SUMS.txt that ships inside the archive,
// covering the executable(s) it carries.
func writeInnerChecksums(stage string, names []string) error {
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		path := filepath.Join(stage, filepath.FromSlash(name))
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&out, "%s  %s\n", sum, name)
	}
	return os.WriteFile(filepath.Join(stage, "SHA256SUMS.txt"), []byte(out.String()), 0o644)
}
