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
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/xswt442-cmd/muxshift/core"
)

var traceSessionSaveTiming = os.Getenv("MUXSHIFT_SESSION_SAVE_TIMING") == "1"

// storedSession is one run's arrangement, as written to disk.
type storedSession struct {
	ActiveScene string             `json:"activeScene,omitempty"`
	Layout      string             `json:"layout,omitempty"`
	LayoutOrder []string           `json:"layoutOrder,omitempty"`
	Scenes      []storedScene      `json:"scenes,omitempty"`
	Sources     []storedSource     `json:"sources,omitempty"`
	StageEdits  []storedStageEdits `json:"stageEdits,omitempty"`
	SyncGroups  []storedSyncGroup  `json:"syncGroups,omitempty"`
	Shifts      []storedShift      `json:"shifts,omitempty"`
}

type storedStageEdits struct {
	Scene  string      `json:"scene,omitempty"`
	Boxes  []storedBox `json:"boxes,omitempty"`
	Order  []string    `json:"order,omitempty"`
	Locked []string    `json:"locked,omitempty"`
}

type storedBox struct {
	Source string   `json:"source,omitempty"`
	Box    stageBox `json:"box"`
}

type storedTrackRef struct {
	Source string `json:"source,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

type storedSyncGroup struct {
	Reference storedTrackRef   `json:"reference"`
	Tracks    []storedTrackRef `json:"tracks,omitempty"`
}

type storedShift struct {
	Track  storedTrackRef `json:"track"`
	Offset int64          `json:"offsetNanos"`
}

// storedScene is one scene's name and the sources it shows and hears. The track
// ids are not stable across a restart, so each entry names the source instead.
type storedScene struct {
	ID      string        `json:"id,omitempty"`
	Name    string        `json:"name,omitempty"`
	Video   []string      `json:"video,omitempty"`
	Audio   []storedAudio `json:"audio,omitempty"`
	Members []string      `json:"members"`
	storedSceneLayout
}

type storedSceneLayout struct {
	Layout      string   `json:"layout,omitempty"`
	LayoutOrder []string `json:"layoutOrder,omitempty"`
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
	ID             string  `json:"id,omitempty"`
	URI            string  `json:"uri,omitempty"`
	Name           string  `json:"name,omitempty"`
	Volume         float64 `json:"volume"`
	Muted          bool    `json:"muted,omitempty"`
	Shown          bool    `json:"shown,omitempty"`
	NeedsReselect  bool    `json:"needsReselect,omitempty"`
	StageOnly      bool    `json:"stageOnly,omitempty"`
	PlaylistMaster string  `json:"playlistMaster,omitempty"`
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
	activeLayout := a.captureSceneLayout()
	activeID := a.persistentSceneID(a.scene)
	snap := storedSession{ActiveScene: activeID, Layout: activeLayout.Layout, LayoutOrder: slices.Clone(activeLayout.LayoutOrder)}
	owner := make(map[core.TrackID]string, len(a.items)*2)
	trackRefs := make(map[core.TrackID]storedTrackRef, len(a.items)*2)
	for _, it := range a.items {
		if it.persistentID == "" {
			it.persistentID = a.nextPersistentSourceID()
		}
		if it.source.ID == "" {
			continue
		}
		if it.video != "" {
			owner[it.video] = it.persistentID
			trackRefs[it.video] = storedTrackRef{Source: it.persistentID, Kind: "video"}
		}
		if it.audio != "" {
			owner[it.audio] = it.persistentID
			trackRefs[it.audio] = storedTrackRef{Source: it.persistentID, Kind: "audio"}
		}
	}
	for _, it := range a.items {
		uri, needsReselect := persistentURI(it.uri)
		master, _ := persistentURI(it.playlistMaster)
		name := it.source.Name
		if name == "" {
			name = it.persistedName
		}
		snap.Sources = append(snap.Sources, storedSource{
			ID:             it.persistentID,
			URI:            uri,
			Name:           name,
			Volume:         it.volume,
			Muted:          it.muted,
			Shown:          it.shown,
			NeedsReselect:  needsReselect,
			StageOnly:      it.stageOnly,
			PlaylistMaster: master,
		})
	}
	scenes := a.session.Scenes()
	sceneIDs := make(map[core.SceneID]string, len(scenes))
	for _, scene := range scenes {
		layout := a.sceneLayouts[scene.ID]
		if scene.ID == a.scene {
			layout = activeLayout
		}
		if layout.Layout == "" {
			layout.Layout = layoutFocus.String()
		}
		id := a.persistentSceneID(scene.ID)
		sceneIDs[scene.ID] = id
		stored := storedScene{ID: id, Name: scene.Name, storedSceneLayout: cloneStoredSceneLayout(layout), Members: []string{}}
		members := a.sceneMembers[scene.ID]
		for _, it := range a.items {
			if members == nil || members[it.persistentID] {
				stored.Members = append(stored.Members, it.persistentID)
			}
		}
		videoSources := make(map[string]bool, len(scene.Video))
		audioSources := make(map[string]bool, len(scene.Audio))
		for _, track := range scene.Video {
			if source := owner[track]; source != "" {
				stored.Video = append(stored.Video, source)
				videoSources[source] = true
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
			audioSources[source] = true
		}
		pendingVideo := make([]struct {
			source string
			index  int
		}, 0)
		for source, binding := range a.pendingBindings[scene.ID] {
			if binding.video && !videoSources[source] {
				pendingVideo = append(pendingVideo, struct {
					source string
					index  int
				}{source: source, index: binding.videoIndex})
			}
		}
		sort.Slice(pendingVideo, func(i, j int) bool { return pendingVideo[i].index < pendingVideo[j].index })
		for _, pending := range pendingVideo {
			insertAt := min(max(pending.index, 0), len(stored.Video))
			stored.Video = append(stored.Video, "")
			copy(stored.Video[insertAt+1:], stored.Video[insertAt:])
			stored.Video[insertAt] = pending.source
		}
		pendingAudio := make([]struct {
			audio storedAudio
			index int
		}, 0)
		for source, binding := range a.pendingBindings[scene.ID] {
			if binding.audio != nil && !audioSources[source] {
				pendingAudio = append(pendingAudio, struct {
					audio storedAudio
					index int
				}{audio: *binding.audio, index: binding.audioIndex})
			}
		}
		sort.SliceStable(pendingAudio, func(i, j int) bool { return pendingAudio[i].index < pendingAudio[j].index })
		for _, pending := range pendingAudio {
			insertAt := min(max(pending.index, 0), len(stored.Audio))
			stored.Audio = append(stored.Audio, storedAudio{})
			copy(stored.Audio[insertAt+1:], stored.Audio[insertAt:])
			stored.Audio[insertAt] = pending.audio
		}
		for _, source := range stored.Video {
			stored.Members = appendUniqueString(stored.Members, source)
		}
		for _, level := range stored.Audio {
			stored.Members = appendUniqueString(stored.Members, level.Source)
		}
		snap.Scenes = append(snap.Scenes, stored)
	}
	for sceneID, edits := range a.edits {
		if edits == nil {
			continue
		}
		stored := storedStageEdits{Scene: sceneIDs[sceneID]}
		if stored.Scene == "" {
			continue
		}
		boxSources := make([]string, 0, len(edits.boxes))
		for id := range edits.boxes {
			if it := a.itemOf(id); it != nil && it.persistentID != "" {
				boxSources = append(boxSources, it.persistentID)
			}
		}
		sort.Strings(boxSources)
		for _, id := range boxSources {
			it := a.itemByPersistentID(id)
			stored.Boxes = append(stored.Boxes, storedBox{Source: id, Box: edits.boxes[it.source.ID]})
		}
		for _, id := range edits.order {
			if it := a.itemOf(id); it != nil && it.persistentID != "" {
				stored.Order = append(stored.Order, it.persistentID)
			}
		}
		for id, locked := range edits.locked {
			if locked {
				if it := a.itemOf(id); it != nil && it.persistentID != "" {
					stored.Locked = append(stored.Locked, it.persistentID)
				}
			}
		}
		sort.Strings(stored.Locked)
		snap.StageEdits = append(snap.StageEdits, stored)
	}
	snap.StageEdits = mergeStoredStageEdits(snap.StageEdits, a.pendingStageEdits)
	for _, group := range a.session.SyncGroups() {
		stored := storedSyncGroup{Reference: trackRefs[group.Reference]}
		if stored.Reference.Source == "" {
			continue
		}
		for _, track := range group.Tracks {
			if ref := trackRefs[track]; ref.Source != "" {
				stored.Tracks = append(stored.Tracks, ref)
			}
		}
		snap.SyncGroups = append(snap.SyncGroups, stored)
	}
	for _, pending := range a.pendingSyncGroups {
		pending.Tracks = slices.Clone(pending.Tracks)
		snap.SyncGroups = append(snap.SyncGroups, pending)
	}
	if a.sync != nil && a.sync.timeline != nil {
		for _, shift := range a.sync.timeline.Shifts() {
			if ref := trackRefs[shift.Track]; ref.Source != "" {
				snap.Shifts = append(snap.Shifts, storedShift{Track: ref, Offset: int64(shift.Offset)})
			}
		}
	}
	snap.Shifts = append(snap.Shifts, a.pendingShifts...)
	return snap
}

func cloneStoredSceneLayout(layout storedSceneLayout) storedSceneLayout {
	layout.LayoutOrder = slices.Clone(layout.LayoutOrder)
	return layout
}

func (a *appState) persistentSceneID(id core.SceneID) string {
	if id == "" {
		return ""
	}
	if stored := a.scenePersistentIDs[id]; stored != "" {
		return stored
	}
	if a.scenePersistentIDs == nil {
		a.scenePersistentIDs = make(map[core.SceneID]string)
	}
	for stored, runtime := range a.persistentSceneIDs {
		if runtime == id && stored != "" {
			a.scenePersistentIDs[id] = stored
			return stored
		}
	}
	if a.persistentSceneIDs == nil {
		a.persistentSceneIDs = make(map[string]core.SceneID)
	}
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("stage-%d", n)
		if a.persistentSceneIDs[candidate] == "" {
			a.scenePersistentIDs[id] = candidate
			a.persistentSceneIDs[candidate] = id
			return candidate
		}
	}
}

func (a *appState) captureSceneLayout() storedSceneLayout {
	layout := storedSceneLayout{Layout: a.layout.String()}
	for _, id := range a.pendingLayoutOrder {
		if a.itemByPersistentID(id) != nil && !containsString(layout.LayoutOrder, id) {
			layout.LayoutOrder = append(layout.LayoutOrder, id)
		}
	}
	for _, id := range a.layoutTaken {
		if it := a.itemOf(id); it != nil {
			if it.persistentID == "" {
				it.persistentID = a.nextPersistentSourceID()
			}
			if !containsString(layout.LayoutOrder, it.persistentID) {
				layout.LayoutOrder = append(layout.LayoutOrder, it.persistentID)
			}
		}
	}
	return layout
}

func (a *appState) activateSceneLayout(id core.SceneID) {
	if a.sceneLayouts == nil {
		a.sceneLayouts = make(map[core.SceneID]storedSceneLayout)
	}
	if a.scene != "" && a.scene != id {
		a.sceneLayouts[a.scene] = a.captureSceneLayout()
	}
	a.scene = id
	layout := a.sceneLayouts[id]
	a.layout = parseLayout(layout.Layout)
	a.pendingLayoutOrder = append([]string(nil), layout.LayoutOrder...)
	a.layoutTaken = nil
	for _, saved := range layout.LayoutOrder {
		if it := a.itemByPersistentID(saved); it != nil && it.source.ID != "" {
			a.layoutTaken = append(a.layoutTaken, it.source.ID)
		}
	}
}

func cloneStoredStageEdits(edits []storedStageEdits) []storedStageEdits {
	cloned := make([]storedStageEdits, len(edits))
	for i, edit := range edits {
		cloned[i] = storedStageEdits{
			Scene:  edit.Scene,
			Boxes:  append([]storedBox(nil), edit.Boxes...),
			Order:  append([]string(nil), edit.Order...),
			Locked: append([]string(nil), edit.Locked...),
		}
	}
	return cloned
}

func mergeStoredStageEdits(active, pending []storedStageEdits) []storedStageEdits {
	byScene := make(map[string]*storedStageEdits, len(active)+len(pending))
	for _, source := range append(cloneStoredStageEdits(active), cloneStoredStageEdits(pending)...) {
		if source.Scene == "" {
			continue
		}
		target := byScene[source.Scene]
		if target == nil {
			target = &storedStageEdits{Scene: source.Scene}
			byScene[source.Scene] = target
		}
		boxes := make(map[string]int, len(target.Boxes))
		for i, box := range target.Boxes {
			boxes[box.Source] = i
		}
		for _, box := range source.Boxes {
			if i, ok := boxes[box.Source]; ok {
				target.Boxes[i] = box
			} else {
				boxes[box.Source] = len(target.Boxes)
				target.Boxes = append(target.Boxes, box)
			}
		}
		if len(source.Order) > 0 {
			if len(target.Order) == 0 || len(target.Order) < len(source.Order) {
				target.Order = append([]string(nil), source.Order...)
			} else {
				for _, id := range source.Order {
					if !containsString(target.Order, id) {
						target.Order = append(target.Order, id)
					}
				}
			}
		}
		for _, id := range source.Locked {
			if !containsString(target.Locked, id) {
				target.Locked = append(target.Locked, id)
			}
		}
	}
	out := make([]storedStageEdits, 0, len(byScene))
	for _, edit := range byScene {
		sort.Strings(edit.Locked)
		out = append(out, *edit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scene < out[j].Scene })
	return out
}

func containsString(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	if value != "" && !containsString(values, value) {
		return append(values, value)
	}
	return values
}

func (a *appState) itemByPersistentID(id string) *item {
	for _, it := range a.items {
		if it.persistentID == id {
			return it
		}
	}
	return nil
}

func (a *appState) preserveSourceBeforeReplacement(it *item) {
	if it == nil || it.source.ID == "" || it.persistentID == "" {
		return
	}
	oldSource, oldVideo, oldAudio := it.source.ID, it.video, it.audio
	for _, scene := range a.session.Scenes() {
		binding := a.pendingBindings[scene.ID][it.persistentID]
		for index, track := range scene.Video {
			if track == oldVideo {
				binding.video = true
				binding.videoIndex = index
				break
			}
		}
		for index, level := range scene.Audio {
			if level.Track == oldAudio {
				copy := storedAudio{Source: it.persistentID, Volume: level.Volume, Muted: level.Muted}
				binding.audio = &copy
				binding.audioIndex = index
				break
			}
		}
		if binding.video || binding.audio != nil {
			if a.pendingBindings == nil {
				a.pendingBindings = make(map[core.SceneID]map[string]pendingSceneBinding)
			}
			if a.pendingBindings[scene.ID] == nil {
				a.pendingBindings[scene.ID] = make(map[string]pendingSceneBinding)
			}
			a.pendingBindings[scene.ID][it.persistentID] = binding
		}
	}

	snap := a.captureSessionStore()
	for _, edits := range snap.StageEdits {
		filtered := storedStageEdits{Scene: edits.Scene}
		for _, box := range edits.Boxes {
			if box.Source == it.persistentID {
				filtered.Boxes = append(filtered.Boxes, box)
			}
		}
		for _, source := range edits.Order {
			if source == it.persistentID {
				filtered.Order = append(filtered.Order, source)
			}
		}
		for _, source := range edits.Locked {
			if source == it.persistentID {
				filtered.Locked = append(filtered.Locked, source)
			}
		}
		if len(filtered.Boxes)+len(filtered.Order)+len(filtered.Locked) > 0 {
			a.pendingStageEdits = append(a.pendingStageEdits, filtered)
		}
	}
	for _, group := range snap.SyncGroups {
		member := group.Reference.Source == it.persistentID
		for _, track := range group.Tracks {
			member = member || track.Source == it.persistentID
		}
		if member {
			a.pendingSyncGroups = append(a.pendingSyncGroups, group)
		}
	}
	for _, shift := range snap.Shifts {
		if shift.Track.Source == it.persistentID {
			a.pendingShifts = append(a.pendingShifts, shift)
		}
	}
	if containsTrackSource(a.layoutTaken, oldSource) && !containsString(a.pendingLayoutOrder, it.persistentID) {
		a.pendingLayoutOrder = append(a.pendingLayoutOrder, it.persistentID)
	}
	a.layoutTaken = removeTrackID(a.layoutTaken, oldSource)
	delete(a.audible, oldAudio)
	if a.sync != nil {
		if a.sync.origins != nil {
			delete(a.sync.origins, oldVideo)
			delete(a.sync.origins, oldAudio)
		}
		if a.sync.timeline != nil {
			a.sync.timeline.Forget(oldVideo)
			a.sync.timeline.Forget(oldAudio)
		}
		a.sync.group = ""
	}
}

func containsTrackSource(ids []core.SourceID, source core.SourceID) bool {
	for _, id := range ids {
		if id == source {
			return true
		}
	}
	return false
}

func removeTrackID(ids []core.SourceID, source core.SourceID) []core.SourceID {
	for i, id := range ids {
		if id == source {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

func (a *appState) nextPersistentSourceID() string {
	used := make(map[string]bool, len(a.items))
	for _, it := range a.items {
		if it.persistentID != "" {
			used[it.persistentID] = true
		}
	}
	for {
		a.sourceIdentitySeq++
		id := fmt.Sprintf("muxshift-source-%d", a.sourceIdentitySeq)
		if !used[id] {
			return id
		}
	}
}

func (a *appState) projectLayoutOrder() {
	rank := make(map[string]int, len(a.pendingLayoutOrder))
	for i, id := range a.pendingLayoutOrder {
		rank[id] = i
	}
	sort.SliceStable(a.layoutTaken, func(i, j int) bool {
		left, right := a.itemOf(a.layoutTaken[i]), a.itemOf(a.layoutTaken[j])
		if left == nil || right == nil {
			return right == nil && left != nil
		}
		li, lok := rank[left.persistentID]
		ri, rok := rank[right.persistentID]
		if lok != rok {
			return lok
		}
		if lok {
			return li < ri
		}
		return false
	})
}

// saveSession writes the current arrangement. A missing path (nowhere to write)
// and a write error are logged, never fatal: losing persistence must not take
// the running session down.
func (a *appState) saveSession() error {
	if a.session == nil || a.sessionPath == "" || !a.sessionWriteEnabled {
		return nil
	}
	var started time.Time
	if traceSessionSaveTiming {
		started = time.Now()
	}
	snap := a.captureSessionStore()
	if !started.IsZero() {
		log.Printf("session timing: capture_us=%d", time.Since(started).Microseconds())
	}
	a.sessionSaveMu.Lock()
	a.sessionSaveSeq++
	a.sessionSavePending = &snap
	a.sessionSavePendingSeq = a.sessionSaveSeq
	if a.sessionSaveTimer != nil {
		a.sessionSaveTimer.Stop()
	}
	a.sessionSaveTimer = time.AfterFunc(150*time.Millisecond, a.flushPendingSession)
	a.sessionSaveMu.Unlock()
	return nil
}

func (a *appState) flushPendingSession() {
	a.sessionSaveWriteMu.Lock()
	a.sessionSaveMu.Lock()
	snap := a.sessionSavePending
	seq := a.sessionSavePendingSeq
	a.sessionSavePending = nil
	a.sessionSavePendingSeq = 0
	a.sessionSaveTimer = nil
	a.sessionSaveMu.Unlock()
	if snap != nil {
		err := a.writeSessionSnapshotLocked(*snap)
		notify := a.recordSessionSnapshot(seq, err)
		a.sessionSaveWriteMu.Unlock()
		a.notifySessionSnapshot(seq, notify)
		return
	}
	a.sessionSaveWriteMu.Unlock()
}

// writeSessionSnapshotLocked requires sessionSaveWriteMu. The writer lock
// precedes the state lock when a timer or shutdown claims a snapshot.
func (a *appState) writeSessionSnapshotLocked(snap storedSession) error {
	writer := a.sessionStoreWriter
	if writer == nil {
		writer = saveSessionStore
	}
	if !traceSessionSaveTiming {
		return writer(a.sessionPath, snap)
	}
	started := time.Now()
	err := writer(a.sessionPath, snap)
	log.Printf("session timing: write_us=%d ok=%t", time.Since(started).Microseconds(), err == nil)
	return err
}

func (a *appState) finishSessionSnapshot(seq uint64, err error) {
	a.notifySessionSnapshot(seq, a.recordSessionSnapshot(seq, err))
}

func (a *appState) recordSessionSnapshot(seq uint64, err error) func(func()) {
	a.sessionSaveMu.Lock()
	if seq >= a.sessionSaveDone {
		a.sessionSaveDone = seq
		if err != nil {
			a.sessionErr = fmt.Sprintf("could not save session (%v)", err)
		} else {
			a.sessionErr = ""
		}
	}
	notify := a.sessionSaveNotify
	a.sessionSaveMu.Unlock()
	if err != nil {
		log.Printf("session: could not save (%v)", err)
	} else {
		log.Printf("session: saved")
	}
	return notify
}

func (a *appState) notifySessionSnapshot(seq uint64, notify func(func())) {
	if notify == nil {
		return
	}
	notify(func() {
		a.sessionSaveMu.Lock()
		current := a.sessionSaveDone == seq
		a.sessionSaveMu.Unlock()
		if current && !a.closed {
			a.invalidate()
		}
	})
}

// flushSessionSave makes a queued change durable before the live session is
// released. It also waits for a write that already left the debounce queue.
func (a *appState) flushSessionSave() error {
	a.sessionSaveWriteMu.Lock()
	a.sessionSaveMu.Lock()
	if a.sessionSaveTimer != nil {
		a.sessionSaveTimer.Stop()
		a.sessionSaveTimer = nil
	}
	snap := a.sessionSavePending
	seq := a.sessionSavePendingSeq
	a.sessionSavePending = nil
	a.sessionSavePendingSeq = 0
	a.sessionSaveMu.Unlock()
	if snap == nil {
		err := a.sessionSaveError()
		a.sessionSaveWriteMu.Unlock()
		if err == "" {
			return nil
		}
		return errors.New(err)
	}
	err := a.writeSessionSnapshotLocked(*snap)
	notify := a.recordSessionSnapshot(seq, err)
	a.sessionSaveWriteMu.Unlock()
	a.notifySessionSnapshot(seq, notify)
	return err
}

func (a *appState) sessionSaveError() string {
	a.sessionSaveMu.Lock()
	defer a.sessionSaveMu.Unlock()
	return a.sessionErr
}

// persistentURI omits addresses that may contain credentials or temporary
// access material. The source's stable identity and user configuration remain
// in the snapshot so the UI can request a replacement address after restart.
func persistentURI(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if filepath.IsAbs(raw) {
		return raw, false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", true
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") {
			return "", true
		}
	default:
		return "", true
	}
	return raw, false
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
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after the session object")
		}
		return storedSession{}, fmt.Sprintf("%s is not a session file (%v); starting with an empty session", path, err)
	}
	return sanitizeSessionStore(snap), ""
}

// sanitizeSessionStore drops entries that cannot be reopened and clamps levels,
// so a hand-edited or older file cannot feed the session impossible values.
func sanitizeSessionStore(snap storedSession) storedSession {
	out := storedSession{
		ActiveScene: snap.ActiveScene,
		Layout:      snap.Layout,
		LayoutOrder: append([]string(nil), snap.LayoutOrder...),
		StageEdits:  append([]storedStageEdits(nil), snap.StageEdits...),
		SyncGroups:  append([]storedSyncGroup(nil), snap.SyncGroups...),
		Shifts:      append([]storedShift(nil), snap.Shifts...),
	}
	for _, src := range snap.Sources {
		if strings.TrimSpace(src.URI) == "" && !src.NeedsReselect {
			continue
		}
		if src.NeedsReselect {
			src.URI = ""
		} else if safeURI, needsReselect := persistentURI(src.URI); needsReselect {
			src.URI = safeURI
			src.NeedsReselect = true
		}
		src.Volume = clampVolume(src.Volume)
		src.PlaylistMaster, _ = persistentURI(src.PlaylistMaster)
		out.Sources = append(out.Sources, src)
	}
	usedSceneIDs := make(map[string]bool, len(snap.Scenes))
	for i := range snap.Scenes {
		scene := &snap.Scenes[i]
		if id := strings.TrimSpace(scene.ID); id != "" {
			usedSceneIDs[id] = true
		}
	}
	nextSceneID := 1
	for i := range snap.Scenes {
		scene := &snap.Scenes[i]
		if strings.TrimSpace(scene.ID) == "" {
			for {
				candidate := fmt.Sprintf("stage-%d", nextSceneID)
				nextSceneID++
				if !usedSceneIDs[candidate] {
					scene.ID = candidate
					usedSceneIDs[candidate] = true
					break
				}
			}
		}
		for i := range scene.Audio {
			scene.Audio[i].Volume = clampVolume(scene.Audio[i].Volume)
		}
		out.Scenes = append(out.Scenes, *scene)
	}
	if !validLayoutName(out.Layout) {
		out.Layout = "focus"
	}
	for i := range out.StageEdits {
		edit := &out.StageEdits[i]
		if strings.TrimSpace(edit.Scene) == "" {
			edit.Scene = ""
			continue
		}
		boxes := edit.Boxes[:0]
		for _, box := range edit.Boxes {
			if box.Source != "" && box.Box.valid() {
				box.Box = box.Box.normalized()
				boxes = append(boxes, box)
			}
		}
		edit.Boxes = boxes
	}
	return out
}

func validLayoutName(name string) bool {
	for _, kind := range layoutKinds {
		if kind.String() == name {
			return true
		}
	}
	return name == ""
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
	app.pendingBindings = make(map[core.SceneID]map[string]pendingSceneBinding)
	app.sceneLayouts = make(map[core.SceneID]storedSceneLayout)
	app.sceneMembers = make(map[core.SceneID]map[string]bool)
	app.pendingStageEdits = append([]storedStageEdits(nil), snap.StageEdits...)
	app.pendingSyncGroups = append([]storedSyncGroup(nil), snap.SyncGroups...)
	app.pendingShifts = append([]storedShift(nil), snap.Shifts...)

	byID := make(map[string]*item, len(snap.Sources))
	for _, stored := range snap.Sources {
		var it *item
		var err error
		if stored.NeedsReselect || strings.TrimSpace(stored.URI) == "" {
			it = &item{uri: "", volume: clampVolume(stored.Volume), openErr: errors.New("choose this source address again to reopen it")}
		} else {
			it, err = app.addFrom(stored.URI, originAdded)
		}
		it.persistentID = stored.ID
		it.persistedName = stored.Name
		it.restored = true
		it.stageOnly = stored.StageOnly
		it.playlistMaster = stored.PlaylistMaster
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
				it.persistedName = ""
			}
		}
		app.items = append(app.items, it)
		if stored.ID != "" {
			byID[stored.ID] = it
		}
	}

	var first core.SceneID
	sceneIDs := make(map[string]core.SceneID, len(snap.Scenes))
	persistentIDs := make([]string, len(snap.Scenes))
	reservedIDs := make(map[string]bool, len(snap.Scenes))
	for i, stored := range snap.Scenes {
		if stored.ID != "" && !reservedIDs[stored.ID] {
			persistentIDs[i] = stored.ID
			reservedIDs[stored.ID] = true
		}
	}
	nextID := 1
	for i := range persistentIDs {
		if persistentIDs[i] != "" {
			continue
		}
		for {
			candidate := fmt.Sprintf("stage-%d", nextID)
			nextID++
			if !reservedIDs[candidate] {
				persistentIDs[i] = candidate
				reservedIDs[candidate] = true
				break
			}
		}
	}
	var restoreErr error
	for sceneIndex, stored := range snap.Scenes {
		scene, err := session.AddScene(stored.Name)
		if err != nil {
			return nil, err
		}
		if first == "" {
			first = scene.ID
		}
		persistentID := persistentIDs[sceneIndex]
		sceneIDs[persistentID] = scene.ID
		layout := stored.storedSceneLayout
		if layout.Layout == "" {
			layout = storedSceneLayout{Layout: snap.Layout, LayoutOrder: snap.LayoutOrder}
		}
		layout.LayoutOrder = append([]string(nil), layout.LayoutOrder...)
		app.sceneLayouts[scene.ID] = layout
		members := make(map[string]bool)
		app.sceneMembers[scene.ID] = members
		for _, source := range stored.Members {
			if byID[source] != nil {
				members[source] = true
			}
		}
		if stored.Members == nil && len(snap.Scenes) == 1 {
			for source := range byID {
				members[source] = true
			}
		}
		if stored.Members == nil {
			for _, edit := range snap.StageEdits {
				if edit.Scene != stored.ID {
					continue
				}
				for _, box := range edit.Boxes {
					if byID[box.Source] != nil {
						members[box.Source] = true
					}
				}
				for _, source := range append(slices.Clone(edit.Order), edit.Locked...) {
					if byID[source] != nil {
						members[source] = true
					}
				}
			}
			if stored.ID == snap.ActiveScene {
				for _, source := range snap.LayoutOrder {
					if byID[source] != nil {
						members[source] = true
					}
				}
			}
			if stored.storedSceneLayout.LayoutOrder != nil {
				for _, source := range stored.storedSceneLayout.LayoutOrder {
					if byID[source] != nil {
						members[source] = true
					}
				}
			}
		}
		video := make([]core.TrackID, 0, len(stored.Video))
		for index, source := range stored.Video {
			if byID[source] != nil {
				members[source] = true
			}
			if it := byID[source]; it != nil && it.video != "" {
				video = append(video, it.video)
			} else if byID[source] != nil {
				app.setPendingVideo(scene.ID, source, index)
			}
		}
		if len(video) > 0 {
			if _, err := session.SetSceneVideo(scene.ID, video...); err != nil {
				restoreErr = err
			}
		}
		for index, level := range stored.Audio {
			it := byID[level.Source]
			if it == nil {
				continue
			}
			members[level.Source] = true
			if it.audio == "" {
				copy := level
				app.setPendingAudio(scene.ID, level.Source, copy, index)
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
	active := sceneIDs[snap.ActiveScene]
	if active == "" {
		active = first
	}
	app.activateSceneLayout(active)
	output, err := session.AddOutput(core.OutputDesktopWindow, app.scene)
	if err != nil {
		return nil, err
	}
	app.output = output.ID
	app.persistentSceneIDs = sceneIDs
	app.scenePersistentIDs = make(map[core.SceneID]string, len(sceneIDs))
	for persistent, runtime := range sceneIDs {
		app.scenePersistentIDs[runtime] = persistent
	}
	app.restorePersistentMetadata(sceneIDs)
	app.apply()
	return app, nil
}

// newAppStateFromStoreDeferred builds the saved scenes and placeholder rows
// without probing any media. The window can be shown before each source is
// inspected by startStoredSourceRestores.
func newAppStateFromStoreDeferred(loaded settings, snap storedSession) (*appState, error) {
	deferred := snap
	deferred.Sources = append([]storedSource(nil), snap.Sources...)
	for i := range deferred.Sources {
		deferred.Sources[i].URI = ""
		deferred.Sources[i].NeedsReselect = true
	}
	app, err := newAppStateFromStore(loaded, deferred)
	if err != nil {
		return nil, err
	}
	app.startupSources = append([]storedSource(nil), snap.Sources...)
	for _, stored := range snap.Sources {
		it := app.itemByPersistentID(stored.ID)
		if it == nil {
			continue
		}
		if stored.NeedsReselect || strings.TrimSpace(stored.URI) == "" {
			it.uri = ""
			it.openErr = errors.New("choose this source address again to reopen it")
			continue
		}
		it.uri = stored.URI
		it.openErr = nil
	}
	return app, nil
}

func (a *appState) restorePersistentMetadata(sceneIDs map[string]core.SceneID) {
	remainingEdits := make([]storedStageEdits, 0, len(a.pendingStageEdits))
	for _, stored := range a.pendingStageEdits {
		scene := sceneIDs[stored.Scene]
		if scene == "" {
			continue
		}
		edits := a.edits[scene]
		if edits == nil {
			fresh := newStageEdits()
			edits = &fresh
		}
		left := storedStageEdits{Scene: stored.Scene}
		for _, box := range stored.Boxes {
			if it := a.itemByPersistentID(box.Source); it != nil && it.source.ID != "" && box.Box.valid() {
				edits.boxes[it.source.ID] = box.Box.normalized()
			} else {
				left.Boxes = append(left.Boxes, box)
			}
		}
		ordered := make([]core.SourceID, 0, len(stored.Order))
		unresolvedOrder := false
		validOrder := make([]string, 0, len(stored.Order))
		for _, id := range stored.Order {
			it := a.itemByPersistentID(id)
			if it == nil {
				continue
			}
			validOrder = append(validOrder, id)
			if it.source.ID == "" {
				unresolvedOrder = true
				continue
			}
			ordered = append(ordered, it.source.ID)
		}
		if len(ordered) > 0 || unresolvedOrder {
			seen := make(map[core.SourceID]bool, len(ordered))
			for _, id := range ordered {
				seen[id] = true
			}
			for _, id := range edits.order {
				if !seen[id] {
					ordered = append(ordered, id)
				}
			}
			edits.order = ordered
		}
		if unresolvedOrder {
			left.Order = validOrder
		}
		for _, id := range stored.Locked {
			if it := a.itemByPersistentID(id); it != nil && it.source.ID != "" {
				edits.locked[it.source.ID] = true
			} else {
				left.Locked = append(left.Locked, id)
			}
		}
		if len(edits.boxes)+len(edits.order)+len(edits.locked) > 0 {
			if a.edits == nil {
				a.edits = make(map[core.SceneID]*stageEdits)
			}
			a.edits[scene] = edits
		}
		if len(left.Boxes)+len(left.Order)+len(left.Locked) > 0 {
			remainingEdits = append(remainingEdits, left)
		}
	}
	a.pendingStageEdits = remainingEdits
	remainingShifts := make([]storedShift, 0, len(a.pendingShifts))
	for _, shift := range a.pendingShifts {
		if track := a.trackForStoredRef(shift.Track); track != "" {
			if err := a.sync.timeline.SetShift(track, time.Duration(shift.Offset)); err != nil {
				a.lastErr = safeOpenError(err)
			}
		} else {
			remainingShifts = append(remainingShifts, shift)
		}
	}
	a.pendingShifts = remainingShifts
	remaining := make([]storedSyncGroup, 0, len(a.pendingSyncGroups))
	for _, stored := range a.pendingSyncGroups {
		reference := a.trackForStoredRef(stored.Reference)
		tracks := make([]core.TrackID, 0, len(stored.Tracks))
		complete := reference != "" && len(stored.Tracks) > 0
		for _, ref := range stored.Tracks {
			track := a.trackForStoredRef(ref)
			if track == "" {
				complete = false
				break
			}
			tracks = append(tracks, track)
		}
		if !complete {
			remaining = append(remaining, stored)
			continue
		}
		group, err := a.restoreSyncGroup(reference, tracks)
		if err != nil {
			a.lastErr = safeOpenError(err)
			remaining = append(remaining, stored)
			continue
		}
		if a.sync.group == "" {
			a.sync.group = group.ID
		}
	}
	a.pendingSyncGroups = remaining
}

func (a *appState) restoreSyncGroup(reference core.TrackID, tracks []core.TrackID) (core.SyncGroup, error) {
	member := make(map[core.TrackID]bool, len(tracks))
	for _, id := range tracks {
		member[id] = true
	}
	for _, existing := range a.session.SyncGroups() {
		subset := len(existing.Tracks) > 0
		for _, id := range existing.Tracks {
			subset = subset && member[id]
		}
		if subset {
			return a.session.UpdateSyncGroup(existing.ID, reference, tracks...)
		}
	}
	return a.session.AddSyncGroup(reference, tracks...)
}

func (a *appState) trackForStoredRef(ref storedTrackRef) core.TrackID {
	it := a.itemByPersistentID(ref.Source)
	if it == nil {
		return ""
	}
	switch ref.Kind {
	case "video":
		return it.video
	case "audio":
		return it.audio
	default:
		return ""
	}
}

func (a *appState) setPendingVideo(scene core.SceneID, source string, index int) {
	if a.pendingBindings == nil {
		a.pendingBindings = make(map[core.SceneID]map[string]pendingSceneBinding)
	}
	bySource := a.pendingBindings[scene]
	if bySource == nil {
		bySource = make(map[string]pendingSceneBinding)
		a.pendingBindings[scene] = bySource
	}
	binding := bySource[source]
	binding.video = true
	binding.videoIndex = index
	bySource[source] = binding
}

func (a *appState) setPendingAudio(scene core.SceneID, source string, audio storedAudio, index int) {
	if a.pendingBindings == nil {
		a.pendingBindings = make(map[core.SceneID]map[string]pendingSceneBinding)
	}
	bySource := a.pendingBindings[scene]
	if bySource == nil {
		bySource = make(map[string]pendingSceneBinding)
		a.pendingBindings[scene] = bySource
	}
	binding := bySource[source]
	binding.audio = &audio
	binding.audioIndex = index
	bySource[source] = binding
}

func (a *appState) clearPendingSceneAudio(scene core.SceneID) {
	for source, binding := range a.pendingBindings[scene] {
		binding.audio = nil
		if !binding.video {
			delete(a.pendingBindings[scene], source)
		} else {
			a.pendingBindings[scene][source] = binding
		}
	}
}

func (a *appState) forgetPendingSource(source string) {
	if source == "" {
		return
	}
	for _, bindings := range a.pendingBindings {
		delete(bindings, source)
	}
	a.pendingLayoutOrder = removeString(a.pendingLayoutOrder, source)
	for id, layout := range a.sceneLayouts {
		layout.LayoutOrder = removeString(slices.Clone(layout.LayoutOrder), source)
		a.sceneLayouts[id] = layout
	}
	for i := range a.pendingStageEdits {
		edit := &a.pendingStageEdits[i]
		boxes := edit.Boxes[:0]
		for _, box := range edit.Boxes {
			if box.Source != source {
				boxes = append(boxes, box)
			}
		}
		edit.Boxes = boxes
		edit.Order = removeString(edit.Order, source)
		edit.Locked = removeString(edit.Locked, source)
	}
	groups := a.pendingSyncGroups[:0]
	for _, group := range a.pendingSyncGroups {
		keep := group.Reference.Source != source
		for _, track := range group.Tracks {
			if track.Source == source {
				keep = false
				break
			}
		}
		if keep {
			groups = append(groups, group)
		}
	}
	a.pendingSyncGroups = groups
	shifts := a.pendingShifts[:0]
	for _, shift := range a.pendingShifts {
		if shift.Track.Source != source {
			shifts = append(shifts, shift)
		}
	}
	a.pendingShifts = shifts
}

func (a *appState) clearPendingStageOrder(scene core.SceneID) {
	persistent := ""
	for id, current := range a.persistentSceneIDs {
		if current == scene {
			persistent = id
			break
		}
	}
	for i := range a.pendingStageEdits {
		if a.pendingStageEdits[i].Scene == persistent {
			a.pendingStageEdits[i].Order = nil
		}
	}
}

func (a *appState) forgetPendingStageEdits(scene core.SceneID) {
	persistent := ""
	for id, current := range a.persistentSceneIDs {
		if current == scene {
			persistent = id
			break
		}
	}
	kept := a.pendingStageEdits[:0]
	for _, edit := range a.pendingStageEdits {
		if edit.Scene != persistent {
			kept = append(kept, edit)
		}
	}
	a.pendingStageEdits = kept
}

func removeString(values []string, value string) []string {
	for i, current := range values {
		if current == value {
			return append(values[:i], values[i+1:]...)
		}
	}
	return values
}
