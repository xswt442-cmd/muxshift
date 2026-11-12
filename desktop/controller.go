// The command and snapshot boundary of the desktop application.
//
// The window's buttons used to call appState methods directly, which meant a
// click had no single entry point, no record of what it applied to, and no way
// to read the result back without parsing the status line. This file gives
// every real command one door — runCommand — and one copy of the state behind
// it — snapshot. Both are ordinary Go values, so a scenario can drive them and
// a test can check them without a window.
//
// The boundary does not replace Core: a command still encodes its change into
// the Core session and applies it to the players, exactly as the methods it
// wraps did. It records the source and the player generation it was given, so
// a failure names which source failed and which player it had at the time.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// commandKind names the operations the window offers.
type commandKind string

const (
	commandShow    commandKind = "show"
	commandHear    commandKind = "hear"
	commandAllow   commandKind = "allow-audio"
	commandVolume  commandKind = "volume"
	commandNudge   commandKind = "nudge"
	commandPause   commandKind = "pause-all"
	commandPlay    commandKind = "play-all"
	commandReload  commandKind = "reload"
	commandDVRInfo commandKind = "dvr-info"
	commandAdd     commandKind = "add-source"
	commandRemove  commandKind = "remove-source"
	commandRetry   commandKind = "retry"
	// commandCancelImport stops a read that has not landed yet. It is a real
	// command, not a disabled placeholder: the context it cancels is the one
	// the probe is waiting on.
	commandCancelImport commandKind = "cancel-import"
	// commandMute is a source's own mute, commandSolo the listening overlay that
	// keeps one source audible, and commandMasterMute the overlay that silences
	// everything. None of them edits the scene's routing.
	commandMute       commandKind = "mute"
	commandSolo       commandKind = "solo"
	commandMasterMute commandKind = "master-mute"
)

// UnknownSourceError names a source that is not in the session any more, which
// is what a command carries when the window's list is stale.
var UnknownSourceError = errors.New("no source with that identifier")

// command is what a button sends. Source is the Core identifier, not a row
// number: rows move when a source is removed, identifiers do not.
type command struct {
	Kind   commandKind
	Source core.SourceID
	Volume float64
	Delta  time.Duration
	// URI is the address an add command reads; the other commands leave it empty.
	URI string
}

// commandResult is kept after the command returns, so the window can show the
// outcome of the last attempt and a scenario can assert on it.
type commandResult struct {
	Command    command
	Generation int
	At         time.Time
	Message    string
	Err        error
}

// String is one line for the log and the window.
func (r commandResult) String() string {
	state := "ok"
	if r.Err != nil {
		state = "failed: " + r.Err.Error()
	}
	line := string(r.Command.Kind)
	if r.Command.Source != "" {
		line += " source=" + string(r.Command.Source) + " generation=" + strconv.Itoa(r.Generation)
	}
	line += " " + state
	if r.Message != "" {
		line += " · " + r.Message
	}
	return line
}

// itemBySource finds the row a command names. Rows are looked up by identifier
// because a removal renumbers the list under the window's feet.
func (a *appState) itemBySource(id core.SourceID) (*item, int, error) {
	for index, it := range a.items {
		if it.source.ID == id {
			return it, index, nil
		}
	}
	return nil, -1, fmt.Errorf("%w: %s", UnknownSourceError, id)
}

