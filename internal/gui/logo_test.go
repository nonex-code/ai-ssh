package gui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestLogoRenders keeps the mark parseable and proves the tile keeps its two
// colors: a plate with a transparent outside, and a paper shield on it. Set
// AISSH_GUI_SHOTS to keep the sizes for review.
func TestLogoRenders(t *testing.T) {
	for _, size := range []int{256, 64, 32, 16} {
		img := logo.Image(size, size, nil)
		if img == nil || img.Bounds().Dx() != size {
			t.Fatalf("%d: bad image", size)
		}
		// The tile's corner is transparent; its middle is the paper shield.
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatalf("%d: the plate's corner should be transparent", size)
		}
		if r, g, b, a := img.At(size*3/5, size*3/5).RGBA(); a == 0 || r < 0x8000 || g < 0x8000 || b < 0x8000 {
			t.Fatalf("%d: the shield should be paper, got %v", size, img.At(size*3/5, size*3/5))
		}
		outDir := os.Getenv("AISSH_GUI_SHOTS")
		if outDir == "" {
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(outDir, "logo-"+strconv.Itoa(size)+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}
	if data := logoPNG(); len(data) < 100 {
		t.Fatalf("logoPNG is too small (%d bytes)", len(data))
	} else if img, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("logoPNG: %v", err)
	} else if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("logoPNG is %v, want 256x256", b)
	}
}

// TestPlatformIconAssets keeps the generated icons present and valid: the
// Windows .ico (compiled into the executable), the macOS .icns (used by the
// .app bundle) and the Linux PNG desktop icon.
func TestPlatformIconAssets(t *testing.T) {
	ico, err := os.ReadFile("assets/logo.ico")
	if err != nil {
		t.Fatal(err)
	}
	if len(ico) < 6 || ico[2] != 1 || ico[3] != 0 {
		t.Fatalf("assets/logo.ico is not an ICO: % x", ico[:min(len(ico), 6)])
	}
	icns, err := os.ReadFile("assets/logo.icns")
	if err != nil {
		t.Fatal(err)
	}
	if len(icns) < 8 || string(icns[:4]) != "icns" {
		t.Fatalf("assets/logo.icns is not an ICNS: % x", icns[:min(len(icns), 4)])
	}
	pngData, err := os.ReadFile("assets/logo-512.png")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("assets/logo-512.png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 512 || b.Dy() != 512 {
		t.Fatalf("assets/logo-512.png is %v, want 512x512", b)
	}
}
