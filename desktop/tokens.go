// Design tokens for the desktop window.
//
// The base palette and the type scale still follow design/tokens.json (the web
// client reads the same file), but the desktop now carries five switchable
// theme packs and three type scales: the values below are the "midnight" pack at
// the standard scale, and applyPalette/applyTextScale replace them at runtime.
// Keep design/tokens.json and this file in step when the base pack changes.
package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Colours shared with the web client. ui.Hex is a call, so these are vars.
var (
	colorBg         = ui.Hex("#0b0d11")
	colorBgSoft     = ui.Hex("#0f1218")
	colorPanel      = ui.Hex("#14181f")
	colorPanel2     = ui.Hex("#1a1f28")
	colorLine       = ui.Hex("#262d39")
	colorLineSoft   = ui.Hex("#1e242e")
	colorText       = ui.Hex("#e9ecf2")
	colorTextDim    = ui.Hex("#9aa4b3")
	colorDim        = ui.Hex("#808a99")
	colorAccent     = ui.Hex("#5aa9ff")
	colorAccentText = ui.Hex("#06121f")
	colorSound      = ui.Hex("#ff79b0")
	colorGood       = ui.Hex("#5fd39a")
	colorWarn       = ui.Hex("#f5c451")
	colorBad        = ui.Hex("#ff7d7d")
	colorAccentSoft = ui.RGBA(90, 169, 255, 0.16)
	colorSoundSoft  = ui.RGBA(255, 121, 176, 0.16)
)

// Radii, spacing and text sizes shared with the web client.
const (
	radiusSm = 9
	radiusMd = 14
	radiusLg = 18
	spaceXs  = 4
	spaceSm  = 8
	spaceMd  = 12
	spaceLg  = 16
	spaceXl  = 24
	spaceXxl = 32
)

// Text sizes are vars because the Appearance page scales them. Six roles, three
// scales; nothing else in the window hard-codes a font size.
var (
	textMicro    float32 = 11
	textCaption  float32 = 12
	textSmall    float32 = 12
	textBody     float32 = 13
	textLarge    float32 = 14
	textSubtitle float32 = 15
	textTitle    float32 = 18
	textDisplay  float32 = 22
)

// The three type scales. Micro is the smallest label still meant to be read,
// display is a page title. Buttons and inputs get their height from scale.
type typeScale struct {
	name                                           string
	micro, caption, body, subtitle, title, display float32
}

var typeScales = []typeScale{
	{name: "compact", micro: 11, caption: 12, body: 13, subtitle: 14, title: 16, display: 20},
	{name: "standard", micro: 12, caption: 13, body: 14, subtitle: 15, title: 18, display: 22},
	{name: "large", micro: 13, caption: 14, body: 16, subtitle: 17, title: 20, display: 26},
}

// palette is one theme pack: the colours the window is drawn from, plus the text
// that is drawn *on* the accent (a light accent needs dark ink) and the tone the
// tooltip uses (Inverse/InverseText), which the previous build left to chance.
type palette struct {
	name, label, labelZH  string
	dark                  bool
	bg, bgSoft            ui.Color
	panel, panel2         ui.Color
	line, lineSoft        ui.Color
	text, textDim, dim    ui.Color
	accent, accentText    ui.Color
	sound                 ui.Color
	good, warn, bad       ui.Color
	inverse, inverseText  ui.Color
	accentSoft, soundSoft ui.Color
}

