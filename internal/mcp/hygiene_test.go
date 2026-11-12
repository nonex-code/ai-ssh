package mcp_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCPPathHasNoCredentialDependencies enforces the core security property of
// the MCP front end: the process an AI host spawns must not be able to open the
// vault, read the host catalogue or speak SSH itself. If this test fails, the
// "thin proxy" guarantee is gone.
func TestMCPPathHasNoCredentialDependencies(t *testing.T) {
	forbidden := []string{
		"github.com/nonex-code/ai-ssh/internal/vault",
		"github.com/nonex-code/ai-ssh/internal/config",
		"github.com/nonex-code/ai-ssh/internal/daemon",
		"github.com/nonex-code/ai-ssh/internal/agent",
		"golang.org/x/crypto/ssh",
		"github.com/pkg/sftp",
	}

	// Every package reachable from the MCP server.
	dirs := []string{".", "../client", "../proto", "../ipc"}
	fset := token.NewFileSet()

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range file.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if p == bad {
						t.Errorf("%s imports %s: the MCP path must stay credential free", path, p)
					}
				}
			}
		}
	}
}
