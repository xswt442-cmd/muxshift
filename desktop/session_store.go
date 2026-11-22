// Session persistence: the scenes, sources and levels a person arranged are
// kept in a JSON snapshot beside the settings file, so a restart reopens the
// same work instead of only the -media command line. The snapshot is a plain
// struct with json tags; it is separate from the settings file, whose shape and
// strict validation must not change.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

// storedSession is one run's arrangement, as written to disk.
type storedSession struct {
	ActiveScene string         `json:"activeScene,omitempty"`
	Scenes      []storedScene  `json:"scenes,omitempty"`
	Sources     []storedSource `json:"sources,omitempty"`
}

// storedScene is one scene's name and the sources it shows and hears. The track
// ids are not stable across a restart, so each entry names the source instead.
type storedScene struct {
	ID    string        `json:"id,omitempty"`
	Name  string        `json:"name,omitempty"`
	Video []string      `json:"video,omitempty"`
	Audio []storedAudio `json:"audio,omitempty"`
}

// storedAudio is one audio source's level in a scene.
type storedAudio struct {
	Source string  `json:"source,omitempty"`
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted,omitempty"`
}

// storedSource is one source: how to reopen it and how it was set up. The URI
// is the address already in memory; no credentials are stored.
type storedSource struct {
	ID     string  `json:"id,omitempty"`
	URI    string  `json:"uri,omitempty"`
	Name   string  `json:"name,omitempty"`
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted,omitempty"`
	Shown  bool    `json:"shown,omitempty"`
}

// sessionStorePath puts the snapshot beside the settings file, so both travel
// together. An empty settings path means nowhere to write.
func sessionStorePath(settingsPath string) string {
	if settingsPath == "" {
		return ""
	}
	return settingsPath + ".session.json"
}

// captureSessionStore reads the live session into a snapshot. Track ids are
// translated back to the source that owns them, because a track id means
// nothing after a restart while a source id does.
func (a *appState) captureSessionStore() storedSession {
	snap := storedSession{ActiveScene: string(a.scene)}
	owner := make(map[core.TrackID]string, len(a.items)*2)
	for _, it := range a.items {
		if it.source.ID == "" {
			continue
		}
		if it.video != "" {
			owner[it.video] = string(it.source.ID)
		}
		if it.audio != "" {
			owner[it.audio] = string(it.source.ID)
		}
	}
	for _, it := range a.items {
		snap.Sources = append(snap.Sources, storedSource{
			ID:     string(it.source.ID),
			URI:    it.uri,
			Name:   it.source.Name,
			Volume: it.volume,
			Muted:  it.muted,
			Shown:  it.shown,
		})
	}
	for _, scene := range a.session.Scenes() {
		stored := storedScene{ID: string(scene.ID), Name: scene.Name}
		for _, track := range scene.Video {
			if source := owner[track]; source != "" {
				stored.Video = append(stored.Video, source)
			}
		}
		for _, level := range scene.Audio {
			source := owner[level.Track]
			if source == "" {
				continue
			}
			stored.Audio = append(stored.Audio, storedAudio{
				Source: source,
				Volume: level.Volume,
				Muted:  level.Muted,
			})
		}
		snap.Scenes = append(snap.Scenes, stored)
	}
	return snap
}

// saveSession writes the current arrangement. A missing path (nowhere to write)
// and a write error are logged, never fatal: losing persistence must not take
// the running session down.
func (a *appState) saveSession() {
	if a.session == nil || a.sessionPath == "" {
		return
	}
	if err := saveSessionStore(a.sessionPath, a.captureSessionStore()); err != nil {
		log.Printf("session: could not save %s (%v)", a.sessionPath, err)
	}
}