var themePacks = []palette{
	{
		name: "midnight", label: "Midnight", labelZH: "午夜", dark: true,
		bg: ui.Hex("#0b0d11"), bgSoft: ui.Hex("#0f1218"),
		panel: ui.Hex("#14181f"), panel2: ui.Hex("#1a1f28"),
		line: ui.Hex("#262d39"), lineSoft: ui.Hex("#1e242e"),
		text: ui.Hex("#e9ecf2"), textDim: ui.Hex("#9aa4b3"), dim: ui.Hex("#808a99"),
		accent: ui.Hex("#5aa9ff"), accentText: ui.Hex("#06121f"),
		sound: ui.Hex("#ff79b0"),
		good:  ui.Hex("#5fd39a"), warn: ui.Hex("#f5c451"), bad: ui.Hex("#ff7d7d"),
		inverse: ui.Hex("#1a1f28"), inverseText: ui.Hex("#e9ecf2"),
		accentSoft: ui.RGBA(90, 169, 255, 0.16), soundSoft: ui.RGBA(255, 121, 176, 0.16),
	},
	{
		name: "graphite", label: "Graphite", labelZH: "石墨", dark: true,
		bg: ui.Hex("#121315"), bgSoft: ui.Hex("#16171a"),
		panel: ui.Hex("#1b1c20"), panel2: ui.Hex("#232428"),
		line: ui.Hex("#303237"), lineSoft: ui.Hex("#26282c"),
		text: ui.Hex("#ededf0"), textDim: ui.Hex("#a9aeb7"), dim: ui.Hex("#8b9099"),
		accent: ui.Hex("#8cb4ff"), accentText: ui.Hex("#0a1420"),
		sound: ui.Hex("#d99ac0"),
		good:  ui.Hex("#86cfa4"), warn: ui.Hex("#e3c069"), bad: ui.Hex("#f08a8a"),
		inverse: ui.Hex("#232428"), inverseText: ui.Hex("#ededf0"),
		accentSoft: ui.RGBA(140, 180, 255, 0.18), soundSoft: ui.RGBA(217, 154, 192, 0.18),
	},
	{
		name: "daylight", label: "Daylight", labelZH: "日光", dark: false,
		bg: ui.Hex("#f5f6f8"), bgSoft: ui.Hex("#eceef1"),
		panel: ui.Hex("#ffffff"), panel2: ui.Hex("#f4f5f7"),
		line: ui.Hex("#d6d9de"), lineSoft: ui.Hex("#e6e8ec"),
		text: ui.Hex("#14161a"), textDim: ui.Hex("#4f5661"), dim: ui.Hex("#6b7280"),
		accent: ui.Hex("#1f6feb"), accentText: ui.Hex("#ffffff"),
		sound: ui.Hex("#b4327a"),
		good:  ui.Hex("#16794a"), warn: ui.Hex("#8a5a00"), bad: ui.Hex("#b42318"),
		inverse: ui.Hex("#14161a"), inverseText: ui.Hex("#f5f6f8"),
		accentSoft: ui.RGBA(31, 111, 235, 0.12), soundSoft: ui.RGBA(180, 50, 122, 0.12),
	},
	{
		name: "celadon", label: "Celadon", labelZH: "青瓷", dark: false,
		bg: ui.Hex("#f1f5f3"), bgSoft: ui.Hex("#e7eeea"),
		panel: ui.Hex("#ffffff"), panel2: ui.Hex("#eef4f1"),
		line: ui.Hex("#cdddd4"), lineSoft: ui.Hex("#dde9e3"),
		text: ui.Hex("#10201a"), textDim: ui.Hex("#4a5f56"), dim: ui.Hex("#647a70"),
		accent: ui.Hex("#0e7c66"), accentText: ui.Hex("#ffffff"),
		sound: ui.Hex("#a63d72"),
		good:  ui.Hex("#1a7f52"), warn: ui.Hex("#7d5a00"), bad: ui.Hex("#b02a2a"),
		inverse: ui.Hex("#10201a"), inverseText: ui.Hex("#f1f5f3"),
		accentSoft: ui.RGBA(14, 124, 102, 0.14), soundSoft: ui.RGBA(166, 61, 114, 0.14),
	},
	{
		name: "noir", label: "Noir", labelZH: "纯黑", dark: true,
		bg: ui.Hex("#000000"), bgSoft: ui.Hex("#07070a"),
		panel: ui.Hex("#0d0d12"), panel2: ui.Hex("#16161d"),
		line: ui.Hex("#2b2b36"), lineSoft: ui.Hex("#1d1d25"),
		text: ui.Hex("#ffffff"), textDim: ui.Hex("#c2c7d0"), dim: ui.Hex("#9aa0ab"),
		accent: ui.Hex("#4da3ff"), accentText: ui.Hex("#00101f"),
		sound: ui.Hex("#ff5fa8"),
		good:  ui.Hex("#3ee08f"), warn: ui.Hex("#ffc44d"), bad: ui.Hex("#ff6060"),
		inverse: ui.Hex("#16161d"), inverseText: ui.Hex("#ffffff"),
		accentSoft: ui.RGBA(77, 163, 255, 0.18), soundSoft: ui.RGBA(255, 95, 168, 0.18),
	},
}

