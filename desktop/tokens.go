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
// The ladder is deliberate: base, surface, raised, hover. Adjacent regions are
// separated by that ladder and an 8 DIP gap, not by a hairline on everything.
var (
	colorBg         = ui.Hex("#13151a")
	colorBgSoft     = ui.Hex("#1b1e24")
	colorPanel      = ui.Hex("#1b1e24")
	colorPanel2     = ui.Hex("#252932")
	colorLine       = ui.Hex("#343a45")
	colorLineSoft   = ui.Hex("#292e36")
	colorText       = ui.Hex("#e6eaf0")
	colorTextDim    = ui.Hex("#a1aab8")
	colorDim        = ui.Hex("#858f9e")
	colorAccent     = ui.Hex("#8bb5f5")
	colorAccentText = ui.Hex("#0f1420")
	colorAccentHi   = ui.Hex("#a2c6fa")
	colorAccentDown = ui.Hex("#739dde")
	colorSound      = ui.Hex("#ff79b0")
	colorGood       = ui.Hex("#70c7a1")
	colorWarn       = ui.Hex("#e4b66d")
	colorBad        = ui.Hex("#ed818b")
	colorAccentSoft = ui.RGBA(139, 181, 245, 0.16)
	colorSoundSoft  = ui.RGBA(255, 121, 176, 0.16)
	// The pointer states every control shares. Hover and pressed are part of a
	// theme pack; the zero value keeps a window drawn before one applied usable.
	colorHover        = ui.Hex("#2d333d")
	colorPressed      = ui.Hex("#363e4a")
	colorSelected     = ui.Hex("#273a51")
	colorSelectedText = ui.Hex("#b3d0fa")
	colorEnabled      = ui.Hex("#252f3d")
	colorEnabledHover = ui.Hex("#304157")
	colorEnabledPress = ui.Hex("#27364a")
	colorSliderKnob   = ui.Hex("#dce4f0")
	colorInput        = ui.Hex("#111318")
)

// Radii, spacing and text sizes shared with the web client. Rows are 6, cards
// and dock panels 8, dialogs 12: small enough to read as a tool, not a toy.
const (
	radiusSm = 6
	radiusMd = 8
	radiusLg = 12
	spaceXs  = 4
	spaceSm  = 8
	spaceMd  = 12
	spaceLg  = 16
	spaceXl  = 24
	spaceXxl = 32
)

// Text sizes are vars because the Appearance page scales them. Six roles, three
// scales; nothing else in the window hard-codes a font size. The steps are the
// five the interface actually needs: title, heading, body, control, caption.
var (
	textMicro    float32 = 11
	textCaption  float32 = 11
	textSmall    float32 = 12
	textBody     float32 = 13
	textLarge    float32 = 14
	textSubtitle float32 = 16
	textTitle    float32 = 20
	textDisplay  float32 = 24
)

// The three type scales. Micro is the smallest label still meant to be read,
// display is a page title. Buttons and inputs get their height from scale.
type typeScale struct {
	name                                           string
	micro, caption, body, subtitle, title, display float32
}

var typeScales = []typeScale{
	{name: "compact", micro: 11, caption: 12, body: 13, subtitle: 14, title: 16, display: 20},
	{name: "standard", micro: 11, caption: 12, body: 13, subtitle: 16, title: 20, display: 24},
	{name: "large", micro: 13, caption: 14, body: 16, subtitle: 17, title: 20, display: 26},
}

// palette is one theme pack: the colours the window is drawn from, plus the text
// that is drawn *on* the accent (a light accent needs dark ink) and the tone the
// tooltip uses (Inverse/InverseText), which the previous build left to chance.
type palette struct {
	name, label, labelZH   string
	dark                   bool
	bg, bgSoft             ui.Color
	panel, panel2          ui.Color
	hover, pressed         ui.Color
	selected, selectedText ui.Color
	enabled                ui.Color
	enabledHover           ui.Color
	enabledPressed         ui.Color
	sliderKnob             ui.Color
	input                  ui.Color
	line, lineSoft         ui.Color
	text, textDim, dim     ui.Color
	accent, accentText     ui.Color
	accentHover            ui.Color
	accentPressed          ui.Color
	sound                  ui.Color
	good, warn, bad        ui.Color
	inverse, inverseText   ui.Color
	accentSoft, soundSoft  ui.Color
}

