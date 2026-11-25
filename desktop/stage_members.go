package main

import (
	"errors"
	"slices"

	"github.com/xswt442-cmd/muxshift/core"
)

func (a *appState) isStageMember(it *item) bool {
	members := a.sceneMembers[a.scene]
	return it != nil && (members == nil || members[it.persistentID])
}

func (a *appState) materializeStageMembers(scene core.SceneID) map[string]bool {
	if a.sceneMembers == nil {
		a.sceneMembers = make(map[core.SceneID]map[string]bool)
	}
	members := a.sceneMembers[scene]
	if members == nil {
		members = make(map[string]bool, len(a.items))
		for _, it := range a.items {
			members[a.stageSourceID(it)] = true
		}
		a.sceneMembers[scene] = members
	}
	return members
}

func (a *appState) stageSourceID(it *item) string {
	if it.persistentID == "" {
		it.persistentID = a.nextPersistentSourceID()
	}
	return it.persistentID
}

func (a *appState) linkStageMember(it *item) {
	if it == nil || it.stageOnly || a.isStageMember(it) {
		return
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		a.lastErr = safeOpenError(err)
		return
	}
	sourceID := a.stageSourceID(it)
	if it.video != "" {
		if _, err := a.session.SetSceneVideo(scene.ID, append(scene.Video, it.video)...); err != nil {
			a.lastErr = safeOpenError(err)
			return
		}
	} else if it.video == "" {
		index := len(scene.Video)
		for _, pending := range a.pendingBindings[scene.ID] {
			if pending.video {
				index = max(index, pending.videoIndex+1)
			}
		}
		if binding := a.pendingBindings[scene.ID][sourceID]; !binding.video {
			a.setPendingVideo(scene.ID, sourceID, index)
		}
	}
	if it.audio != "" {
		if _, err := a.session.SetSceneAudio(scene.ID, it.audio, it.volume, false); err != nil {
			a.lastErr = safeOpenError(err)
			return
		}
	} else {
		index := len(scene.Audio)
		for _, pending := range a.pendingBindings[scene.ID] {
			if pending.audio != nil {
				index = max(index, pending.audioIndex+1)
			}
		}
		a.setPendingAudio(scene.ID, sourceID, storedAudio{Source: sourceID, Volume: it.volume}, index)
	}
	a.materializeStageMembers(scene.ID)[sourceID] = true
	a.selected = itemKey(it)
	a.apply()
	a.wakeShown(it)
	a.saveSession()
	a.invalidate()
}

func (a *appState) unlinkStageMember(it *item) {
	if !a.isStageMember(it) {
		return
	}
	scene, err := a.session.Scene(a.scene)
	if err != nil {
		a.lastErr = safeOpenError(err)
		return
	}
	removedVideoIndex, removedAudioIndex := -1, -1
	if slices.Contains(scene.Video, it.video) {
		liveIndex := slices.Index(scene.Video, it.video)
		removedVideoIndex = expandedPendingIndex(liveIndex, a.pendingBindings[scene.ID], func(b pendingSceneBinding) (int, bool) { return b.videoIndex, b.video })
		video := slices.DeleteFunc(scene.Video, func(track core.TrackID) bool { return track == it.video })
		if _, err := a.session.SetSceneVideo(scene.ID, video...); err != nil {
			a.lastErr = safeOpenError(err)
			return
		}
	}
	for audioIndex, level := range scene.Audio {
		if level.Track == it.audio {
			removedAudioIndex = expandedPendingIndex(audioIndex, a.pendingBindings[scene.ID], func(b pendingSceneBinding) (int, bool) { return b.audioIndex, b.audio != nil })
			if _, err := a.session.RemoveSceneAudio(scene.ID, it.audio); err != nil {
				a.lastErr = safeOpenError(err)
				return
			}
			break
		}
	}
	delete(a.materializeStageMembers(scene.ID), it.persistentID)
	if bindings := a.pendingBindings[scene.ID]; bindings != nil {
		removed := bindings[it.persistentID]
		delete(bindings, it.persistentID)
		if removed.video {
			removedVideoIndex = removed.videoIndex
		}
		if removed.audio != nil {
			removedAudioIndex = removed.audioIndex
		}
		compactPendingMemberIndexes(bindings, removedVideoIndex, removedAudioIndex)
	}
	a.layoutTaken = removeTrackID(a.layoutTaken, it.source.ID)
	a.pendingLayoutOrder = removeString(a.pendingLayoutOrder, it.persistentID)
	if edits := a.edits[scene.ID]; edits != nil {
		edits.forget(it.source.ID)
	}
	for i := range a.pendingStageEdits {
		edit := &a.pendingStageEdits[i]
		if a.persistentSceneIDs[edit.Scene] == scene.ID {
			edit.Boxes = slices.DeleteFunc(edit.Boxes, func(box storedBox) bool { return box.Source == it.persistentID })
			edit.Order = removeString(edit.Order, it.persistentID)
			edit.Locked = removeString(edit.Locked, it.persistentID)
		}
	}
	a.reconcileStageSelection()
	if it.stageOnly && !a.sourceHasStageMember(it.persistentID) {
		a.removeCatalogSource(it)
		return
	}
	a.apply()
	a.saveSession()
	a.invalidate()
}

