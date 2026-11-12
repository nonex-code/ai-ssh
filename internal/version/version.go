// Package version carries the build version so the CLI, the MCP server and the
// window all report the same string. Release builds override it:
//
//	go build -ldflags "-X github.com/nonex-code/ai-ssh/internal/version.Value=0.1.0"
package version

// Value is the human readable version. Keep the "+dev" suffix for local builds
// so a binary that was not produced by the release pipeline is recognisable.
var Value = "0.1.0+dev"