// runCommand is the one door a button uses. It must be called on the drawing
// thread: it touches players and the window's items.
func (a *appState) runCommand(cmd command) commandResult {
	result := commandResult{Command: cmd, At: time.Now()}
	finish := func(err error, message string) commandResult {
		result.Err = safeOpenError(err)
		result.Message = message
		a.lastCommand = result
		if err != nil {
			a.lastErr = safeOpenError(err)
		}
		return result
	}

	switch cmd.Kind {
	case commandPause, commandPlay:
		var problems []string
		if cmd.Kind == commandPause {
			problems = a.pauseAll()
		} else {
			problems = a.playAll()
		}
		if len(problems) > 0 {
			return finish(errors.New(problems[0]), fmt.Sprintf("%d member(s) did not accept it", len(problems)))
		}
		return finish(nil, "every member accepted it")

	case commandReload:
		it, _, err := a.itemBySource(cmd.Source)
		if err != nil {
			return finish(err, "")
		}
		if it.player == nil {
			return finish(fmt.Errorf("%s has no player to reopen", a.name(it)), "")
		}
		result.Generation = it.generation
		a.reopenPlayer(it)
		return finish(nil, "asked for a new player; the old one stays until the new one has a picture")

	case commandShow, commandHear, commandAllow, commandVolume, commandNudge, commandMute, commandSolo:
		it, index, err := a.itemBySource(cmd.Source)
		if err != nil {
			return finish(err, "")
		}
		result.Generation = it.generation
		switch cmd.Kind {
		case commandShow:
			err = a.showVideo(index)
		case commandHear:
			err = a.hearAudio(index, it.volume)
		case commandAllow:
			err = a.allowAudio(index)
		case commandVolume:
			err = a.setVolume(index, cmd.Volume)
		case commandNudge:
			var note string
			note, err = a.nudge(index, cmd.Delta)
			result.Message = note
		case commandMute:
			result.Message, err = a.toggleMute(index)
		case commandSolo:
			result.Message, err = a.toggleSolo(index)
		}
		return finish(err, result.Message)

	case commandMasterMute:
		result.Message = a.toggleMasterMute()
		return finish(nil, result.Message)

	case commandAdd:
		uri := strings.TrimSpace(cmd.URI)
		if uri == "" {
			return finish(errors.New("nothing to import: type an address or a file path first"), "")
		}
		if a.importing != "" {
			return finish(fmt.Errorf("%s is still being read", shortURI(a.importing)), "")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		a.importToken++
		token := a.importToken
		a.importing = uri
		a.importText = ""
		a.importSince = time.Now()
		a.importCancel = cancel
		a.importDone = make(chan struct{})
		go a.tickImport(a.importDone)
		go a.importSource(ctx, uri, token)
		return finish(nil, fmt.Sprintf("reading %s in the background; Cancel stops it", shortURI(uri)))

	case commandCancelImport:
		if a.importing == "" {
			return finish(errors.New("nothing is being read right now"), "")
		}
		uri := a.importing
		a.importToken++
		if a.importCancel != nil {
			a.importCancel()
			a.importCancel = nil
		}
		a.closeImportTicker()
		a.importing = ""
		a.importText = ""
		result.Message = fmt.Sprintf("cancelled reading %s; nothing was added", shortURI(uri))
		a.lastCommand = result
		a.report(result)
		a.lastErr = nil
		return result

	case commandRemove:
		it, index, err := a.itemBySource(cmd.Source)
		if err != nil {
			return finish(err, "")
		}
		result.Generation = it.generation
		name := a.name(it)
		if err := a.remove(index); err != nil {
			return finish(err, "")
		}
		if a.selected == cmd.Source {
			a.selected = ""
		}
		return finish(nil, fmt.Sprintf("removed %s; %d source(s) left", name, len(a.items)))

	case commandRetry:
		it, _, err := a.itemBySource(cmd.Source)
		if err != nil {
			return finish(err, "")
		}
		result.Generation = it.generation
		if it.openErr == nil && it.player != nil {
			return finish(fmt.Errorf("%s has not failed, there is nothing to retry", a.name(it)), "")
		}
		it.openErr = nil
		a.reopenPlayer(it)
		if it.openErr != nil {
			return finish(it.openErr, "the retry opened no player")
		}
		return finish(nil, "opened a new player for "+a.name(it))

	default:
		return finish(fmt.Errorf("unknown command %q", cmd.Kind), "")
	}
}

// shortcutCommand runs a command against the selected source, which is what the
// Ctrl shortcuts do. It takes the same door as a click, so the log and the
// snapshot record a key exactly as they record a press; with nothing selected it
// says so instead of guessing which source was meant.
func (a *appState) shortcutCommand(kind commandKind) {
	// P18.1: a key press is recorded with the focus that let it through, so a
	// reader can tell a shortcut from a keystroke that typed into a field. The
	// window binds no shortcut while a text field has the focus; the count
	// exists to prove that, not to be a feature.
	a.shortcutFires++
	if a.keysTyping {
		a.firesWhileTyping++
		log.Printf("focus: %s fired while a text field had the focus", kind)
	}
	log.Printf("focus: shortcut %s fired (typing %v, workspace %s)", kind, a.keysTyping, a.workspace.title())
	it, _, ok := a.selectedSource()
	if !ok {
		a.lastErr = errors.New("no source is selected: click a row in Sources, then press the key again")
		result := commandResult{Command: command{Kind: kind}, At: time.Now(), Err: a.lastErr, Message: "nothing selected"}
		a.lastCommand = result
		a.report(result)
		return
	}
	result := a.runCommand(command{Kind: kind, Source: it.source.ID})
	a.report(result)
}

// trackSnapshot is one audio track the scene plays, as copy.
type trackSnapshot struct {
	Track  core.TrackID
	Source core.SourceID
	Volume float64
	Muted  bool
}

// sourceSnapshot is one source and the player behind it, as copy: reading the
// snapshot never touches a live player or a Core object again.
type sourceSnapshot struct {
	ID         core.SourceID
	URI        string
	Video      core.TrackID
	Audio      core.TrackID
	Generation int
	Shown      bool
	Player     bool
	Ready      bool
	Playing    bool
	Buffering  bool
	Muted      bool
	Volume     float64
	Position   time.Duration
	Duration   time.Duration
	Aligned    bool
	Offset     time.Duration
	OpenErr    string
}

// sessionSnapshot is the whole readable state behind the window.
type sessionSnapshot struct {
	Name        string
	Scene       core.SceneID
	Picture     []core.TrackID
	Sound       []trackSnapshot
	Sources     []sourceSnapshot
	Event       time.Duration
	Playing     bool
	Group       string
	GroupTracks int
	LastCommand string

	// Seek is the last measured seek, in media time; SeekSource names the source
	// it belongs to so the numbers cannot be read as the shared clock.
	SeekSource    core.SourceID
	SeekRequested time.Duration
	SeekActual    time.Duration
	SeekResidual  time.Duration
	SeekWindow    string
	SeekWallClock string
}

// snapshot copies the state the window shows. It must be called on the drawing
// thread because it reads players; snapshotOnMain is the wrapper for a caller
// on another goroutine.
func (a *appState) snapshot() sessionSnapshot {
	out := sessionSnapshot{Name: a.session.Name(), Scene: a.scene}
	if scene, err := a.session.Scene(a.scene); err == nil {
		out.Picture = append(out.Picture, scene.Video...)
		for _, level := range scene.Audio {
			out.Sound = append(out.Sound, trackSnapshot{
				Track:  level.Track,
				Source: a.sourceOfTrack(level.Track),
				Volume: level.Volume,
				Muted:  level.Muted,
			})
		}
	}
	for _, it := range a.items {
		entry := sourceSnapshot{
			ID:         it.source.ID,
			URI:        it.uri,
			Video:      it.video,
			Audio:      it.audio,
			Generation: it.generation,
			Shown:      it.shown,
			Volume:     it.volume,
			OpenErr:    errorString(it.openErr),
		}
		if track := it.audio; track != "" {
			entry.Aligned = a.sync.timeline.Aligned(track)
			if shift, ok := a.sync.timeline.Shift(track); ok {
				entry.Offset = shift.Offset
			}
		}
		if it.player != nil {
			state := readPlayer(rawPlayer(it.player))
			entry.Player = true
			entry.Ready = state.Ready
			entry.Playing = state.Playing
			entry.Buffering = state.Buf
			entry.Muted = state.Muted
			entry.Volume = state.Volume
			entry.Position = state.Pos
			entry.Duration = state.Dur
		}
		out.Sources = append(out.Sources, entry)
	}
	out.Event = a.sync.playhead.Event()
	out.Playing = a.sync.playhead.Playing()
	if group, ok := a.existingGroup(); ok {
		out.Group = string(group.ID)
		out.GroupTracks = len(group.Tracks)
	}
	out.LastCommand = a.lastCommand.String()
	out.SeekSource = a.lastSeekSource
	out.SeekRequested = a.lastSeek.Requested
	out.SeekActual = a.lastSeek.Actual
	out.SeekResidual = a.lastSeek.Residual
	out.SeekWindow = a.lastSeek.Window.String()
	if !a.lastSeek.RequestedAt.IsZero() {
		out.SeekWallClock = a.lastSeek.RequestedAt.Format("15:04:05.000")
	}
	if a.lastCommand.Command.Kind == "" {
		out.LastCommand = "none yet"
	}
	return out
}

// report writes a command's outcome where a scenario and a reader of the log
// can find it: one line naming the command, the source, the player generation
// and the failure, if any.
func (a *appState) report(result commandResult) {
	log.Printf("command: %s", result.String())
}

// readSnapshotOnMain reads the snapshot from a goroutine that does not draw.
func (a *appState) readSnapshotOnMain() sessionSnapshot {
	var out sessionSnapshot
	mygoRunOnMain(func() { out = a.snapshot() })
	return out
}

// sourceOfTrack is the source a track belongs to, or "" when it is not in the
// session any more.
func (a *appState) sourceOfTrack(track core.TrackID) core.SourceID {
	for _, it := range a.items {
		if it.video == track || it.audio == track {
			return it.source.ID
		}
	}
	return ""
}
