package main

import (
	"strings"

	"github.com/xswt442-cmd/muxshift/desktop/internal/win"
)

// The language setting's stored values. "auto" follows the window locale, which
// on a Chinese Windows install is zh-*; an explicit choice survives restarts.
const (
	languageAuto = "auto"
	languageZH   = "zh"
	languageEN   = "en"
)

// normalizeLanguage maps what the settings file holds onto one of the three
// values. An unknown value falls back to auto rather than to English: a typo in
// the file must not silently change the language for a Chinese user.
func normalizeLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "zh", "zh-cn", "zh-hans", "chinese":
		return languageZH
	case "en", "en-us", "english":
		return languageEN
	}
	return languageAuto
}

// lang is the language the next frame is drawn in.
func (a *appState) lang() string {
	if mode := normalizeLanguage(a.settings.Language); mode != languageAuto {
		return mode
	}
	if a.systemLang == languageZH {
		return languageZH
	}
	return languageEN
}

// chinese reports whether the window is in Chinese.
func (a *appState) chinese() bool { return a.lang() == languageZH }

// tr picks between the English and the Chinese text of one label. Every string
// the shell draws goes through it, so a switch cannot leave half a page in the
// other language. Data (file names, addresses, ids) is passed through as-is and
// never translated.
func (a *appState) tr(en, zh string) string {
	if a.chinese() {
		return zh
	}
	return en
}

// detectLocale records the window's language once, before the first frame. The
// UI library reports the C locale, so when that says nothing useful the Windows
// display locale decides — a Chinese install must open in Chinese.
func (a *appState) detectLocale(locale string) {
	if a.systemLang != "" {
		return
	}
	lower := strings.ToLower(locale)
	if strings.HasPrefix(lower, "zh") {
		a.systemLang = languageZH
		return
	}
	if strings.HasPrefix(strings.ToLower(win.Locale()), "zh") {
		a.systemLang = languageZH
		return
	}
	a.systemLang = languageEN
}

// fontFamily resolves the font setting. mygo takes one family per theme, so an
// "auto" choice has to name a family that carries both scripts; the explicit
// choices are the families Windows ships and the app can count on.
func (a *appState) fontFamily() string {
	switch a.settings.FontFamily {
	case "segoe":
		return "Segoe UI"
	case "yahei":
		return "Microsoft YaHei UI"
	case "cascadia":
		return "Cascadia Mono, Microsoft YaHei UI"
	case "inter":
		return "Inter, Microsoft YaHei UI"
	}
	if a.chinese() {
		// Latin and digits take Segoe UI's tighter forms; Chinese falls
		// through to YaHei UI glyph by glyph. Asking for the language's one
		// family instead is what made filenames and numbers look soft.
		return "Segoe UI, Microsoft YaHei UI"
	}
	return "Segoe UI"
}

// syncAppearance rebuilds the token vars when the applied settings name a
// different theme, scale or font. It runs at the top of view, before the theme
// is handed to the window, so the first frame already uses the saved look.
//
// Appearance is applied the moment a control changes (applyAppearanceNow), so
// the live settings always name the look to draw; there is no draft preview to
// undo when the page is left. An unapplied Reset must likewise not repaint the
// window, which is why the draft is not read here.
func (a *appState) syncAppearance() {
	theme, scale := a.settings.Theme, a.settings.TextScale
	if a.appliedTheme != theme {
		applyPalette(themePack(theme))
		a.appliedTheme = theme
	}
	if a.appliedScale != scale {
		applyTextScale(scalePack(scale))
		a.appliedScale = scale
	}
	if family := a.fontFamily(); currentFont != family {
		currentFont = family
	}
}
