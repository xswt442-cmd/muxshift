// The session half of the desktop application: Core holds what the user
// configured, and this file maps it onto the players MyGo actually opened.
//
// The mapping runs one way. A scene says which video is shown and which audio
// tracks are heard at which level; applying it sets the players' mute and
// volume. The players' positions and buffering are never written back into the
// session, because a Session holds configuration, not playback state.
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
	"github.com/xswt442-cmd/muxshift/core/hls"
)

// item is one media source, the Core objects that describe it, and the player
// opened for it.
type item struct {
	uri string

	source core.Source
	video  core.TrackID
	audio  core.TrackID

	player  *ui.VideoPlayer
	openErr error

	// previous is the player this item opened before the current one, kept
	// alive and drawn until the current one has a picture. See reopenPlayer.
	previous *ui.VideoPlayer

	// handover owns previous while a task waits for the replacement's picture.
	// It moves to the newer task on a reload, so a burst of reloads releases
	// each player exactly once. See handOver.
	handover *handover

	// shown is whether the window draws this player right now. It changes
	// before the player is closed, never after.
	shown bool

	// generation counts how many players this item has opened. A wake task
	// carries the generation it was started for, so a task left over from an
	// earlier player cannot seek the current one or a closed one.
	generation int

	// volume is the level this source's audio track gets when the scene plays
	// it, kept here so the window's buttons can raise and lower it.
	volume float64
}

// appState is a Core session plus the players it is mapped onto.
type appState struct {
	session *core.Session
	scene   core.SceneID
	output  core.OutputID
	items   []*item
	lastErr error

	// syncNote is the outcome of the last sync command, so a refused seek stays
	// visible in the window instead of only in the log.
	syncNote string

	// sync is the shared event clock and the per-track offsets the panel
	// edits; see sync.go.

	// audible holds the audio tracks a person has explicitly allowed to be heard
	// while their place on the shared timeline is still unknown.
	audible map[core.TrackID]bool
	sync    *syncState

	// frames receives one token for every frame the window builds, which is
	// what says a player dropped from the view is no longer being drawn.
	frames chan struct{}

	// pending holds players that were removed while no other player was alive.
	// See close for why they cannot be released at that moment.
	pendingMu sync.Mutex
	pending   []*ui.VideoPlayer
}

// newAppState builds a session with one scene per given source set, opens a
// player for every source, and applies the first scene.
func newAppState(uris []string) (*appState, error) {
	session := core.NewSession("desktop")
	scene, err := session.AddScene("main")
	if err != nil {
		return nil, err
	}
	output, err := session.AddOutput(core.OutputDesktopWindow, scene.ID)
	if err != nil {
		return nil, err
	}
	app := &appState{session: session, scene: scene.ID, output: output.ID, frames: make(chan struct{}, 1), sync: newSyncState(), audible: make(map[core.TrackID]bool)}
	for _, uri := range uris {
		it, err := app.add(uri)
		if err != nil {
			// One unreachable source does not take the session down: the item
			// keeps its open error so the window can report it.
			app.lastErr = err
		}
		app.items = append(app.items, it)
	}
	// The first source that opened is what the window shows and hears until a
	// button says otherwise. Without a video track in the scene nothing is
	// drawn, and a player that is not drawn does not advance in this MyGo
	// build, so this is not cosmetic.
	for _, it := range app.items {
		if it.video != "" {
			if _, err := session.SetSceneVideo(app.scene, it.video); err != nil {
				app.lastErr = err
			}
			break
		}
	}
	for _, it := range app.items {
		if it.audio != "" {
			if _, err := session.SetSceneAudio(app.scene, it.audio, 1, false); err != nil {
				app.lastErr = err
			}
			break
		}
	}
	app.apply()
	return app, nil
}

// add registers one source and the tracks its player exposes, then opens the
// player.
func (a *appState) add(uri string) (*item, error) {
	it := &item{uri: uri, volume: 1}
	if isNetworkAddress(uri) {
		if err := a.addProbed(it); err != nil {
			return it, err
		}
	} else if err := a.addLocal(it); err != nil {
		return it, err
	}
	player, err := openPlayer(uri)
	if err != nil {
		it.openErr = err
		return it, err
	}
	it.player = player
	it.generation++
	wakeNetworkPlayer(it.uri, player, a.generationIs(it, it.generation))
	a.closePendingWhenReady()
	return it, nil
}

