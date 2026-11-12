package gui

// credit is one open-source project the app ships or links against.
type credit struct {
	name    string
	url     string
	license string
	use     string
}

// credits is shown on the "关于" page. Keep it in step with go.mod: the direct
// dependencies, the runtime pieces they bring, and the bundled fonts.
var credits = []credit{
	{"Go", "https://go.dev", "BSD-3-Clause", "language and runtime"},
	{"mygo", "https://github.com/egoist/mygo", "MIT", "window, UI toolkit and terminal widget"},
	{"Ghostty / libghostty-vt", "https://github.com/ghostty-org/ghostty", "MIT", "terminal parser and renderer"},
	{"golang.org/x/crypto", "https://pkg.go.dev/golang.org/x/crypto", "BSD-3-Clause", "SSH client and host keys"},
	{"golang.org/x/term", "https://pkg.go.dev/golang.org/x/term", "BSD-3-Clause", "password prompts without echo"},
	{"golang.org/x/sys, net, text, image", "https://pkg.go.dev/golang.org/x", "BSD-3-Clause", "platform, network, text and image support"},
	{"github.com/pkg/sftp", "https://github.com/pkg/sftp", "BSD-2-Clause", "SFTP file transfers"},
	{"github.com/Microsoft/go-winio", "https://github.com/microsoft/go-winio", "MIT", "Windows named pipes for IPC"},
	{"github.com/ebitengine/purego", "https://github.com/ebitengine/purego", "Apache-2.0", "loading libghostty-vt without cgo"},
	{"github.com/go-text/typesetting", "https://github.com/go-text/typesetting", "BSD-3-Clause / Unlicense", "text shaping for the terminal font"},
	{"github.com/kr/fs", "https://github.com/kr/fs", "BSD-3-Clause", "walking local files in the file pane"},
	{"Inter", "https://rsms.me/inter/", "SIL OFL 1.1", "interface font"},
	{"JetBrains Mono", "https://www.jetbrains.com/lp/mono/", "SIL OFL 1.1", "terminal font"},
}
