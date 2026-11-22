// The small set of controls the console, the rail and the pages share. A
// picture of one product needs one icon button, one list row and one offset
// stepper: when each panel builds its own, the same action looks different in
// every corner of the window.
//
// The rules come from the ms-v0 spec: a control is transparent until the
// pointer is on it; hover is the raised surface, pressed is one step further,
// selected is the low-saturation accent, and disabled text is the quietest
// ink. Nothing here draws a permanent grey box around an operation.
package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// stateFill is the one table of pointer states. A selected control keeps its
// mark while the pointer is elsewhere; a disabled one has no pointer state at
// all, so callers leave it unselected.
func stateFill(hovered, pressed, selected bool) ui.Color {
	switch {
	case pressed:
		return colorPressed
	case selected:
		return colorSelected
	case hovered:
		return colorHover
	}
	return ui.Transparent
}

// iconButton is the one icon operation: a 28x28 hit area, 16 DIP of icon, no
// border, and a background only while the pointer is on it. selected marks a
// mode that stays on (the list layout, an enabled panel), not a press.
func (a *appState) iconButton(c *ui.Context, key string, icon *ui.SVG, tip string, selected bool, onClick func()) bool {
	ink := colorTextDim
	if selected {
		ink = colorSelectedText
	}
	return a.iconButtonSized(c, key, icon, tip, 28, ink, selected, onClick)
}

// ghostButton is a quiet text action: no face until the pointer is on it.
// Keeping the background transparent is what makes the transport read as
// controls on the canvas rather than as a row of dark blocks.
func (a *appState) ghostButton(c *ui.Context, key, label string, width float32, onClick func()) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, spaceMd).Radius(radiusMd).
		AlignItems(ui.Center).Shrink(0).AlignSelf(ui.Center)
	if width > 0 {
		button = button.Width(width)
	}
	fill := ui.Transparent
	ink := colorTextDim
	if button.Pressed() {
		fill, ink = colorPressed, colorText
	} else if button.Hovered() {
		fill, ink = colorHover, colorText
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
	})
	if button.Clicked() && onClick != nil {
		onClick()
	}
	return button
}

// ghostIconButton is a quiet text action with one 14 DIP icon: the transport
// uses it where a word alone left the row looking like a list of links. The
// icon and the label share the button's ink, so hover and pressed stay one
// state.
func (a *appState) ghostIconButton(c *ui.Context, key, label string, icon *ui.SVG, width float32, onClick func()) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, spaceMd).Radius(radiusMd).
		AlignItems(ui.Center).Shrink(0).AlignSelf(ui.Center)
	if width > 0 {
		button = button.Width(width)
	}
	fill := ui.Transparent
	ink := colorTextDim
	if button.Pressed() {
		fill, ink = colorPressed, colorText
	} else if button.Hovered() {
		fill, ink = colorHover, colorText
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(ink)
			ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
		})
	})
	if button.Clicked() && onClick != nil {
		onClick()
	}
	return button
}

// rowSurface is the shared inset row. A list row carries its grouping in the
// background and its hover/selected state, not in a permanent outline: the
// outline is reserved for an operation block such as one source's sync card.
func (a *appState) rowSurface(c *ui.Context) ui.Element {
	return ui.Row(c).WidthPercent(100).Background(colorRow).Radius(radiusMd)
}

// columnSurface is rowSurface for a block with two lines, as one source's
// sync control is.
func (a *appState) columnSurface(c *ui.Context) ui.Element {
	return ui.Column(c).WidthPercent(100).Background(colorRow).Radius(radiusMd)
}

// quietIconButton is an operation that carries its state in the icon's ink and
// stays transparent otherwise: the eye on the stage, the sound a channel is
// making. A permanent square around every one of them is what the spec's
// "transparent until hover" rule removes.
func (a *appState) quietIconButton(c *ui.Context, key string, icon *ui.SVG, tip string, ink ui.Color, onClick func()) bool {
	return a.iconButtonSized(c, key, icon, tip, 28, ink, false, onClick)
}

// iconButtonSized is the one drawing of an icon operation. The stage's tool
// cluster is the only caller that asks for 32 instead of 28.
func (a *appState) iconButtonSized(c *ui.Context, key string, icon *ui.SVG, tip string, size float32, ink ui.Color, background bool, onClick func()) bool {
	button := ui.ButtonBase(c.Key(key)).Width(size).Height(size).Radius(radiusMd).Padding(0).
		Tooltip(tip)
	button = button.Background(stateFill(button.Hovered(), button.Pressed(), background))
	button = button.TextColor(ink)
	button.Children(func() {
		ui.Icon(c, icon).Size(16, 16)
	})
	if button.Clicked() {
		if onClick != nil {
			onClick()
		}
		return true
	}
	return false
}

// volumeSlider is the one level control: 0..1 with the console's width.
func (a *appState) volumeSlider(c *ui.Context, key string, value *float64, width float32) bool {
	return a.rangeSlider(c, key, value, 0, 1, width)
}

