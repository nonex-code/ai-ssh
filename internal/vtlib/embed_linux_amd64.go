//go:build ai_ssh_embedvt && linux && amd64

package vtlib

import _ "embed"

//go:embed libghostty-vt-linux-amd64.so
var embedded []byte

const embeddedName = "libghostty-vt.so"
