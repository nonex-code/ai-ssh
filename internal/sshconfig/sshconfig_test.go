package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
# global defaults
Host *
    User default-user
    Port 2200
    IdentityFile ~/.ssh/default_key

Host prod
    HostName 10.0.0.5
    User deploy

Host staging stg
    HostName 10.0.0.9
    IdentityFile "~/.ssh/staging key"

Host *.internal
    User ignored

Host jump
    HostName=bastion.example.com
    Port=2222
    User ops
`

func TestParse(t *testing.T) {
	entries, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 4 {
		t.Fatalf("entries = %d: %+v", len(entries), entries)
	}
	byAlias := map[string]Entry{}
	for _, e := range entries {
		byAlias[e.Alias] = e
	}
	if _, ok := byAlias["*.internal"]; ok {
		t.Fatal("wildcard patterns must be skipped")
	}

	prod := byAlias["prod"]
	if prod.HostName != "10.0.0.5" || prod.User != "deploy" || prod.Port != "2200" {
		t.Fatalf("prod = %+v", prod)
	}
	if prod.IdentityFile != "~/.ssh/default_key" {
		t.Fatalf("default identity not applied: %+v", prod)
	}

	stg := byAlias["stg"]
	if stg.HostName != "10.0.0.9" || stg.IdentityFile != "~/.ssh/staging key" {
		t.Fatalf("stg = %+v", stg)
	}

	jump := byAlias["jump"]
	if jump.Address() != "bastion.example.com:2222" || jump.User != "ops" {
		t.Fatalf("jump = %+v", jump)
	}
}

func TestLoadMissingFile(t *testing.T) {
	entries, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil || entries != nil {
		t.Fatalf("missing config should be empty: %v %v", entries, err)
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d", len(entries))
	}
}

func TestInclude(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra.conf")
	if err := os.WriteFile(extra, []byte("Host included\n    HostName 10.9.9.9\n    User ci\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "config")
	body := "Host first\n    HostName 10.0.0.1\n\nInclude " + extra + "\n\nHost last\n    HostName 10.0.0.2\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	aliases := []string{entries[0].Alias, entries[1].Alias, entries[2].Alias}
	if aliases[0] != "first" || aliases[1] != "included" || aliases[2] != "last" {
		t.Fatalf("order = %v", aliases)
	}
	if entries[1].User != "ci" || entries[1].HostName != "10.9.9.9" {
		t.Fatalf("included entry = %+v", entries[1])
	}
}
