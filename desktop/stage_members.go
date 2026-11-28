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
	reference := a.syncReference()
	defer a.invalidateChangedAlignmentReference(reference)
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
	} else if bindings := a.pendingBindings[scene.ID]; !bindings[sourceID].video {
		a.setPendingVideo(scene.ID, sourceID, expandedPendingIndex(len(scene.Video), bindings, pendingVideoSlot))
	}
	if it.audio != "" {
		if _, err := a.session.SetSceneAudio(scene.ID, it.audio, it.volume, false); err != nil {
			a.lastErr = safeOpenError(err)
			return
		}
	} else if bindings := a.pendingBindings[scene.ID]; bindings[sourceID].audio == nil {
		index := expandedPendingIndex(len(scene.Audio), bindings, pendingAudioSlot)
		a.setPendingAudio(scene.ID, sourceID, storedAudio{Source: sourceID, Volume: it.volume}, index)
	}
	a.materializeStageMembers(scene.ID)[sourceID] = true
	a.setSelectedSource(itemKey(it))
	a.apply()
	a.wakeShown(it)
	a.reconcileScenePlayers(scene.ID, a.stageGeneration)
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
	if slices.Contains(scene.Video, it.video) {
		video := slices.DeleteFunc(slices.Clone(scene.Video), func(track core.TrackID) bool { return track == it.video })
		if _, err := a.session.SetSceneVideo(scene.ID, video...); err != nil {
			a.lastErr = safeOpenError(err)
			return
		}
	}
	for _, level := range scene.Audio {
		if level.Track == it.audio {
			if _, err := a.session.RemoveSceneAudio(scene.ID, it.audio); err != nil {
				a.lastErr = safeOpenError(err)
				return
			}
			break
		}
	}
	sourceID := a.stageSourceID(it)
	delete(a.materializeStageMembers(scene.ID), sourceID)
	if it.startupPictureScene == scene.ID {
		it.startupPictureScene = ""
	}
	it.closeMediaMenu()
	if a.retryScene == scene.ID && a.retryTargetID == sourceID {
		a.importToken++
		a.clearUnavailableRetry()
	}
	a.removePendingStageRoutes(scene, it, true, true)
	a.layoutTaken = removeTrackID(a.layoutTaken, it.source.ID)
	a.pendingLayoutOrder = removeString(a.pendingLayoutOrder, it.persistentID)
	if edits := a.edits[scene.ID]; edits != nil {
		edits.forget(it.source.ID)
	}
	a.forgetPendingSourceEdits(scene.ID, sourceID)
	if id := itemKey(it); a.alignRow.Reference == id || a.alignRow.Target == id {
		a.invalidateAlignmentRow()
	}
	a.reconcileStageSelection()
	if it.stageOnly && !a.sourceHasStageMember(it.persistentID) {
		a.removeCatalogSource(it)
		return
	}
	a.apply()
	a.reconcileScenePlayers(scene.ID, a.stageGeneration)
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

func pendingVideoSlot(b pendingSceneBinding) (int, bool) { return b.videoIndex, b.video }
func pendingAudioSlot(b pendingSceneBinding) (int, bool) { return b.audioIndex, b.audio != nil }

// expandedPendingIndex maps a live track index into the saved order, where
// unavailable sources still occupy a slot.
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

// removePendingStageRoutes receives the scene as it was before removing its
// tracks, so a removed live or pending source frees the same saved slot.
func (a *appState) removePendingStageRoutes(scene core.Scene, it *item, video, audio bool) {
	bindings := a.pendingBindings[scene.ID]
	binding := bindings[it.persistentID]
	videoIndex, audioIndex := -1, -1
	if video {
		if binding.video {
			videoIndex = binding.videoIndex
		} else if index := slices.Index(scene.Video, it.video); index >= 0 {
			videoIndex = expandedPendingIndex(index, bindings, pendingVideoSlot)
		}
		binding.video = false
	}
	if audio {
		if binding.audio != nil {
			audioIndex = binding.audioIndex
		} else if index := slices.IndexFunc(scene.Audio, func(level core.AudioLevel) bool { return level.Track == it.audio }); index >= 0 {
			audioIndex = expandedPendingIndex(index, bindings, pendingAudioSlot)
		}
		binding.audio = nil
	}
	if binding.video || binding.audio != nil {
		bindings[it.persistentID] = binding
	} else {
		delete(bindings, it.persistentID)
	}
	compactPendingMemberIndexes(bindings, videoIndex, audioIndex)
}

// compactPendingMemberIndexes leaves no hole after a route is removed.
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
		a.setSelectedSource("")
	}
	if !onStage(a.syncRef) {
		a.syncRef = ""
	}
	if !onStage(a.reselectSource) {
		a.reselectSource = ""
		a.reselectText = ""
	}
}
