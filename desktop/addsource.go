package main

// P1: one "add a source" chooser behind both the top bar's Open… and the
// Sources page's Add. The three choices are a person's own statement of what
// they have — a file on this machine, a media address, or a website a browser
// opens. Guessing the intent from the shape of a string would be a guess, and a
// wrong guess either imports the wrong thing or opens a page that was meant to
// be a stream.

import (
	"errors"
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

func (a *appState) addChoiceLabel(c addChoice) string {
	switch c {
	case addChoiceFile:
		return a.tr("Local file", "本地文件")
	case addChoiceMedia:
		return a.tr("Media link", "媒体地址")
	case addChoiceSite:
		return a.tr("Website address", "网页地址")
	}
	return a.tr("Nothing chosen", "还没选")
}

// addChoiceExplanation is one short line, not a paragraph: the chooser's three
// buttons already say what they do.
func (a *appState) addChoiceExplanation(c addChoice) string {
	switch c {
	case addChoiceFile:
		return a.tr("Choose a video file from this device", "从本机选择视频文件")
	case addChoiceMedia:
		return a.tr("Enter an HTTP or HTTPS playlist or media file URL", "填写播放列表或媒体文件的 HTTP/HTTPS 地址")
	case addChoiceSite:
		return a.tr("Open the page in your browser, then send its media with the extension", "在浏览器打开网页后，用扩展发送媒体地址")
	}
	return ""
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
		if err := a.siteAddressProblem(a.addSiteText); err != nil {
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
	if _, err := url.Parse(address); err != nil {
		return refuse("%q is not an address a browser can open: %v", address, err)
	}
	display := stripSecrets(address)
	opener := a.openURL
	if opener == nil {
		opener = a.contextOpenURL
	}
	if opener == nil {
		return refuse("this build has no system browser to open %s", display)
	}
	opener(address, func(err error) {
		if err != nil {
			a.lastErr = safeWrap("opening "+display+" failed", err)
			a.report(commandResult{Command: command, At: time.Now(), Err: a.lastErr})
			a.invalidate()
			return
		}
		log.Printf("add-source: opened %s in the system browser", display)
	})
	result := commandResult{
		Command: command,
		At:      time.Now(),
		Message: fmt.Sprintf("opened %s in your browser; the MuxShift extension there can hand one media address back to this window", display),
	}
	a.report(result)
	return result, true
}

// addMediaAddress checks the address and starts the existing add command. Both
// the chooser's Add and Enter in the Sources address field go through it, so a
// person who types an address sees one set of rules.
func (a *appState) addMediaAddress(text string) (commandResult, bool) {
	if err := a.mediaAddressProblem(text); err != nil {
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
		return a.tr("Open again", "再次打开")
	}
	return a.tr("Add", "添加")
}

// addSourcePanel draws the chooser inside the dialog's panel. Three buttons,
// one line under them, one field: the panel explains itself by being short.
func (a *appState) addSourcePanel(c *ui.Context) {
	// The website choice needs a browser, and the frame's context owns the only
	// way to ask for one. Handing it over here keeps the choice usable from the
	// panel alone, while a test can still put its own opener in place instead.
	a.contextOpenURL = c.OpenURLThen
	ui.Column(c).Gap(spaceSm).MinWidth(412).MaxWidth(440).Children(func() {
		ui.Text(c, a.tr("Add a source", "添加来源")).FontSize(textTitle).FontWeight(600).
			TextColor(colorText)
		track := a.segmentTrack(c)
		track.WidthPercent(100).Children(func() {
			for _, choice := range []addChoice{addChoiceFile, addChoiceMedia, addChoiceSite} {
				ch := choice
				a.segmentedOption(c, "add-"+ch.key(), a.addChoiceLabel(ch), a.addChoice == ch, true, func() {
					a.addChoice = ch
					a.addNote, a.addNoteBad = "", false
				})
			}
		})
		if a.addChoice != addChoiceNone {
			ui.Text(c, a.addChoiceExplanation(a.addChoice)).FontSize(textMicro).
				FontWeight(400).TextColor(colorDim).Wrap()
		}
		switch a.addChoice {
		case addChoiceMedia:
			a.field(c, &a.importText).WidthPercent(100).
				Placeholder(`https://… .m3u8  or  C:\path\file.mp4`).
				Invalid(a.addNoteBad)
			ui.Text(c, a.tr("the media address or file path to read", "要读取的媒体地址或文件路径")).
				FontSize(textMicro).FontWeight(400).TextColor(colorDim)
		case addChoiceSite:
			a.field(c, &a.addSiteText).WidthPercent(100).
				Placeholder("https://example.com/watch?v=…").
				Invalid(a.addNoteBad)
			ui.Text(c, a.tr("the page to open in your browser", "要在浏览器里打开的页面")).
				FontSize(textMicro).FontWeight(400).TextColor(colorDim)
		}
		if a.addNote != "" {
			tone := colorTextDim
			if a.addNoteBad {
				tone = colorBad
			}
			ui.Text(c, a.addNote).FontSize(textMicro).FontWeight(400).TextColor(tone).Wrap()
		}
		ui.Row(c).Gap(spaceSm).WidthPercent(100).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1)
			a.secondaryButton(c, "add-cancel", a.tr("Cancel", "取消"), 88, true, func() {
				a.closeAddSource()
			})
			enabled := a.addChoice != addChoiceNone
			submit := a.primaryButton(c, "add-submit", a.addSubmitLabel(), 116, enabled, func() {
				a.submitAddSource()
			})
			switch a.addChoice {
			case addChoiceMedia:
				submit.Tooltip(a.tr("Add media", "添加媒体"))
			case addChoiceSite:
				submit.Tooltip(a.tr("Open in browser", "在浏览器打开"))
			case addChoiceFile:
				submit.Tooltip(a.tr("open the desktop file picker", "打开系统文件选择器"))
			default:
				submit.Tooltip(a.tr("Choose a source type first", "请先选择来源类型"))
			}
		})
	})
}

