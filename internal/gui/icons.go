package gui

import (
	"github.com/egoist/mygo/ui"
)

// The icon set: hairline strokes on a 24×24 grid, in the text color, so one
// set works in both themes. Each is a couple of paths at most — no filled
// plates, no detail that disappears at 16 px.
var iconSet = map[string]*ui.SVG{
	// The home page: a house.
	"home": svg(`<path d="M4 10.6 12 4l8 6.6"/><path d="M6.5 9.6V19a1.5 1.5 0 0 0 1.5 1.5h8a1.5 1.5 0 0 0 1.5-1.5V9.6"/><path d="M10 20.5v-5.2h4v5.2"/>`),

	// Servers: a rack beside their list.
	"server": svg(`<rect x="3" y="4.5" width="9" height="15" rx="2"/><path d="M6 8h3M6 12h3M6 16h3"/><path d="M16 8h5M16 12h5M16 16h5"/>`),

	// A new tab: a page with a plus.
	"newtab": svg(`<rect x="4" y="4" width="16" height="16" rx="3.5"/><path d="M12 8.6v6.8M8.6 12h6.8"/>`),

	// Another session of the same tab: two windows, one behind the other.
	"duplicate": svg(`<rect x="3.5" y="7" width="12" height="11" rx="2"/><path d="M8 3.5h10A2.5 2.5 0 0 1 20.5 6v10"/>`),

	// AI activity: a robot head.
	"log": svg(`<rect x="4" y="7" width="16" height="11" rx="3"/><path d="M12 3.5V7"/>` +
		`<circle cx="9.3" cy="12.4" r="1.1" fill="currentColor" stroke="none"/>` +
		`<circle cx="14.7" cy="12.4" r="1.1" fill="currentColor" stroke="none"/>` +
		`<path d="M9.8 15.6h4.4"/>`),

	// The MCP config: a clipboard holding code, as in "copy this snippet".
	"mcp": svg(`<rect x="4.5" y="4.5" width="15" height="17" rx="2.5"/><path d="M9 4.5h6v2.6H9z"/>` +
		`<path d="M9.8 12 8.2 14l1.6 2M14.2 12l1.6 2-1.6 2"/>`),
	"check": svg(`<path d="M4 12.5l5 5L20 6.5"/>`),

	// Appearance: a half-filled circle, as a contrast setting.
	"appearance": svg(`<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none"/>`),
	// Quick theme: the sun and the moon it switches to.
	"sun":  svg(`<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4"/>`),
	"moon": svg(`<path d="M20 14.5A8.5 8.5 0 0 1 9.5 4 8.5 8.5 0 1 0 20 14.5Z"/>`),
	// Settings: a cog.
	"gear": svg(`<circle cx="12" cy="12" r="3.2"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.6-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.6V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.6 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.6 1Z"/>`),

	"lock": svg(`<rect x="4" y="10" width="16" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>`),

	"terminal": svg(`<path d="M4 17l6-5-6-5"/><path d="M12 19h8"/>`),
	"folder":   svg(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"/>`),
	"forward":  svg(`<path d="M8 3L4 7l4 4"/><path d="M4 7h16"/><path d="M16 21l4-4-4-4"/><path d="M20 17H4"/>`),

	// The machine's vitals: a speedometer.
	"gauge": svg(`<path d="M4 17.5a8 8 0 0 1 16 0"/><path d="M12 17.5l4.2-5.6"/>` +
		`<circle cx="12" cy="17.5" r="1.2" fill="currentColor" stroke="none"/>`),

	"pencil": svg(`<path d="M4 20h4l10-10a2.8 2.8 0 0 0-4-4L4 16v4Z"/><path d="M13.5 6.5l4 4"/>`),

	"chevron-left":  svg(`<path d="M15 5l-7 7 7 7"/>`),
	"chevron-right": svg(`<path d="M9 5l7 7-7 7"/>`),
	"chevron-down":  svg(`<path d="M5 9l7 7 7-7"/>`),
	"arrow-up":      svg(`<path d="M12 19V5"/><path d="M6 11l6-6 6 6"/>`),
	"refresh":       svg(`<path d="M20 12a8 8 0 1 1-2.3-5.7"/><path d="M20 4v5h-5"/>`),

	"arrow-right": svg(`<path d="M4 12h16"/><path d="M14 6l6 6-6 6"/>`),
	"plus":        svg(`<path d="M12 5v14M5 12h14"/>`),
	"x":           svg(`<path d="M6 6l12 12M18 6L6 18"/>`),
}

// svg gives a path set the shared stroke attributes. Every icon uses the same
// weight, so the strip and the toolbar look like one set.
func svg(paths string) *ui.SVG {
	data := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
		`stroke="currentColor" stroke-width="2" stroke-linecap="round" ` +
		`stroke-linejoin="round">` + paths + `</svg>`
	return ui.MustParseSVG([]byte(data))
}

func icon(name string) *ui.SVG {
	if s, ok := iconSet[name]; ok {
		return s
	}
	return iconSet["server"]
}
