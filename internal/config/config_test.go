package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDurationJSONForms(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{`"90s"`, 90 * time.Second},
		{`"1m30s"`, 90 * time.Second},
		{`90`, 90 * time.Second},
		{`null`, 0},
	}
	for _, c := range cases {
		var d Duration
		if err := json.Unmarshal([]byte(c.in), &d); err != nil {
			t.Fatalf("unmarshal %s: %v", c.in, err)
		}
		if time.Duration(d) != c.want {
			t.Errorf("%s = %v, want %v", c.in, time.Duration(d), c.want)
		}
	}

	out, err := json.Marshal(Duration(90 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"1m30s"` {
		t.Fatalf("marshal = %s", out)
	}

	var bad Duration
	if err := json.Unmarshal([]byte(`"ninety"`), &bad); err == nil {
		t.Fatal("expected an error for an unparsable duration")
	}
}

func TestMarshalKeepsReferencesAndDurations(t *testing.T) {
	t.Setenv("CFG_PASS", "resolved-secret")
	src := `{
  "defaults": {"timeout": "45s"},
  "hosts": {
    "a": {
      "addr": "10.0.0.5:22",
      "user": "deploy",
      "auth": {"type": "key", "key": "/k", "passphrase": "env:CFG_PASS"},
      "elevate": {"mode": "sudo", "password": "literal-sudo"}
    }
  }
}`
	cfg, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Timeout(cfg.Hosts["a"]); got != 45*time.Second {
		t.Fatalf("timeout = %v", got)
	}
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "resolved-secret") {
		t.Fatalf("the resolved secret was written into the payload:\n%s", data)
	}
	if !strings.Contains(string(data), `"env:CFG_PASS"`) {
		t.Fatalf("the reference was lost:\n%s", data)
	}
	if !strings.Contains(string(data), `"45s"`) {
		t.Fatalf("durations should stay human readable:\n%s", data)
	}

	// What is stored must be exactly what a restart would load.
	again, err := Parse(data, "")
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := again.Hosts["a"].Auth.Passphrase; got != "resolved-secret" {
		t.Fatalf("passphrase after reload = %q", got)
	}
}

func TestRedactedMasksLiteralSecrets(t *testing.T) {
	t.Setenv("CFG_SUDO", "sudo-secret")
	src := `{
  "hosts": {
    "a": {
      "addr": "10.0.0.5:22",
      "user": "deploy",
      "auth": {"type": "password", "password": "hunter2"},
      "elevate": {"mode": "sudo", "password": "env:CFG_SUDO"}
    }
  }
}`
	cfg, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cfg.Redacted().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, "hunter2") {
		t.Fatalf("a literal secret leaked:\n%s", text)
	}
	if !strings.Contains(text, `"***"`) {
		t.Fatalf("the literal secret was not masked:\n%s", text)
	}
	if !strings.Contains(text, "env:CFG_SUDO") {
		t.Fatalf("a reference should stay visible:\n%s", text)
	}
}

func TestEmptyPayloadIsValidAndEmpty(t *testing.T) {
	plain, err := EmptyPayload()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(plain, "")
	if err != nil {
		t.Fatalf("the built-in empty payload must parse: %v", err)
	}
	if len(cfg.Hosts) != 0 {
		t.Fatalf("a new vault must start without hosts: %v", cfg.Hosts)
	}
	if cfg.Defaults.Approval != ApprovalAuto {
		t.Fatalf("approval default = %q", cfg.Defaults.Approval)
	}
	if got := cfg.Defaults.Timeout.Or(DefaultTimeout); got != DefaultTimeout {
		t.Fatalf("timeout default = %v", got)
	}
}

func TestGroupsFollowTheCatalogue(t *testing.T) {
	cfg, err := Parse([]byte(`{
  "defaults": {"groups": ["生产", "空分组"]},
  "hosts": {
    "a": {"addr": "a:22", "user": "u", "auth": {"type": "key", "key": "/k"}, "group": "测试"},
    "b": {"addr": "b:22", "user": "u", "auth": {"type": "key", "key": "/k"}, "group": "生产"}
  }
}`), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"生产", "空分组", "测试"}
	if !slices.Equal(cfg.Defaults.Groups, want) {
		t.Fatalf("groups = %v, want %v", cfg.Defaults.Groups, want)
	}
	if cfg.Hosts["a"].Group != "测试" || cfg.Hosts["b"].Group != "生产" {
		t.Fatalf("host groups = %q %q", cfg.Hosts["a"].Group, cfg.Hosts["b"].Group)
	}

	if !cfg.AddGroup("客户") || cfg.AddGroup("客户") {
		t.Fatal("AddGroup must report only a new group")
	}
	if cfg.GroupExists("生产") != true || cfg.GroupExists("不存在") != false {
		t.Fatal("GroupExists is wrong")
	}

	if err := cfg.RenameGroup("测试", "QA"); err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts["a"].Group != "QA" {
		t.Fatalf("rename did not move the host: %q", cfg.Hosts["a"].Group)
	}
	found := false
	for _, g := range cfg.Defaults.Groups {
		if g == "QA" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rename did not update the order: %v", cfg.Defaults.Groups)
	}
	if err := cfg.RenameGroup("QA", "生产"); err == nil {
		t.Fatal("renaming onto an existing group must be refused")
	}
	// Changing only the case of one group's own name is allowed; joining a
	// different group that folds to the same name is not.
	if err := cfg.RenameGroup("QA", "qa"); err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts["a"].Group != "qa" {
		t.Fatalf("case rename did not move the host: %q", cfg.Hosts["a"].Group)
	}

	cfg.DeleteGroup("生产")
	if cfg.Hosts["b"].Group != "" {
		t.Fatalf("DeleteGroup must ungroup its hosts: %q", cfg.Hosts["b"].Group)
	}
	for _, g := range cfg.Defaults.Groups {
		if g == "生产" {
			t.Fatalf("group still listed: %v", cfg.Defaults.Groups)
		}
	}
	if _, ok := cfg.Hosts["b"]; !ok {
		t.Fatal("DeleteGroup must not delete servers")
	}
}