// rangeSlider draws a 4 DIP track, a 12 DIP round knob and the accent on the
// part that is in effect; width <= 0 fills the parent. SliderBase keeps the
// dragging, the arrow keys and the accessibility range, the look is ours.
func (a *appState) rangeSlider(c *ui.Context, key string, value *float64, lo, hi float64, width float32) bool {
	slider := ui.SliderBase(c.Key(key), value, lo, hi).Height(24).Padding(0, 6)
	if width > 0 {
		slider = slider.Width(width)
	} else {
		slider = slider.WidthPercent(100)
	}
	slider.Draw(func(p *ui.Painter, r ui.Rect) {
		frac := float32(0)
		if hi > lo {
			frac = float32((*value - lo) / (hi - lo))
		}
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1
		}
		// The draw rect is SliderBase's content box: the value mapping uses
		// the same x/width, so the track must not inset it a second time.
		mid := r.Y + r.H/2
		x0 := r.X
		track := r.W
		if track < 0 {
			track = 0
		}
		p.Fill(ui.Rect{X: x0, Y: mid - 2, W: track, H: 4}, colorLine, 2)
		p.Fill(ui.Rect{X: x0, Y: mid - 2, W: track * frac, H: 4}, colorAccent, 2)
		// A 14 DIP knob with a dark halo: the halo separates it from a track of
		// the same light value, so the control reads as a knob rather than a
		// dot painted on the line.
		p.Fill(ui.Rect{X: x0 + track*frac - 8, Y: mid - 8, W: 16, H: 16},
			ui.RGBA(0, 0, 0, 0.28), 8)
		p.Fill(ui.Rect{X: x0 + track*frac - 7, Y: mid - 7, W: 14, H: 14}, colorSliderKnob, 7)
	})
	return slider.Changed()
}

// offsetStepper is the one time-offset control: a 136x28 box holding a minus, a
// monospaced number with its unit in the quiet ink, and a plus. The border is
// the one place in the console that earns a line; the buttons inside do not.
func (a *appState) offsetStepper(c *ui.Context, key string, offset time.Duration, step time.Duration, onChange func(time.Duration)) {
	ui.Row(c).Width(136).Height(28).Radius(radiusCard).Background(colorBg).
		Border(1, colorLineSoft).AlignItems(ui.Center).Children(func() {
		stepButton := func(buttonKey string, icon *ui.SVG, tip string, delta time.Duration) {
			button := ui.ButtonBase(c.Key(buttonKey)).Width(26).Height(26).Radius(radiusSm).
				Padding(0).Tooltip(tip)
			button = button.Background(stateFill(button.Hovered(), button.Pressed(), false))
			button = button.TextColor(colorTextDim)
			button.Children(func() {
				ui.Icon(c, icon).Size(16, 16)
			})
			if button.Clicked() {
				onChange(delta)
			}
		}
		stepButton(key+"-", lucide.Minus(),
			fmt.Sprintf(a.tr("offset -%.2f s", "偏移 −%.2f 秒"), step.Seconds()), -step)
		ui.Box(c).Width(1).Height(16).Background(colorLineSoft)
		ui.Box(c).Width(80).HeightPercent(100).Center().Children(func() {
			ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%+.2f", offset.Seconds())).FontSize(textBody).
					FontWeight(500).TextColor(colorText).FontFeatures("tnum")
				ui.Text(c, "s").FontSize(textMicro).FontWeight(400).TextColor(colorDim)
			})
		})
		ui.Box(c).Width(1).Height(16).Background(colorLineSoft)
		stepButton(key+"+", lucide.Plus(),
			fmt.Sprintf(a.tr("offset +%.2f s", "偏移 +%.2f 秒"), step.Seconds()), step)
	})
}

// --- P7 control primitives --------------------------------------------------
//
// The console used to build each text button by hand, so the same action was 26
// DIP high in one panel and 34 in the next, with a different corner and padding.
// The three families below are now the one definition of a raised or filled
// button: they differ only in colour, never in shape, spacing or type. Each
// takes the scale-aware control height and the shared control radius and draws a
// 12/500 label — the same spec as every other body control.

// controlFrame is the shared skeleton of a family button. Width 0 means "as wide
// as the label and padding need"; pass a width to pin it.
func (a *appState) controlFrame(c *ui.Context, key string, width float32) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Height(a.controlHeight()).
		Padding(0, controlPadX).Radius(radiusMd).AlignItems(ui.Center).
		Shrink(0).AlignSelf(ui.Center)
	if width > 0 {
		button = button.Width(width)
	}
	return button
}

// primaryButton is the accent-filled call to action: Add, OK, Play. Hover and
// pressed step through the accent's light/dark pair so it still reads as raised
// while a pointer is on it; disabled keeps the panel face and the quiet ink.
func (a *appState) primaryButton(c *ui.Context, key, label string, width float32, enabled bool, onClick func()) ui.Element {
	button := a.controlFrame(c, key, width)
	fill, ink := colorAccent, colorAccentText
	switch {
	case !enabled:
		fill, ink = colorPanel2, colorDim
		button = button.Disabled(true)
	case button.Pressed():
		fill, ink = colorAccentDown, colorAccentText
	case button.Hovered():
		fill, ink = colorAccentHi, colorAccentText
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
	})
	if button.Clicked() && enabled && onClick != nil {
		onClick()
	}
	return button
}