// saveSessionStore writes the snapshot atomically: a temp file in the same
// directory is written, flushed and renamed over the target, so a crash cannot
// leave a half file that the next start reads as a session.
func saveSessionStore(path string, snap storedSession) error {
	if path == "" {
		return errors.New("there is no configuration directory to save the session into")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".session-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	var renameErr error
	for attempt := 0; attempt < 10; attempt++ {
		renameErr = os.Rename(name, path)
		if renameErr == nil {
			return nil
		}
		if !errors.Is(renameErr, fs.ErrPermission) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Remove(name)
	return renameErr
}

// loadSessionStore reads a snapshot. A missing file is a first run. A file that
// cannot be read or decoded is reported and replaced by an empty snapshot, so a
// corrupt file starts the app empty instead of crashing it. Unknown and absent
// fields are ignored rather than refused.
func loadSessionStore(path string) (storedSession, string) {
	var snap storedSession
	if path == "" {
		return snap, ""
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return snap, ""
	}
	if err != nil {
		return snap, fmt.Sprintf("could not read %s (%v); starting with an empty session", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&snap); err != nil {
		return storedSession{}, fmt.Sprintf("%s is not a session file (%v); starting with an empty session", path, err)
	}
	return sanitizeSessionStore(snap), ""
}

// sanitizeSessionStore drops entries that cannot be reopened and clamps levels,
// so a hand-edited or older file cannot feed the session impossible values.
func sanitizeSessionStore(snap storedSession) storedSession {
	out := storedSession{ActiveScene: snap.ActiveScene}
	for _, src := range snap.Sources {
		if strings.TrimSpace(src.URI) == "" {
			continue
		}
		src.Volume = clampVolume(src.Volume)
		out.Sources = append(out.Sources, src)
	}
	for _, scene := range snap.Scenes {
		if strings.TrimSpace(scene.ID) == "" {
			continue
		}
		for i := range scene.Audio {
			scene.Audio[i].Volume = clampVolume(scene.Audio[i].Volume)
		}
		out.Scenes = append(out.Scenes, scene)
	}
	return out
}

// clampVolume keeps a level inside the 0..1 range Core accepts.
func clampVolume(volume float64) float64 {
	if volume < 0 {
		return 0
	}
	if volume > 1 {
		return 1
	}
	return volume
}

// newAppStateFromStore rebuilds the session a previous run saved. Sources are
// reopened through the same path the command line uses; one that fails keeps its
// open error and the rest of the session still comes back. An empty snapshot is
// just a first start.
func newAppStateFromStore(loaded settings, snap storedSession) (*appState, error) {
	if len(snap.Scenes) == 0 {
		return newAppStateWithSettings(nil, loaded)
	}
	session := core.NewSession("desktop")
	app := newAppShell(session, "", "", loaded)

	byID := make(map[string]*item, len(snap.Sources))
	for _, stored := range snap.Sources {
		it, err := app.addFrom(stored.URI, originAdded)
		if err != nil {
			// One source that cannot reopen must not stop the start: the item
			// keeps its open error so the window can report it.
			app.lastErr = safeOpenError(err)
		}
		it.volume = clampVolume(stored.Volume)
		it.muted = stored.Muted
		it.shown = stored.Shown
		if stored.Name != "" && it.source.ID != "" {
			if rerr := session.RenameSource(it.source.ID, stored.Name); rerr != nil {
				app.lastErr = safeOpenError(rerr)
			} else if src, rerr := session.Source(it.source.ID); rerr == nil {
				it.source = src
			}
		}
		app.items = append(app.items, it)
		if stored.ID != "" {
			byID[stored.ID] = it
		}
	}

	var first core.SceneID
	sceneIDs := make(map[string]core.SceneID, len(snap.Scenes))
	var restoreErr error
	for _, stored := range snap.Scenes {
		scene, err := session.AddScene(stored.Name)
		if err != nil {
			return nil, err
		}
		if first == "" {
			first = scene.ID
		}
		if stored.ID != "" {
			sceneIDs[stored.ID] = scene.ID
		}
		video := make([]core.TrackID, 0, len(stored.Video))
		for _, source := range stored.Video {
			if it := byID[source]; it != nil && it.video != "" {
				video = append(video, it.video)
			}
		}
		if len(video) > 0 {
			if _, err := session.SetSceneVideo(scene.ID, video...); err != nil {
				restoreErr = err
			}
		}
		for _, level := range stored.Audio {
			it := byID[level.Source]
			if it == nil || it.audio == "" {
				continue
			}
			if _, err := session.SetSceneAudio(scene.ID, it.audio, clampVolume(level.Volume), level.Muted); err != nil {
				restoreErr = err
			}
		}
	}
	if restoreErr != nil {
		app.lastErr = safeOpenError(restoreErr)
	}
	app.scene = sceneIDs[snap.ActiveScene]
	if app.scene == "" {
		app.scene = first
	}
	output, err := session.AddOutput(core.OutputDesktopWindow, app.scene)
	if err != nil {
		return nil, err
	}
	app.output = output.ID
	app.apply()
	return app, nil
}