// generationIs returns a function that reports whether it still owns the player
// of the given generation. A wake task left over from an earlier player stops
// asking as soon as the item opened a new one, so it cannot seek a player that
// was closed or take over one that replaced it.
func (a *appState) generationIs(it *item, generation int) func() bool {
	return func() bool {
		var current bool
		mygoRunOnMain(func() { current = it.generation == generation && it.player != nil })
		return current
	}
}

// addProbed reads an HLS address with core/hls, so the tracks in the session
// are the ones the stream really declares.
func (a *appState) addProbed(it *item) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := hls.Probe(ctx, it.uri, hls.Options{})
	if err != nil {
		it.openErr = err
		return err
	}
	source, attachments, err := stream.Attach(a.session)
	if err != nil {
		it.openErr = err
		return err
	}
	it.source = source
	for _, attachment := range attachments {
		switch attachment.Track.Kind {
		case core.MediaVideo:
			if it.video == "" {
				it.video = attachment.Track.ID
			}
		case core.MediaAudio:
			if it.audio == "" {
				it.audio = attachment.Track.ID
			}
		}
	}
	return nil
}

// addLocal registers a local file. MyGo does not enumerate the tracks of a
// file it opens, so the session records the one video and one audio stream the
// player takes from it, and says so in the labels.
func (a *appState) addLocal(it *item) error {
	source, err := a.session.AddSource(it.uri)
	if err != nil {
		it.openErr = err
		return err
	}
	it.source = source
	videoTrack, err := a.session.AddTrack(source.ID, core.MediaVideo, "video (player does not enumerate tracks)")
	if err != nil {
		return err
	}
	audioTrack, err := a.session.AddTrack(source.ID, core.MediaAudio, "audio (player does not enumerate tracks)")
	if err != nil {
		return err
	}
	it.video = videoTrack.ID
	it.audio = audioTrack.ID
	return nil
}

// apply maps the current scene onto the players: the video tracks in the scene
// are drawn, everything else is hidden, and each source's player takes the
// level the scene gives its audio track, muted when it is not in the scene.
func (a *appState) apply() {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		a.lastErr = err
		return
	}
	shown := make(map[core.TrackID]bool, len(scene.Video))
	for _, track := range scene.Video {
		shown[track] = true
	}
	levels := make(map[core.TrackID]core.AudioLevel, len(scene.Audio))
	for _, level := range scene.Audio {
		levels[level.Track] = level
	}
	for _, it := range a.items {
		if it.player == nil {
			continue
		}
		it.shown = shown[it.video]
		level, ok := levels[it.audio]
		if !ok {
			it.player.SetMuted(true)
			continue
		}
		// A source whose place on the shared timeline is unknown makes no claim
		// about which moment of the match it is playing, so its sound stays off
		// until someone who can see both pictures says it is safe.
		if !level.Muted && !a.audible[it.audio] && !a.aligned(it) {
			it.player.SetMuted(true)
			continue
		}
		it.player.SetMuted(level.Muted)
		if !level.Muted {
			it.player.SetVolume(level.Volume)
		}
	}
}

// showVideo makes one source's video the scene's video, keeping the audio
// levels as they are.
func (a *appState) showVideo(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.video == "" {
		return fmt.Errorf("source %d has no video track", index+1)
	}
	if _, err := a.session.SetSceneVideo(a.scene, it.video); err != nil {
		return err
	}
	a.apply()
	a.wakeShown(it)
	return nil
}

// wakeShown asks one seek of a network player that only became visible now. A
// network source that is shown for the first time after it was opened does not
// deliver its first picture on its own with this build of MyGo, and the seek is
// the only signal observed to make it start.
func (a *appState) wakeShown(it *item) {
	if it == nil || it.player == nil || !needsWake(it.uri) {
		return
	}
	if it.player.Ready() || it.player.Err() != nil {
		return
	}
	wakeNetworkPlayer(it.uri, it.player, a.generationIs(it, it.generation))
}

