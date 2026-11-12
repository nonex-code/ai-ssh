//go:build ai_ssh_embedvt && windows && amd64

package vtlib

import _ "embed"

//go:embed ghostty-vt-windows-amd64.dll
var embedded []byte

const embeddedName = "ghostty-vt.dll"
