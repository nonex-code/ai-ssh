//go:build !ai_ssh_embedvt || !((windows || linux || darwin) && (amd64 || arm64))

package vtlib

// Development builds (and platforms whose library is not embedded) leave the
// lookup to mygo: next to the executable, in its cache, or downloaded.
var embedded []byte

const embeddedName = ""
