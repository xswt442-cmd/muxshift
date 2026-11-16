package main

// P1: one "add a source" chooser behind both the top bar's Open… and the
// Sources page's Add. The three choices are a person's own statement of what
// they have — a file on this machine, a media address, or a website a browser
// opens. Guessing the intent from the shape of a string would be a guess, and a
// wrong guess either imports the wrong thing or opens a page that was meant to
// be a stream.

import (
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
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
		return "A video on this machine. It opens the desktop's own file picker, and what you pick joins this session like any other source."
	case addChoiceMedia:
		return "An http(s) address to a playlist or a single file. It is read away from the drawing thread and joins this session when the first picture is ready."
	case addChoiceSite:
		return "A page in your browser. The system browser opens it, and the MuxShift extension there hands one media address back; the page itself never becomes a source. A browser without the extension can only be used through Media link."
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
	a.siteOpened = false
}

// closeAddSource closes the chooser, including the note the attempt left.
func (a *appState) closeAddSource() {
	a.addOpen = false
	a.addChoice = addChoiceNone
	a.addNote = ""
	a.addNoteBad = false
	a.siteOpened = false
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
		// P4 opens the page and says plainly that the media is still in the
		// browser: an opened page is not a source, and handing the stream back
		// is the extension step, which this build does not have.
		result, ok := a.openSiteAddress(a.addSiteText)
		if !ok {
			a.addNote, a.addNoteBad = result.Err.Error(), true
			return result, false
		}
		// The browser is open, but the media is still there: the panel stays and
		// says so, because closing it would look like the page had been added.
		a.siteOpened = true
		a.addNote, a.addNoteBad = result.Message, false
		return result, true
	case addChoiceFile:
		// P2: the picker blocks until it is dismissed, so it runs off the
		// drawing thread and comes back through acceptPickedFile.
		if a.pickingFile {
			a.addNote = "a file dialog is already open; finish it first"
			a.addNoteBad = false
			return commandResult{}, false
		}
		a.closeAddSource()
		a.startFilePick()
		return commandResult{}, true
	}
	a.addNote = "choose one of the three first"
	a.addNoteBad = false
	return commandResult{}, false
}

// startFilePick opens the desktop's file picker off the drawing thread. The
// picker returns nothing when it is cancelled, and a cancellation must leave
// the session and the last error exactly as they were.
func (a *appState) startFilePick() {
	a.pickingFile = true
	go func() {
		path, err := a.chooseLocalFile()
		deliver := func() { a.acceptPickedFile(path, err) }
		if a.onMain != nil {
			a.onMain(deliver)
			return
		}
		mygoRunOnMain(deliver)
	}()
}

// chooseLocalFile is the real picker. The seam keeps the tests off native
// dialogs while the same outcome runs through acceptPickedFile.
func (a *appState) chooseLocalFile() (string, error) {
	if a.pickFile != nil {
		return a.pickFile()
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: a.window,
		Title:  "Add a source",
		Filters: []mygo.FileFilter{
			{Name: "Media files", Extensions: []string{"mp4", "m4v", "mov", "mkv", "webm", "ts", "m3u8"}},
			{Name: "All files", Extensions: []string{"*"}},
		},
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// acceptPickedFile turns the picker's answer into an add command, or into
// nothing at all when the picker was cancelled.
func (a *appState) acceptPickedFile(path string, err error) {
	a.pickingFile = false
	if err != nil {
		result := commandResult{
			Command: command{Kind: commandAdd},
			At:      time.Now(),
			Err:     fmt.Errorf("choosing a file failed: %w", err),
		}
		a.lastErr = result.Err
		a.report(result)
		a.invalidate()
		return
	}
	if path == "" {
		log.Printf("add-source: the file picker was cancelled; the session was not touched")
		return
	}
	result, _ := a.addMediaAddress(path)
	a.report(result)
}

// openSiteAddress hands the page to the system browser. The browser is opened
// by the frame's own context, so the seam is what a test replaces; without a
// seam or a browser the attempt says so and nothing is recorded. An opened page
// is never a source: the message says where the media still is.
func (a *appState) openSiteAddress(text string) (commandResult, bool) {
	address := strings.TrimSpace(text)
	command := command{Kind: commandAdd, URI: address}
	refuse := func(format string, args ...any) (commandResult, bool) {
		return commandResult{Command: command, At: time.Now(), Err: fmt.Errorf(format, args...)}, false
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return refuse("%q is not an address a browser can open: %v", address, err)
	}
	opener := a.openURL
	if opener == nil {
		opener = a.contextOpenURL
	}
	if opener == nil {
		return refuse("this build has no system browser to open %s", parsed.Redacted())
	}
	opener(address, func(err error) {
		if err != nil {
			a.lastErr = fmt.Errorf("opening %s failed: %w", parsed.Redacted(), err)
			a.report(commandResult{Command: command, At: time.Now(), Err: a.lastErr})
			a.invalidate()
			return
		}
		log.Printf("add-source: opened %s in the system browser", parsed.Redacted())
	})
	result := commandResult{
		Command: command,
		At:      time.Now(),
		Message: fmt.Sprintf("opened %s in your browser; the MuxShift extension there can hand one media address back to this window", parsed.Redacted()),
	}
	a.report(result)
	return result, true
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

// addSubmitLabel names what the Confirm button does: a page that is already in
// the browser can only be opened again, and saying so keeps the two states apart.
func (a *appState) addSubmitLabel() string {
	if a.addChoice == addChoiceSite && a.siteOpened {
		return "Open again"
	}
	return "Add"
}

// addSourcePanel draws the chooser inside the modal's panel.
func (a *appState) addSourcePanel(c *ui.Context) {
	// The website choice needs a browser, and the frame's context owns the only
	// way to ask for one. Handing it over here keeps the choice usable from the
	// panel alone, while a test can still put its own opener in place instead.
	a.contextOpenURL = c.OpenURLThen
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
			submit := ui.Button(c.Key("add-submit"), a.addSubmitLabel()).Width(116).Height(34)
			switch a.addChoice {
			case addChoiceMedia:
				submit.Tooltip("read this address and add it to the session")
			case addChoiceSite:
				submit.Tooltip("open this page in your default browser; the media still has to be handed back")
			case addChoiceFile:
				submit.Tooltip("open the desktop file picker")
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
