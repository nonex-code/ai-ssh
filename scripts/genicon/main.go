//go:build ignore

// genicon turns the app's SVG mark into the icon files each platform uses:
//
//   - internal/gui/assets/logo.ico   Windows executable resource (PE);
//   - internal/gui/assets/logo.icns  macOS app bundle icon;
//   - internal/gui/assets/logo-512.png  Linux desktop-entry icon.
//
// The Makefile's icon target then compiles the .ico into
// rsrc_windows_amd64.syso with windres.
//
//	go run scripts/genicon/main.go
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

var windowsSizes = []int{16, 24, 32, 48, 64, 128, 256}

// icnsTypes are the ICNS element types macOS expects, by pixel size.
var icnsTypes = []struct {
	size int
	kind string
}{
	{16, "icp4"},
	{32, "icp5"},
	{64, "icp6"},
	{128, "ic07"},
	{256, "ic08"},
	{512, "ic09"},
	{1024, "ic10"},
}

func main() {
	svgPath := filepath.Join("internal", "gui", "assets", "logo.svg")
	raw, err := os.ReadFile(svgPath)
	if err != nil {
		fatal(err)
	}
	mark, err := ui.ParseSVG(raw)
	if err != nil {
		fatal(fmt.Errorf("parse %s: %w", svgPath, err))
	}

	pngAt := func(size int) []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, mark.Image(size, size, nil)); err != nil {
			fatal(err)
		}
		return buf.Bytes()
	}

	// --- Windows .ico -------------------------------------------------------
	var ico bytes.Buffer
	header := make([]byte, 6)
	binary.LittleEndian.PutUint16(header[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(header[4:], uint16(len(windowsSizes)))
	ico.Write(header)

	offset := 6 + 16*len(windowsSizes)
	frames := make([][]byte, 0, len(windowsSizes))
	for _, size := range windowsSizes {
		data := pngAt(size)
		frames = append(frames, data)
		entry := make([]byte, 16)
		if size >= 256 {
			entry[0], entry[1] = 0, 0 // 0 means 256
		} else {
			entry[0], entry[1] = byte(size), byte(size)
		}
		binary.LittleEndian.PutUint16(entry[4:], 1)  // planes
		binary.LittleEndian.PutUint16(entry[6:], 32) // bits per pixel
		binary.LittleEndian.PutUint32(entry[8:], uint32(len(data)))
		binary.LittleEndian.PutUint32(entry[12:], uint32(offset))
		ico.Write(entry)
		offset += len(data)
	}
	for _, data := range frames {
		ico.Write(data)
	}
	writeAsset("logo.ico", ico.Bytes())

	// --- macOS .icns --------------------------------------------------------
	var body bytes.Buffer
	for _, t := range icnsTypes {
		data := pngAt(t.size)
		body.WriteString(t.kind)
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(8+len(data)))
		body.Write(length[:])
		body.Write(data)
	}
	var icns bytes.Buffer
	icns.WriteString("icns")
	var total [4]byte
	binary.BigEndian.PutUint32(total[:], uint32(8+body.Len()))
	icns.Write(total[:])
	icns.Write(body.Bytes())
	writeAsset("logo.icns", icns.Bytes())

	// --- Linux desktop icon -------------------------------------------------
	writeAsset("logo-512.png", pngAt(512))
}

func writeAsset(name string, data []byte) {
	dst := filepath.Join("internal", "gui", "assets", name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", dst, len(data))
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "genicon: %v\n", err)
	os.Exit(1)
}