// hearAudio makes the scene play one source's audio at the given level and mute
// every other source, so what is heard is unambiguous.
func (a *appState) hearAudio(index int, volume float64) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.audio == "" {
		return fmt.Errorf("source %d has no audio track", index+1)
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return err
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio {
			continue
		}
		if _, err := a.session.RemoveSceneAudio(a.scene, level.Track); err != nil {
			return err
		}
	}
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, volume, false); err != nil {
		return err
	}
	a.apply()
	return nil
}

// setVolume changes the level of one source's audio track inside the scene.
func (a *appState) setVolume(index int, volume float64) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if it.audio == "" {
		return fmt.Errorf("source %d has no audio track", index+1)
	}
	if _, err := a.session.SetSceneAudio(a.scene, it.audio, volume, false); err != nil {
		return err
	}
	a.apply()
	return nil
}

// hearBoth makes the scene play two sources' audio together, each at its own
// level, which is what independent volume control means here.
func (a *appState) hearBoth(first, second int, firstVolume, secondVolume float64) error {
	a1, err := a.at(first)
	if err != nil {
		return err
	}
	a2, err := a.at(second)
	if err != nil {
		return err
	}
	if a1.audio == "" || a2.audio == "" {
		return fmt.Errorf("source %d or %d has no audio track", first+1, second+1)
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return err
	}
	for _, level := range scene.Audio {
		if level.Track == a1.audio || level.Track == a2.audio {
			continue
		}
		if _, err := a.session.RemoveSceneAudio(a.scene, level.Track); err != nil {
			return err
		}
	}
	if _, err := a.session.SetSceneAudio(a.scene, a1.audio, firstVolume, false); err != nil {
		return err
	}
	if _, err := a.session.SetSceneAudio(a.scene, a2.audio, secondVolume, false); err != nil {
		return err
	}
	a.apply()
	return nil
}

// remove deletes a source from the session and closes its player. The session
// drops its tracks and every scene reference; the player is closed only after
// the window stopped drawing it.
func (a *appState) remove(index int) error {
	it, err := a.at(index)
	if err != nil {
		return err
	}
	if err := a.session.RemoveSource(it.source.ID); err != nil {
		return err
	}
	a.close(it)
	a.items = append(a.items[:index], a.items[index+1:]...)
	a.apply()
	return nil
}

// at is the item at a zero-based index.
func (a *appState) at(index int) (*item, error) {
	if index < 0 || index >= len(a.items) {
		return nil, fmt.Errorf("source %d: %w", index+1, core.ErrNotFound)
	}
	return a.items[index], nil
}

// close stops drawing a player and then closes it.
//
// The order matters: a player closed while a frame still draws it crashes the
// process inside Media Foundation, so the view drops it first and the close
// waits for frames the window really built, not for a fixed sleep. The drop and
// the close both run on the drawing thread, and the player pointer is cleared
// there too, so the view cannot read a half-released player.
//
// A player that is the last live one in the window is not closed here. In this
// MyGo build, once the last video player of a window is closed, a player opened
// afterwards reports its duration but never gets a picture or moves its
// position: the window keeps a Media Foundation resource it does not rebuild.
// The smallest reproduction (testplace/poc/onevid -reopen) fails the same way,
// and opening the new player before closing the old one works. So the last one
// is paused and kept until another player is ready, and the pending close runs
// then.
func (a *appState) close(it *item) {
	var kept *handover
	var previous *ui.VideoPlayer
	mygoRunOnMain(func() {
		kept = it.handover
		previous = it.previous
		it.handover = nil
		it.previous = nil
	})
	switch {
	case kept != nil:
		// The handover that was waiting owns the player it kept drawn; a close
		// here takes that player over instead of closing it twice later.
		a.releasePlayer(kept.take())
	case previous != nil:
		a.releasePlayer(previous)
	}
	if it.player == nil {
		return
	}
	var player *ui.VideoPlayer
	mygoRunOnMain(func() {
		it.shown = false
		it.generation++
		player = it.player
		it.player = nil
	})
	a.waitFrames(3)
	if a.livePlayers() > 0 {
		mygoRunOnMain(func() { player.Close() })
		return
	}
	mygoRunOnMain(func() { player.Pause() })
	a.pendingMu.Lock()
	a.pending = append(a.pending, player)
	a.pendingMu.Unlock()
	a.closePendingWhenReady()
}