// siteAddressProblem says why an address cannot be opened as a website. A
// browser follows almost anything, so the checks are explicit: an http(s) page
// with a host, no space, and no credentials in the address.
func (a *appState) siteAddressProblem(raw string) error {
	text := strings.TrimSpace(raw)
	if text == "" {
		return errors.New(a.tr("type the address of the page to open", "先填要打开的网页地址"))
	}
	if strings.ContainsAny(text, " \t\r\n") {
		return errors.New(a.tr("the address contains a space; a space in a URL is written %20", "地址里有空格；URL 里的空格要写成 %20"))
	}
	u, err := url.Parse(text)
	if err != nil {
		return errors.New(a.tr("the address is not one a browser can open: ", "浏览器打不开这个地址：") + shortURI(text))
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return errors.New(a.tr("only http and https addresses can be opened", "只能打开 http 或 https 地址"))
	}
	if u.Host == "" {
		return errors.New(a.tr("the address names no host", "地址里没有主机名"))
	}
	if u.User != nil {
		return errors.New(a.tr("addresses with credentials are not opened", "带用户名的地址不会被打开"))
	}
	return nil
}

// mediaAddressProblem says why an address cannot be handed to the importer: an
// absolute local path, or an http(s) URL without credentials. The file's
// existence is the player's question, not this check's.
func (a *appState) mediaAddressProblem(raw string) error {
	text := strings.TrimSpace(raw)
	if text == "" {
		return errors.New(a.tr("type a media address or a file path first", "先填媒体地址或文件路径"))
	}
	if strings.ContainsAny(text, "\r\n\t") {
		return errors.New(a.tr("the address contains a control character", "地址里有控制字符"))
	}
	if filepath.IsAbs(text) {
		return nil
	}
	u, err := url.Parse(text)
	if err != nil {
		return errors.New(a.tr("this is neither an absolute path nor an http(s) address", "既不是绝对路径，也不是 http(s) 地址"))
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		if u.Host == "" {
			return errors.New(a.tr("the address names no host, so there is nothing to read", "地址里没有主机名，读不到东西"))
		}
		if u.User != nil {
			return errors.New(a.tr("addresses with credentials are not read", "带用户名的地址不会被读取"))
		}
		return nil
	}
	if u.Scheme != "" {
		return errors.New(a.tr("only http and https addresses can be read", "只能读取 http 或 https 地址"))
	}
	return errors.New(a.tr("a relative path cannot be read; use an absolute path or an http(s) address", "相对路径读不了；用绝对路径或 http(s) 地址"))
}

// siteAddressProblem and mediaAddressProblem keep the package-level entry points
// the handoff server and the address tests use. They ask the same questions as
// the methods above; a zero state has no language yet, so their messages stay
// English and are not shown to a person.
func siteAddressProblem(raw string) error { return (&appState{}).siteAddressProblem(raw) }

func mediaAddressProblem(raw string) error { return (&appState{}).mediaAddressProblem(raw) }
