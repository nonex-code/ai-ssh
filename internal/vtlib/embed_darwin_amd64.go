//go:build ai_ssh_embedvt && darwin && amd64

package vtlib

import _ "embed"

//go:embed libghostty-vt-darwin-amd64.dylib
var embedded []byte

const embeddedName = "libghostty-vt.dylib"