var themePacks = []palette{
	{
		name: "midnight", label: "Midnight", labelZH: "午夜", dark: true,
		bg: ui.Hex("#13151a"), bgSoft: ui.Hex("#1b1e24"),
		panel: ui.Hex("#1b1e24"), panel2: ui.Hex("#252932"),
		hover: ui.Hex("#2d333d"), pressed: ui.Hex("#363e4a"),
		selected: ui.Hex("#273a51"), selectedText: ui.Hex("#b3d0fa"),
		enabled:      ui.Hex("#252f3d"),
		enabledHover: ui.Hex("#304157"), enabledPressed: ui.Hex("#27364a"),
		sliderKnob: ui.Hex("#dce4f0"),
		input:      ui.Hex("#111318"),
		line:       ui.Hex("#343a45"), lineSoft: ui.Hex("#292e36"),
		text: ui.Hex("#e6eaf0"), textDim: ui.Hex("#a1aab8"), dim: ui.Hex("#858f9e"),
		accent: ui.Hex("#8bb5f5"), accentText: ui.Hex("#101d30"),
		accentHover: ui.Hex("#a2c6fa"), accentPressed: ui.Hex("#739dde"),
		sound: ui.Hex("#e89abf"),
		good:  ui.Hex("#70c7a1"), warn: ui.Hex("#e4b66d"), bad: ui.Hex("#ed818b"),
		inverse: ui.Hex("#252932"), inverseText: ui.Hex("#e6eaf0"),
		accentSoft: ui.RGBA(139, 181, 245, 0.16), soundSoft: ui.RGBA(232, 154, 191, 0.16),
	},
	{
		name: "graphite", label: "Graphite", labelZH: "石墨", dark: true,
		bg: ui.Hex("#121315"), bgSoft: ui.Hex("#16171a"),
		panel: ui.Hex("#1b1c20"), panel2: ui.Hex("#232428"),
		hover: ui.Hex("#232428"), pressed: ui.Hex("#2c2f35"),
		selected: ui.Hex("#2b3444"), selectedText: ui.Hex("#c7d8f5"),
		enabled:      ui.Hex("#2a313c"),
		enabledHover: ui.Hex("#343d4b"), enabledPressed: ui.Hex("#2b3442"),
		sliderKnob: ui.Hex("#e2e6ee"),
		input:      ui.Hex("#101113"),
		line:       ui.Hex("#303237"), lineSoft: ui.Hex("#26282c"),
		text: ui.Hex("#ededf0"), textDim: ui.Hex("#a9aeb7"), dim: ui.Hex("#8b9099"),
		accent: ui.Hex("#8cb4ff"), accentText: ui.Hex("#0a1420"),
		accentHover: ui.Hex("#a6c7ff"), accentPressed: ui.Hex("#7da9f0"),
		sound: ui.Hex("#d99ac0"),
		good:  ui.Hex("#86cfa4"), warn: ui.Hex("#e3c069"), bad: ui.Hex("#f08a8a"),
		inverse: ui.Hex("#232428"), inverseText: ui.Hex("#ededf0"),
		accentSoft: ui.RGBA(140, 180, 255, 0.18), soundSoft: ui.RGBA(217, 154, 192, 0.18),
	},
	{
		name: "daylight", label: "Daylight", labelZH: "日光", dark: false,
		bg: ui.Hex("#f5f6f8"), bgSoft: ui.Hex("#eceef1"),
		panel: ui.Hex("#ffffff"), panel2: ui.Hex("#f4f5f7"),
		hover: ui.Hex("#eef0f3"), pressed: ui.Hex("#e3e6ea"),
		selected: ui.Hex("#dbe7fb"), selectedText: ui.Hex("#1d4f9c"),
		enabled:      ui.Hex("#e8f0fc"),
		enabledHover: ui.Hex("#d2e2f8"), enabledPressed: ui.Hex("#c5d8f3"),
		sliderKnob: ui.Hex("#1f2937"),
		input:      ui.Hex("#ffffff"),
		line:       ui.Hex("#d6d9de"), lineSoft: ui.Hex("#e6e8ec"),
		text: ui.Hex("#14161a"), textDim: ui.Hex("#4f5661"), dim: ui.Hex("#6b7280"),
		accent: ui.Hex("#1f6feb"), accentText: ui.Hex("#ffffff"),
		accentHover: ui.Hex("#4a86f0"), accentPressed: ui.Hex("#1858c9"),
		sound: ui.Hex("#b4327a"),
		good:  ui.Hex("#16794a"), warn: ui.Hex("#8a5a00"), bad: ui.Hex("#b42318"),
		inverse: ui.Hex("#14161a"), inverseText: ui.Hex("#f5f6f8"),
		accentSoft: ui.RGBA(31, 111, 235, 0.12), soundSoft: ui.RGBA(180, 50, 122, 0.12),
	},
	{
		name: "celadon", label: "Celadon", labelZH: "青瓷", dark: false,
		bg: ui.Hex("#f1f5f3"), bgSoft: ui.Hex("#e7eeea"),
		panel: ui.Hex("#ffffff"), panel2: ui.Hex("#eef4f1"),
		hover: ui.Hex("#e9f1ed"), pressed: ui.Hex("#dde9e3"),
		selected: ui.Hex("#d8ebe4"), selectedText: ui.Hex("#0b5f4d"),
		enabled:      ui.Hex("#e2f0ea"),
		enabledHover: ui.Hex("#cfe4db"), enabledPressed: ui.Hex("#c1d9cf"),
		sliderKnob: ui.Hex("#173b33"),
		input:      ui.Hex("#ffffff"),
		line:       ui.Hex("#cdddd4"), lineSoft: ui.Hex("#dde9e3"),
		text: ui.Hex("#10201a"), textDim: ui.Hex("#4a5f56"), dim: ui.Hex("#647a70"),
		accent: ui.Hex("#0e7c66"), accentText: ui.Hex("#ffffff"),
		accentHover: ui.Hex("#2e947c"), accentPressed: ui.Hex("#0b6553"),
		sound: ui.Hex("#a63d72"),
		good:  ui.Hex("#1a7f52"), warn: ui.Hex("#7d5a00"), bad: ui.Hex("#b02a2a"),
		inverse: ui.Hex("#10201a"), inverseText: ui.Hex("#f1f5f3"),
		accentSoft: ui.RGBA(14, 124, 102, 0.14), soundSoft: ui.RGBA(166, 61, 114, 0.14),
	},
	{
		name: "noir", label: "Noir", labelZH: "纯黑", dark: true,
		bg: ui.Hex("#000000"), bgSoft: ui.Hex("#07070a"),
		panel: ui.Hex("#0d0d12"), panel2: ui.Hex("#16161d"),
		hover: ui.Hex("#16161d"), pressed: ui.Hex("#20202a"),
		selected: ui.Hex("#1b2a3f"), selectedText: ui.Hex("#9ec8ff"),
		enabled:      ui.Hex("#1e2c3d"),
		enabledHover: ui.Hex("#26364a"), enabledPressed: ui.Hex("#1e2c3e"),
		sliderKnob: ui.Hex("#dfe7f5"),
		input:      ui.Hex("#000000"),
		line:       ui.Hex("#2b2b36"), lineSoft: ui.Hex("#1d1d25"),
		text: ui.Hex("#ffffff"), textDim: ui.Hex("#c2c7d0"), dim: ui.Hex("#9aa0ab"),
		accent: ui.Hex("#4da3ff"), accentText: ui.Hex("#00101f"),
		accentHover: ui.Hex("#7cb8ff"), accentPressed: ui.Hex("#3d8be8"),
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
	colorHover = p.hover
	colorPressed = p.pressed
	colorSelected = p.selected
	colorSelectedText = p.selectedText
	colorEnabled = p.enabled
	colorEnabledHover = p.enabledHover
	colorEnabledPress = p.enabledPressed
	colorSliderKnob = p.sliderKnob
	colorInput = p.input
	colorLine = p.line
	colorLineSoft = p.lineSoft
	colorText = p.text
	colorTextDim = p.textDim
	colorDim = p.dim
	colorAccent = p.accent
	colorAccentText = p.accentText
	colorAccentHi = p.accentHover
	colorAccentDown = p.accentPressed
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
	theme.SurfaceHover = colorHover
	theme.SurfacePressed = colorPressed
	// Borders are the quietest line in the interface: a hard outline around
	// every button and card is what makes a dark tool look like a spreadsheet.
	// The surface ladder separates regions; only inputs and popovers get a
	// visible edge.
	theme.Border = colorBg
	theme.Text = colorText
	theme.TextMuted = colorTextDim
	theme.Accent = colorAccent
	theme.AccentHover = colorAccentHi
	theme.AccentPressed = colorAccentDown
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
