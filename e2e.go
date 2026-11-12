//go:build e2e

// This file links mygo's automation plugin, which lets an end-to-end runner (or
// an agent) drive the window over a local socket. It is only compiled with
// `go build -tags e2e` and only listens when MYGO_AUTOMATION is set.
package main

import _ "github.com/egoist/mygo/plugins/automation"