// livePlayers counts players still open for a source in this session.
func (a *appState) livePlayers() int {
	n := 0
	mygoRunOnMain(func() {
		for _, it := range a.items {
			if it.player != nil {
				n++
			}
		}
	})
	return n
}

// closePendingWhenReady releases the players kept because they were the last
// live one, as soon as another player actually has a picture.
func (a *appState) closePendingWhenReady() {
	a.pendingMu.Lock()
	waiting := len(a.pending)
	a.pendingMu.Unlock()
	if waiting == 0 || a.livePlayers() == 0 {
		return
	}
	go func() {
		for i := 0; i < 40; i++ {
			ready := false
			mygoRunOnMain(func() {
				for _, it := range a.items {
					if it.player != nil && it.player.Ready() {
						ready = true
					}
				}
			})
			if ready {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		a.pendingMu.Lock()
		wait := a.pending
		a.pending = nil
		a.pendingMu.Unlock()
		if len(wait) == 0 {
			return
		}
		mygoRunOnMain(func() {
			for _, player := range wait {
				player.Close()
			}
		})
	}()
}

// waitFrames blocks until the window built n frames after this call, or until
// it stops building them.
func (a *appState) waitFrames(n int) {
	for i := 0; i < n; i++ {
		select {
		case <-a.frames:
		case <-time.After(2 * time.Second):
			return
		}
	}
}

// signalFrame records that the window built a frame, so a close that just
// dropped a player can wait for the view to stop drawing it.
func (a *appState) signalFrame() {
	select {
	case a.frames <- struct{}{}:
	default:
	}
}

// release closes every player and the session.
func (a *appState) release() {
	for _, it := range a.items {
		a.close(it)
	}
	a.pendingMu.Lock()
	wait := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	if len(wait) > 0 {
		mygoRunOnMain(func() {
			for _, player := range wait {
				player.Close()
			}
		})
	}
	if a.session != nil && !a.session.Closed() {
		a.lastErr = a.session.Close()
	}
}

// sceneLabel describes the scene as the window shows it.
func (a *appState) sceneLabel() string {
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		return "scene: " + err.Error()
	}
	var video string
	for _, it := range a.items {
		if it.video != "" && containsTrack(scene.Video, it.video) {
			video = it.uri
			break
		}
	}
	audio := make([]string, 0, len(scene.Audio))
	for _, level := range scene.Audio {
		label := shortURI(trackURI(a.items, level.Track))
		audio = append(audio, fmt.Sprintf("%s %.2f", label, level.Volume))
	}
	if video == "" {
		video = "none"
	}
	if len(audio) == 0 {
		return "video " + shortURI(video) + " 路 silent"
	}
	return "video " + shortURI(video) + " 路 audio " + strings.Join(audio, " + ")
}

// containsTrack reports whether the track is in the list.
func containsTrack(tracks []core.TrackID, want core.TrackID) bool {
	for _, track := range tracks {
		if track == want {
			return true
		}
	}
	return false
}

// trackURI is the address of the source that owns an audio track.
func trackURI(items []*item, track core.TrackID) string {
	for _, it := range items {
		if it.audio == track {
			return it.uri
		}
	}
	return ""
}

// shortURI is the last path element, with the directory before it when that
// element is a playlist, so a status line stays readable and still tells two
// index.m3u8 addresses apart.
func shortURI(uri string) string {
	if uri == "" {
		return "none"
	}
	trimmed := strings.TrimSuffix(uri, "/")
	parts := strings.Split(trimmed, "/")
	last := parts[len(parts)-1]
	if strings.HasPrefix(last, "index.") && len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + last
	}
	return last
}
