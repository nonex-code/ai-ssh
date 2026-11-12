package gui

// The type scale and the spacing steps, in one place so the window keeps a
// consistent hierarchy: page titles, card titles, body, small print.
const (
	fontPage  float32 = 19
	fontCard  float32 = 14.5
	fontBody  float32 = 13
	fontSmall float32 = 11.5
	fontMicro float32 = 10.5
)

// uiFont is the interface face: the bundled Inter for Latin, with the system's
// Chinese UI font behind it (Inter has no CJK glyphs).
const uiFont = "Inter, Microsoft YaHei UI, PingFang SC, Noto Sans CJK SC, Hiragino Sans GB, sans-serif"

// monoFont is used for addresses, logs and the terminal. JetBrains Mono is
// bundled and registered at startup.
const monoFont = "JetBrains Mono, Cascadia Mono, Consolas, monospace"

// mono is what the views pass to Element.Font.
const mono = monoFont

// iconSize is the box an icon is drawn in, in buttons and the toolbar.
const iconSize float32 = 16

// inputH is the height of the home page's quick connect field and its +
// button, tall enough to be the page's one big control.
const inputH float32 = 44

// titleBarH is the height of the tab strip drawn in the window's title bar,
// where the native window buttons live (Windows draws them 40 DIPs tall).
const titleBarH float32 = controlH + 12

// controlH is the height of a control in a dense bar (tab strip, bookmarks,
// toolbar, file pane). Dialogs keep the platform's default, which is taller.
const controlH float32 = 28

const (
	gapTight = 4
	gapSmall = 8
	gapMid   = 12
	gapWide  = 20
)

// Corners: cards and tabs are rounder than inputs and buttons, sheets rounder
// still, which is what makes the layout read as layers rather than boxes.
const (
	radiusTab    float32 = 8
	radiusCard   float32 = 12
	radiusSheet  float32 = 16
	paddingCard  float32 = 16
	paddingSheet float32 = 28
)