var (
	currentTheme   = themePacks[0]
	currentScale   = typeScales[1]
	currentFont    = ""
	currentDensity = false
)

// themePack finds a pack by its stored name; an unknown name is midnight, which
// is also the first-run theme.
func themePack(name string) palette {
	for _, p := range themePacks {
		if p.name == name {
			return p
		}
	}
	return themePacks[0]
}

// scalePack finds a type scale by name; an unknown name is the standard one.
func scalePack(name string) typeScale {
	for _, s := range typeScales {
		if s.name == name {
			return s
		}
	}
	return typeScales[1]
}

// applyPalette replaces the package's colour vars. Every drawing call reads those
// vars, so a switch needs no widget-by-widget plumbing: the next frame is drawn
// in the new pack.
func applyPalette(p palette) {
	currentTheme = p
	colorBg = p.bg
	colorBgSoft = p.bgSoft
	colorPanel = p.panel
	colorPanel2 = p.panel2
	colorLine = p.line
	colorLineSoft = p.lineSoft
	colorText = p.text
	colorTextDim = p.textDim
	colorDim = p.dim
	colorAccent = p.accent
	colorAccentText = p.accentText
	colorSound = p.sound
	colorGood = p.good
	colorWarn = p.warn
	colorBad = p.bad
	colorAccentSoft = p.accentSoft
	colorSoundSoft = p.soundSoft
}

// applyTextScale sets the six text roles from one scale.
func applyTextScale(s typeScale) {
	currentScale = s
	textMicro = s.micro
	textCaption = s.caption
	textBody = s.body
	textSmall = s.caption
	textLarge = s.subtitle
	textSubtitle = s.subtitle
	textTitle = s.title
	textDisplay = s.display
}

// uiFontSize is the size the theme gives text that does not set its own: the
// body role, so a widget's label grows with the scale like everything else.
func uiFontSize() float32 { return currentScale.body }

// muxshiftTheme is the desktop window's palette, built from the same tokens as
// the web page so the two clients read as one product.
func muxshiftTheme() *ui.Theme {
	p := currentTheme
	theme := ui.DarkTheme()
	theme.Dark = p.dark
	theme.Background = colorBg
	theme.Surface = colorPanel
	theme.SurfaceHover = colorPanel2
	theme.SurfacePressed = colorPanel2
	theme.Border = colorLine
	theme.Text = colorText
	theme.TextMuted = colorTextDim
	theme.Accent = colorAccent
	theme.AccentHover = colorAccent
	theme.AccentPressed = colorAccent
	theme.AccentText = colorAccentText
	theme.Selection = colorAccentSoft
	theme.Success = colorGood
	theme.Warning = colorWarn
	theme.Danger = colorBad
	// Tooltips were the one widget drawn in the wrong direction: Inverse was
	// left to the default, which is Text-on-Background, so a dark theme got a
	// near-white card. The pack names the tooltip's fill and ink explicitly.
	theme.Inverse = p.inverse
	theme.InverseText = p.inverseText
	theme.Focus = colorAccent
	theme.FocusRingWidth = 2
	theme.FocusRingOffset = 1
	theme.Radius = 8
	theme.Spacing = 4
	theme.Font = currentFont
	theme.FontSize = uiFontSize()
	theme.TooltipDelay = 400 * time.Millisecond
	return theme
}
