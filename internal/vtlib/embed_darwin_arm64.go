//go:build ai_ssh_embedvt && darwin && arm64

package vtlib

import _ "embed"

//go:embed libghostty-vt-darwin-arm64.dylib
var embedded []byte

const embeddedName = "libghostty-vt.dylib"
