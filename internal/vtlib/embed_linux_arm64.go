//go:build ai_ssh_embedvt && linux && arm64

package vtlib

import _ "embed"

//go:embed libghostty-vt-linux-arm64.so
var embedded []byte

const embeddedName = "libghostty-vt.so"
