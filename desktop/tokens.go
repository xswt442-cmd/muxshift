// Generated from design/tokens.json by tools/designtokens; edit the tokens, not this file.
package main

import "github.com/egoist/mygo/ui"

// Colours shared with the web client. ui.Hex is a call, so these are vars.
var (
	colorBg         = ui.Hex("#0c0e12")
	colorBgSoft     = ui.Hex("#101319")
	colorPanel      = ui.Hex("#151920")
	colorPanel2     = ui.Hex("#1b202a")
	colorLine       = ui.Hex("#262c37")
	colorLineSoft   = ui.Hex("#1e242e")
	colorText       = ui.Hex("#e9ecf2")
	colorTextDim    = ui.Hex("#99a2b1")
	colorDim        = ui.Hex("#6d7684")
	colorAccent     = ui.Hex("#5aa9ff")
	colorSound      = ui.Hex("#ff79b0")
	colorGood       = ui.Hex("#5fd39a")
	colorWarn       = ui.Hex("#f5c451")
	colorBad        = ui.Hex("#ff7d7d")
	colorAccentSoft = ui.RGBA(90, 169, 255, 0.16)
	colorSoundSoft  = ui.RGBA(255, 121, 176, 0.16)
)

// Radii, spacing and text sizes shared with the web client.
const (
	radiusSm  = 9
	radiusMd  = 14
	radiusLg  = 18
	spaceXs   = 4
	spaceSm   = 8
	spaceMd   = 12
	spaceLg   = 16
	spaceXl   = 24
	spaceXxl  = 32
	textMicro = 11
	textSmall = 12
	textBody  = 13
	textLarge = 15
	textTitle = 22
)

// muxshiftTheme is the desktop window's palette, built from the same tokens as
// the web page so the two clients read as one product.
func muxshiftTheme() *ui.Theme {
	theme := ui.DarkTheme()
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
	theme.Selection = colorAccentSoft
	theme.Success = colorGood
	theme.Warning = colorWarn
	theme.Danger = colorBad
	return theme
}
