package main

// P1: one "add a source" chooser behind both the top bar's Open… and the
// Sources page's Add. The three choices are a person's own statement of what
// they have — a file on this machine, a media address, or a website a browser
// opens. Guessing the intent from the shape of a string would be a guess, and a
// wrong guess either imports the wrong thing or opens a page that was meant to
// be a stream.

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type addChoice int8

const (
	addChoiceNone addChoice = iota
	addChoiceFile
	addChoiceMedia
	addChoiceSite
)

func (c addChoice) key() string {
	switch c {
	case addChoiceFile:
		return "file"
	case addChoiceMedia:
		return "media"
	case addChoiceSite:
		return "site"
	}
	return "none"
}

func (c addChoice) label() string {
	switch c {
	case addChoiceFile:
		return "Local file"
	case addChoiceMedia:
		return "Media link"
	case addChoiceSite:
		return "Website address"
	}
	return "Nothing chosen"
}

func (c addChoice) explanation() string {
	switch c {
	case addChoiceFile:
		return "A video on this machine. The desktop file picker is not in this build yet; an absolute path still works under Media link."
	case addChoiceMedia:
		return "An http(s) address to a playlist or a single file. It is read away from the drawing thread and joins this session when the first picture is ready."
	case addChoiceSite:
		return "A page in your browser. The system browser opens it, and an extension hands the stream back; in this build the browser is not opened yet."
	}
	return "Say which of the three you have. The app does not guess from what is typed in the address field."
}

// openAddSource opens the chooser from either entry point and forgets what the
// last attempt said.
func (a *appState) openAddSource() {
	a.addOpen = true
	a.addChoice = addChoiceNone
	a.addNote = ""
	a.addNoteBad = false
	a.addSiteText = ""
}

// closeAddSource closes the chooser, including the note the attempt left.
func (a *appState) closeAddSource() {
	a.addOpen = false
	a.addChoice = addChoiceNone
	a.addNote = ""
	a.addNoteBad = false
}

// submitAddSource runs the chosen path. Only the media choice reaches the
// existing add command; the other two say what is missing in this build rather
// than pretend, and the website choice never creates a Core source.
func (a *appState) submitAddSource() (commandResult, bool) {
	switch a.addChoice {
	case addChoiceMedia:
		result, ok := a.addMediaAddress(a.importText)
		if !ok {
			a.addNote, a.addNoteBad = result.Err.Error(), true
			return result, false
		}
		a.closeAddSource()
		return result, true
	case addChoiceSite:
		if err := siteAddressProblem(a.addSiteText); err != nil {
			a.addNote, a.addNoteBad = err.Error(), true
			return commandResult{}, false
		}
		// P4 opens the system browser. Until it exists, keeping the address
		// and saying so is the honest outcome: a page that opens is not a
		// source, and a source that fails to appear would be worse.
		a.addNote = "the browser is not opened in this build, so no source was added; the handoff step is where this path continues"
		a.addNoteBad = false
		return commandResult{}, false
	case addChoiceFile:
		a.addNote = "the desktop file picker is not in this build; an absolute path under Media link is the way a file is added today"
		a.addNoteBad = false
		return commandResult{}, false
	}
	a.addNote = "choose one of the three first"
	a.addNoteBad = false
	return commandResult{}, false
}

// addMediaAddress checks the address and starts the existing add command. Both
// the chooser's Add and Enter in the Sources address field go through it, so a
// person who types an address sees one set of rules.
func (a *appState) addMediaAddress(text string) (commandResult, bool) {
	if err := mediaAddressProblem(text); err != nil {
		return commandResult{
			Command: command{Kind: commandAdd, URI: text},
			At:      time.Now(),
			Err:     err,
		}, false
	}
	result := a.runCommand(command{Kind: commandAdd, URI: text})
	return result, result.Err == nil
}

