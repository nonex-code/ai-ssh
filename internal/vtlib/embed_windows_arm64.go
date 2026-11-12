//go:build ai_ssh_embedvt && windows && arm64

package vtlib

import _ "embed"

//go:embed ghostty-vt-windows-arm64.dll
var embedded []byte

const embeddedName = "ghostty-vt.dll"