// secondaryButton is the quiet raised action beside a primary one: Cancel, the
// diagnostics buttons, a dialog's alternative. It carries a panel face so it
// sits above the page, and steps to hover/pressed while the pointer is on it.
func (a *appState) secondaryButton(c *ui.Context, key, label string, width float32, enabled bool, onClick func()) ui.Element {
	button := a.controlFrame(c, key, width)
	fill, ink := colorPanel2, colorText
	switch {
	case !enabled:
		fill, ink = ui.Transparent, colorDim
		button = button.Disabled(true)
	case button.Pressed():
		fill, ink = colorPressed, colorText
	case button.Hovered():
		fill = colorHover
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
	})
	if button.Clicked() && enabled && onClick != nil {
		onClick()
	}
	return button
}

// dangerButton is the destructive action. It stays flat until the pointer is on
// it, then keeps the warning ink the spec's destructive rows use; it never wears
// a permanent red box.
func (a *appState) dangerButton(c *ui.Context, key, label string, width float32, onClick func()) ui.Element {
	button := a.controlFrame(c, key, width)
	fill, ink := ui.Transparent, colorBad
	if button.Pressed() {
		fill = colorPressed
	} else if button.Hovered() {
		fill = colorHover
	}
	button = button.Background(fill).TextColor(ink)
	button.Children(func() {
		ui.Text(c, label).FontSize(textSmall).FontWeight(500).TextColor(ink).SingleLine()
	})
	if button.Clicked() && onClick != nil {
		onClick()
	}
	return button
}

// segmentTrack is the rounded trough a segmented control sits in: one inset
// background with a hairline, the same in the settings form, the console panel
// chips, the properties tabs and the add-source chooser.
func (a *appState) segmentTrack(c *ui.Context) ui.Element {
	return ui.Row(c).Height(a.controlHeight()).Padding(segmentPad).Gap(segmentGap).
		Radius(radiusMd).Background(colorBgSoft).Border(1, colorLineSoft).
		AlignItems(ui.Center)
}

// segmentedOption is one choice inside a segmentTrack. Selected uses the theme's
// single selected pair (the same as a settings tab), so "this one is on" looks
// identical everywhere; grow makes the option share the trough's width.
func (a *appState) segmentedOption(c *ui.Context, key, label string, selected, grow bool, onClick func()) {
	option := ui.ButtonBase(c.Key(key)).Height(a.controlHeight()-2*segmentPad).
		Padding(0, controlPadXDense).Radius(radiusSm).AlignItems(ui.Center).
		AlignSelf(ui.Center)
	if grow {
		option = option.Grow(1)
	}
	fill, ink := ui.Transparent, colorTextDim
	if selected {
		fill, ink = colorSelected, colorSelectedText
	}
	if option.Pressed() {
		fill, ink = colorPressed, colorText
	} else if option.Hovered() && !selected {
		fill, ink = colorHover, colorText
	}
	option = option.Background(fill).TextColor(ink)
	option.Children(func() {
		weight := 400
		if selected {
			weight = 500
		}
		ui.Text(c, label).FontSize(textSmall).FontWeight(weight).TextColor(ink).SingleLine()
	})
	if option.Clicked() && onClick != nil {
		onClick()
	}
}

// field is the one text input: the control height, the shared corner, one inset
// padding and the dark input well. Callers add the placeholder, the invalid
// state and the width on top.
func (a *appState) field(c *ui.Context, bind *string) ui.Element {
	return ui.TextInput(c, bind).Height(a.controlHeight()).Padding(0, fieldPadX).
		Radius(radiusMd).Background(colorInput).Border(1, colorLine).
		PlaceholderColor(colorDim)
}

// sectionTitle is a panel or page section heading: 13 DIP, semibold, full ink.
func (a *appState) sectionTitle(c *ui.Context, label string) {
	ui.Text(c, label).FontSize(textBody).FontWeight(600).TextColor(colorText).SingleLine()
}

// overlineLabel is a quiet group label above a control cluster: 11 DIP,
// semibold, dim. It is what the settings rail and the console headers use.
func (a *appState) overlineLabel(c *ui.Context, label string) {
	ui.Text(c, label).FontSize(textMicro).FontWeight(600).TextColor(colorTextDim).SingleLine()
}

// numericReadout is one numeric value beside a slider or stepper: tabular
// figures, the control label size, right aligned in a fixed width so a column of
// them lines up.
func (a *appState) numericReadout(c *ui.Context, text string, width float32) {
	ui.Text(c, text).Width(width).FontSize(textSmall).FontWeight(500).
		TextColor(colorTextDim).TextAlign(ui.End).FontFeatures("tnum").SingleLine()
}