func (a *appState) sourceHasStageMember(source string) bool {
	for _, scene := range a.session.Scenes() {
		members, initialized := a.sceneMembers[scene.ID]
		if !initialized {
			return true
		}
		if members[source] {
			return true
		}
	}
	return false
}

func (a *appState) removeCatalogSource(it *item) {
	if it == nil {
		return
	}
	if a.sourceHasStageMember(it.persistentID) {
		a.lastErr = errors.New("remove this source from every stage before deleting it from the catalog")
		a.invalidate()
		return
	}
	index := -1
	for i, current := range a.items {
		if current == it {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	if err := a.remove(index); err != nil {
		a.lastErr = safeOpenError(err)
		return
	}
	a.saveSession()
	a.invalidate()
}

// compactPendingMemberIndexes keeps offline members' intended order dense after
// one is removed. Their positions share an index space with live scene tracks.
func expandedPendingIndex(liveIndex int, bindings map[string]pendingSceneBinding, slot func(pendingSceneBinding) (int, bool)) int {
	fullIndex := liveIndex
	for {
		next := liveIndex
		for _, binding := range bindings {
			index, exists := slot(binding)
			if exists && index <= fullIndex {
				next++
			}
		}
		if next == fullIndex {
			return fullIndex
		}
		fullIndex = next
	}
}

func compactPendingMemberIndexes(bindings map[string]pendingSceneBinding, removedVideoIndex, removedAudioIndex int) {
	compact := func(index int, pending func(pendingSceneBinding) bool, get func(pendingSceneBinding) int, set func(*pendingSceneBinding, int)) {
		if index < 0 {
			return
		}
		for id, binding := range bindings {
			if pending(binding) && get(binding) > index {
				set(&binding, get(binding)-1)
				bindings[id] = binding
			}
		}
	}
	compact(removedVideoIndex, func(b pendingSceneBinding) bool { return b.video }, func(b pendingSceneBinding) int { return b.videoIndex }, func(b *pendingSceneBinding, n int) { b.videoIndex = n })
	compact(removedAudioIndex, func(b pendingSceneBinding) bool { return b.audio != nil }, func(b pendingSceneBinding) int { return b.audioIndex }, func(b *pendingSceneBinding, n int) { b.audioIndex = n })
}

func (a *appState) reconcileStageSelection() {
	onStage := func(id core.SourceID) bool {
		if id == "" {
			return false
		}
		it, _, err := a.itemBySource(id)
		return err == nil && a.isStageMember(it)
	}
	if !onStage(a.selected) {
		a.selected = ""
	}
	if !onStage(a.syncRef) {
		a.syncRef = ""
	}
	if !onStage(a.reselectSource) {
		a.reselectSource = ""
		a.reselectText = ""
	}
}