// addSourcePanel draws the chooser inside the modal's panel.
func (a *appState) addSourcePanel(c *ui.Context) {
	ui.Column(c).Gap(spaceSm).MinWidth(520).MaxWidth(560).Children(func() {
		ui.Text(c, "Add a source").FontSize(textTitle).TextColor(colorText)
		ui.Text(c, "Three things can be added. Pick the one you have; the app does not guess from the address text.").
			FontSize(textSmall).TextColor(colorTextDim).Wrap()
		ui.Row(c).Gap(spaceSm).WidthPercent(100).Children(func() {
			for _, choice := range []addChoice{addChoiceFile, addChoiceMedia, addChoiceSite} {
				label := choice.label()
				if a.addChoice == choice {
					label += " ✓"
				}
				button := ui.Button(c.Key("add-"+choice.key()), label).Grow(1).Height(36)
				if a.addChoice == choice {
					button.Border(1, colorAccent).Background(colorPanel2)
				}
				if button.Clicked() {
					a.addChoice = choice
					a.addNote, a.addNoteBad = "", false
				}
			}
		})
		ui.Text(c, a.addChoice.explanation()).FontSize(textSmall).TextColor(colorTextDim).Wrap()
		switch a.addChoice {
		case addChoiceMedia:
			ui.TextInput(c, &a.importText).WidthPercent(100).Height(34).
				Placeholder("https://… .m3u8  or  C:\\path\\file.mp4").
				Description("the media address or file path to read")
		case addChoiceSite:
			ui.TextInput(c, &a.addSiteText).WidthPercent(100).Height(34).
				Placeholder("https://example.com/watch?v=…").
				Description("the page to open in the system browser")
		}
		if a.addNote != "" {
			tone := colorTextDim
			if a.addNoteBad {
				tone = colorBad
			}
			ui.Text(c, a.addNote).FontSize(textSmall).TextColor(tone).Wrap()
		}
		ui.Row(c).Gap(spaceSm).WidthPercent(100).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1)
			if ui.Button(c.Key("add-cancel"), "Cancel").Width(88).Height(34).Clicked() {
				a.closeAddSource()
			}
			submit := ui.Button(c.Key("add-submit"), "Add").Width(96).Height(34)
			switch a.addChoice {
			case addChoiceMedia:
				submit.Tooltip("read this address and add it to the session")
			case addChoiceSite:
				submit.Disabled(true).Tooltip("the system browser opens a website in the handoff step; this build does not open it")
			case addChoiceFile:
				submit.Disabled(true).Tooltip("the desktop file picker is not in this build")
			default:
				submit.Disabled(true).Tooltip("choose what you want to add first")
			}
			if submit.Clicked() {
				a.submitAddSource()
			}
		})
	})
}

// siteAddressProblem says why an address cannot be opened as a website. A
// browser follows almost anything, so the checks are explicit: an http(s) page
// with a host, no space, and no credentials in the address.
func siteAddressProblem(raw string) error {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fmt.Errorf("type the address of the page to open")
	}
	if strings.ContainsAny(text, " \t\r\n") {
		return fmt.Errorf("the address contains a space; a space in a URL is written %%20")
	}
	u, err := url.Parse(text)
	if err != nil {
		return fmt.Errorf("%q is not an address a browser can open: %v", text, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%q is not an http or https address, so it is not opened", text)
	}
	if u.Host == "" {
		return fmt.Errorf("%q names no host, so it is not opened", text)
	}
	if u.User != nil {
		return fmt.Errorf("%q carries a user name; this app does not open addresses with credentials", u.Redacted())
	}
	return nil
}

// mediaAddressProblem says why an address cannot be handed to the importer: an
// absolute local path, or an http(s) URL without credentials. The file's
// existence is the player's question, not this check's.
func mediaAddressProblem(raw string) error {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fmt.Errorf("type a media address or a file path first")
	}
	if strings.ContainsAny(text, "\r\n\t") {
		return fmt.Errorf("the address contains a control character")
	}
	if filepath.IsAbs(text) {
		return nil
	}
	u, err := url.Parse(text)
	if err != nil {
		return fmt.Errorf("%q is neither an absolute path nor an http(s) address", text)
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		if u.Host == "" {
			return fmt.Errorf("%q names no host, so there is nothing to read", text)
		}
		if u.User != nil {
			return fmt.Errorf("%q carries a user name; this app reads addresses without credentials", u.Redacted())
		}
		return nil
	}
	if u.Scheme != "" {
		return fmt.Errorf("%q uses the %q scheme; only http and https addresses can be read", text, u.Scheme)
	}
	return fmt.Errorf("%q is a relative path; only an absolute path or an http(s) address can be read", text)
}
