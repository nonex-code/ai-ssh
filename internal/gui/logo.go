package gui

import (
	"bytes"
	_ "embed"
	"image/png"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The mark is a shield whose face holds an AI star and a terminal prompt:
// access is bounded by the agent, the AI reasons about the task, and what it
// gets is a shell the agent allows. It travels as an SVG so the window, the
// dialogs and the icon all draw the same shape. The executable's own icon is
// built from the same SVG (internal/gui/assets/logo.ico and
// rsrc_windows_amd64.syso); run "make icon" after changing it.
//
//go:embed assets/logo.svg
var logoSVG []byte

var logo = ui.MustParseSVG(logoSVG)

// logoMark draws the tile at a size. It is drawn with Image, not Icon: the
// mark has two colors (ink plate, paper lock) and Icon would flatten it.
func logoMark(c *ui.Context, size float32) ui.Element {
	return ui.Image(c, logo).Size(size, size)
}

var (
	logoPNGOnce sync.Once
	logoPNGData []byte
	dockPNGOnce sync.Once
	dockPNGData []byte
)

// renderLogoPNG encodes the mark at size.
func renderLogoPNG(size int) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, logo.Image(size, size, nil)); err != nil {
		return nil
	}
	return buf.Bytes()
}

// logoPNG renders the mark as the PNG mygo wants for a window icon: the title
// bar and taskbar on Windows and Linux, and the dialogs' icon too. 256 px
// covers the 2× icons of a high-density display. The executable's file icon is
// a platform resource (logo.ico on Windows, logo.icns in a macOS bundle), not
// this PNG.
func logoPNG() []byte {
	logoPNGOnce.Do(func() { logoPNGData = renderLogoPNG(256) })
	return logoPNGData
}

// setDockIcon gives a macOS app bundle-less binary its Dock icon: a raw
// executable cannot carry a Finder icon, but the running app can. Bundles get
// the same artwork from Contents/Resources/logo.icns.
func setDockIcon() {
	if runtime.GOOS != "darwin" || mygo.App.Dock == nil {
		return
	}
	dockPNGOnce.Do(func() { dockPNGData = renderLogoPNG(512) })
	if len(dockPNGData) > 0 {
		_ = mygo.App.Dock.SetIcon(dockPNGData)
	}
}

// setWindowIcon gives a window the app's mark. A failure is cosmetic: the
// window still works with the platform's default icon.
func setWindowIcon(w *mygo.Window) {
	if w == nil {
		return
	}
	if png := logoPNG(); png != nil {
		_ = w.SetIcon(png)
	}
}
