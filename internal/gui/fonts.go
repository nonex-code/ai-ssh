package gui

import (
	_ "embed"
	"log"
	"sync"

	"github.com/egoist/mygo/ui"
)

// The app carries its own fonts so the window looks the same on every machine:
// Inter for the interface (Chinese falls back to the system's UI font) and
// JetBrains Mono for addresses, logs and the terminal. Both are under the SIL
// Open Font License; see assets/fonts/OFL-*.txt.
//
//go:embed assets/fonts/Inter-Regular.ttf
var interRegular []byte

//go:embed assets/fonts/Inter-SemiBold.ttf
var interSemiBold []byte

//go:embed assets/fonts/Inter-Bold.ttf
var interBold []byte

//go:embed assets/fonts/JetBrainsMono-Regular.ttf
var jbmRegular []byte

//go:embed assets/fonts/JetBrainsMono-Bold.ttf
var jbmBold []byte

var fontsOnce sync.Once

// ensureFonts registers the bundled faces once, under their own family names.
// Registering several weights under one family lets bold text use the real
// SemiBold/Bold face instead of a synthetic one.
func ensureFonts() {
	fontsOnce.Do(func() {
		for _, f := range []struct {
			family string
			data   []byte
		}{
			{"Inter", interRegular},
			{"Inter", interSemiBold},
			{"Inter", interBold},
			{"JetBrains Mono", jbmRegular},
			{"JetBrains Mono", jbmBold},
		} {
			if err := ui.RegisterFont(f.data, f.family); err != nil {
				log.Printf("ai-ssh: register font %q: %v", f.family, err)
			}
		}
	})
}
