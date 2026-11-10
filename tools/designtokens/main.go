// Command designtokens turns the shared design tokens into the files each
// client consumes: a CSS custom property sheet for the web page and Go
// constants plus a MyGo theme for the desktop window.
//
// The tokens are the one place where a colour, a radius or a text size is
// chosen. The two clients render with different engines - HTML and CSS on one
// side, MyGo's own widget tree on the other - so components cannot be shared
// verbatim; what is shared is this vocabulary and the layout it describes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type token struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type color struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

type alphaColor struct {
	Name  string  `json:"name"`
	Hex   string  `json:"hex"`
	Alpha float64 `json:"alpha"`
}

type tokens struct {
	Colors []color      `json:"colors"`
	Alphas []alphaColor `json:"alphas"`
	Radius []token      `json:"radius"`
	Space  []token      `json:"space"`
	Text   []token      `json:"text"`
	Font   struct {
		Mono string `json:"mono"`
	} `json:"font"`
}

func main() {
	source := flag.String("source", "design/tokens.json", "the token file")
	web := flag.String("web", "web/tokens.css", "the CSS custom property sheet to write")
	desktop := flag.String("desktop", "desktop/tokens.go", "the Go constants to write")
	panel := flag.String("panel", "desktop/webpanel/tokens.css", "the same sheet for the web view inside the desktop window")
	flag.Parse()

	data, err := os.ReadFile(*source)
	if err != nil {
		log.Fatalf("designtokens: read %s: %v", *source, err)
	}
	var t tokens
	if err := json.Unmarshal(data, &t); err != nil {
		log.Fatalf("designtokens: parse %s: %v", *source, err)
	}
	if len(t.Colors) == 0 {
		log.Fatalf("designtokens: %s declares no colours", *source)
	}

	if err := os.WriteFile(*web, []byte(css(t, *source)), 0o644); err != nil {
		log.Fatalf("designtokens: write %s: %v", *web, err)
	}
	if err := os.WriteFile(*desktop, []byte(golang(t, *source)), 0o644); err != nil {
		log.Fatalf("designtokens: write %s: %v", *desktop, err)
	}
	// The desktop window shows a small page; it gets the same sheet so the two
	// clients cannot drift apart in colour or spacing.
	if err := os.MkdirAll(filepath.Dir(*panel), 0o755); err != nil {
		log.Fatalf("designtokens: make room for %s: %v", *panel, err)
	}
	if err := os.WriteFile(*panel, []byte(css(t, *source)), 0o644); err != nil {
		log.Fatalf("designtokens: write %s: %v", *panel, err)
	}
	log.Printf("designtokens: wrote %s, %s and %s from %s", *web, *desktop, *panel, *source)
}

// header is the same in both files: whoever opens them should know they are
// generated and where the values come from.
func header(source, comment string) string {
	return fmt.Sprintf("%s Generated from %s by tools/designtokens; edit the tokens, not this file.\n",
		comment, source)
}

func css(t tokens, source string) string {
	var b strings.Builder
	b.WriteString(header(source, "/*"))
	b.WriteString(" */\n")
	b.WriteString(":root {\n")
	for _, c := range t.Colors {
		fmt.Fprintf(&b, "  --%s: %s;\n", c.Name, c.Hex)
	}
	for _, c := range t.Alphas {
		r, g, bl := rgb(c.Hex)
		fmt.Fprintf(&b, "  --%s: rgba(%d, %d, %d, %s);\n", c.Name, r, g, bl, trim(c.Alpha))
	}
	for _, r := range t.Radius {
		fmt.Fprintf(&b, "  --radius-%s: %spx;\n", r.Name, trim(r.Value))
	}
	for _, s := range t.Space {
		fmt.Fprintf(&b, "  --space-%s: %spx;\n", s.Name, trim(s.Value))
	}
	for _, s := range t.Text {
		fmt.Fprintf(&b, "  --text-%s: %spx;\n", s.Name, trim(s.Value))
	}
	fmt.Fprintf(&b, "  --font-mono: %s;\n", t.Font.Mono)
	b.WriteString("}\n")
	return b.String()
}

func golang(t tokens, source string) string {
	var b strings.Builder
	b.WriteString(header(source, "//"))
	b.WriteString("package main\n\nimport \"github.com/egoist/mygo/ui\"\n\n")
	b.WriteString("// Colours shared with the web client. ui.Hex is a call, so these are vars.\nvar (\n")
	for _, c := range t.Colors {
		fmt.Fprintf(&b, "\tcolor%s = ui.Hex(%q)\n", exported(c.Name), c.Hex)
	}
	for _, c := range t.Alphas {
		r, g, bl := rgb(c.Hex)
		fmt.Fprintf(&b, "\tcolor%s = ui.RGBA(%d, %d, %d, %s)\n", exported(c.Name), r, g, bl, trim(c.Alpha))
	}
	b.WriteString(")\n\n// Radii, spacing and text sizes shared with the web client.\nconst (\n")
	for _, r := range t.Radius {
		fmt.Fprintf(&b, "\tradius%s = %s\n", exported(r.Name), trim(r.Value))
	}
	for _, s := range t.Space {
		fmt.Fprintf(&b, "\tspace%s = %s\n", exported(s.Name), trim(s.Value))
	}
	for _, s := range t.Text {
		fmt.Fprintf(&b, "\ttext%s = %s\n", exported(s.Name), trim(s.Value))
	}
	b.WriteString(")\n\n")
	b.WriteString(`// muxshiftTheme is the desktop window's palette, built from the same tokens as
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
`)
	return b.String()
}

// exported turns a token name such as "text-dim" into a Go identifier suffix.
func exported(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func rgb(hex string) (int, int, int) {
	value := strings.TrimPrefix(hex, "#")
	if len(value) != 6 {
		log.Fatalf("designtokens: %q is not a six digit colour", hex)
	}
	number, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		log.Fatalf("designtokens: %q is not a colour: %v", hex, err)
	}
	return int(number >> 16 & 0xff), int(number >> 8 & 0xff), int(number & 0xff)
}

// trim prints a number without a trailing ".0" so CSS and Go both read well.
func trim(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
