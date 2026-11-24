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
	if a.isStageMember(it) {
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
		a.setPendingAudio(scene.ID, sourceID, storedAudio{Source: sourceID, Volume: it.volume}, len(scene.Audio))
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
	if slices.Contains(scene.Video, it.video) {
		video := slices.DeleteFunc(scene.Video, func(track core.TrackID) bool { return track == it.video })
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
	delete(a.materializeStageMembers(scene.ID), it.persistentID)
	if bindings := a.pendingBindings[scene.ID]; bindings != nil {
		delete(bindings, it.persistentID)
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
	a.invalidate()
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
