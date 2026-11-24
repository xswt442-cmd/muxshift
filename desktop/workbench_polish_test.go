package main

import (
	"errors"
	"testing"

	"github.com/xswt442-cmd/muxshift/core"
)

func TestTopWorkspacePageKeepsSourcesUnderStage(t *testing.T) {
	a := &appState{workspace: workspaceStage, dockUp: true, dockTab: 0}
	if got := a.currentWorkspacePage(); got != workspaceSources {
		t.Fatalf("current page = %v, want Sources", got)
	}
	if got := a.topWorkspacePage(); got != workspaceStage {
		t.Fatalf("top label page = %v, want Stage", got)
	}
}

func TestMixerChannelShowsLocalizedActiveSolo(t *testing.T) {
	a := &appState{settings: settings{Language: languageZH}}
	it := &item{source: core.Source{ID: "source-1"}, audio: "track-1"}
	if got := a.mixerChannelSoloLabel(it); got != "" {
		t.Fatalf("solo label without active solo = %q", got)
	}
	a.solo = it.source.ID
	if got := a.mixerChannelSoloLabel(it); got != "独听中" {
		t.Fatalf("solo label with active solo = %q", got)
	}
}

func TestImportFailureCauseIsLocalizedAndConcise(t *testing.T) {
	a := &appState{settings: settings{Language: languageEN}}
	if got := a.importFailureCause(errors.New("open media: connection timed out")); got != "The source could not be reached." {
		t.Fatalf("English cause = %q", got)
	}
	a.settings.Language = languageZH
	if got := a.importFailureCause(errors.New("unsupported media format")); got != "不支持此媒体格式。" {
		t.Fatalf("Chinese cause = %q", got)
	}
}

func TestImportFailureCauseLocalizesHTTP404WithMediaPath(t *testing.T) {
	a := &appState{settings: settings{Language: languageZH}}
	err := errors.New("high-1.mp4 answered 404 Not Found")
	if got := a.importFailureCause(err); got != "找不到此文件或地址。" {
		t.Fatalf("Chinese HTTP 404 cause = %q", got)
	}
	if got := err.Error(); got != "high-1.mp4 answered 404 Not Found" {
		t.Fatalf("diagnostic detail changed: %q", got)
	}
}
