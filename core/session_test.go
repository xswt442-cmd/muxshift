package core

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// TestCommentaryScene builds the first use case: a camera with its ambient
// sound, a commentary source with its own sound, one sync group binding them
// to the camera's event clock, and a scene that shows the camera while mixing
// both audio tracks at their own levels. It then switches the scene to a
// second picture without touching the group.
func TestCommentaryScene(t *testing.T) {
	s := NewSession("match")

	camera, err := s.AddSource("https://example.test/camera.m3u8")
	if err != nil {
		t.Fatalf("add camera source: %v", err)
	}
	cameraVideo, err := s.AddTrack(camera.ID, MediaVideo, "camera")
	if err != nil {
		t.Fatalf("add camera video: %v", err)
	}
	cameraAudio, err := s.AddTrack(camera.ID, MediaAudio, "ambient")
	if err != nil {
		t.Fatalf("add camera audio: %v", err)
	}

	commentary, err := s.AddSource("https://example.test/commentary.m3u8")
	if err != nil {
		t.Fatalf("add commentary source: %v", err)
	}
	commentaryAudio, err := s.AddTrack(commentary.ID, MediaAudio, "commentary")
	if err != nil {
		t.Fatalf("add commentary audio: %v", err)
	}

	group, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID, commentaryAudio.ID)
	if err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	if group.Reference != cameraVideo.ID {
		t.Fatalf("group reference = %s, want %s", group.Reference, cameraVideo.ID)
	}
	if len(group.Tracks) != 3 {
		t.Fatalf("group tracks = %d, want 3", len(group.Tracks))
	}

	mix, err := s.AddScene("commentary mix")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := s.SetSceneVideo(mix.ID, cameraVideo.ID); err != nil {
		t.Fatalf("set scene video: %v", err)
	}
	if _, err := s.SetSceneAudio(mix.ID, cameraAudio.ID, 0.6, false); err != nil {
		t.Fatalf("set ambient level: %v", err)
	}
	scene, err := s.SetSceneAudio(mix.ID, commentaryAudio.ID, 1, false)
	if err != nil {
		t.Fatalf("set commentary level: %v", err)
	}
	if len(scene.Video) != 1 || scene.Video[0] != cameraVideo.ID {
		t.Fatalf("scene video = %v, want [%s]", scene.Video, cameraVideo.ID)
	}
	if len(scene.Audio) != 2 {
		t.Fatalf("scene audio tracks = %d, want 2", len(scene.Audio))
	}
	if scene.Audio[0].Track != cameraAudio.ID || scene.Audio[0].Volume != 0.6 {
		t.Fatalf("first audio level = %+v, want ambient at 0.6", scene.Audio[0])
	}
	out, err := s.AddOutput(OutputDesktopWindow, mix.ID)
	if err != nil {
		t.Fatalf("add output: %v", err)
	}
	if out.Scene != mix.ID {
		t.Fatalf("output scene = %s, want %s", out.Scene, mix.ID)
	}

	// Switching the shown picture keeps the mix and the group intact.
	second, err := s.AddTrack(camera.ID, MediaVideo, "wide")
	if err != nil {
		t.Fatalf("add second camera track: %v", err)
	}
	switched, err := s.SetSceneVideo(mix.ID, second.ID)
	if err != nil {
		t.Fatalf("switch scene video: %v", err)
	}
	if len(switched.Video) != 1 || switched.Video[0] != second.ID {
		t.Fatalf("scene video after switch = %v, want [%s]", switched.Video, second.ID)
	}
	if len(switched.Audio) != 2 {
		t.Fatalf("scene audio after switch = %d, want 2", len(switched.Audio))
	}
	if groups := s.SyncGroups(); len(groups) != 1 || len(groups[0].Tracks) != 3 {
		t.Fatalf("sync groups after switch = %+v", groups)
	}
}

// TestGridWithoutSync builds the second use case: four unrelated sources in
// one scene, with no sync group at all.
func TestGridWithoutSync(t *testing.T) {
	s := NewSession("grid")
	var videos []TrackID
	for _, uri := range []string{
		"https://example.test/one.m3u8",
		"https://example.test/two.m3u8",
		"https://example.test/three.m3u8",
		"https://example.test/four.m3u8",
	} {
		src, err := s.AddSource(uri)
		if err != nil {
			t.Fatalf("add source %s: %v", uri, err)
		}
		track, err := s.AddTrack(src.ID, MediaVideo, uri)
		if err != nil {
			t.Fatalf("add track %s: %v", uri, err)
		}
		videos = append(videos, track.ID)
	}

	scene, err := s.AddScene("four up")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	scene, err = s.SetSceneVideo(scene.ID, videos...)
	if err != nil {
		t.Fatalf("set four videos: %v", err)
	}
	if len(scene.Video) != 4 {
		t.Fatalf("scene videos = %d, want 4", len(scene.Video))
	}
	if groups := s.SyncGroups(); len(groups) != 0 {
		t.Fatalf("sync groups = %+v, want none", groups)
	}
	if _, err := s.AddOutput(OutputDesktopWindow, scene.ID); err != nil {
		t.Fatalf("add output: %v", err)
	}
}

// TestRemoveSourceCleansReferences removes a source and checks that its
// tracks, the group membership and the scene's audio level disappear, while
// the other source keeps playing.
func TestRemoveSourceCleansReferences(t *testing.T) {
	s := NewSession("cleanup")
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	commentary, commentaryAudio := addCommentary(t, s)

	if _, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID, commentaryAudio.ID); err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	scene, err := s.AddScene("mix")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := s.SetSceneVideo(scene.ID, cameraVideo.ID); err != nil {
		t.Fatalf("set video: %v", err)
	}
	if _, err := s.SetSceneAudio(scene.ID, cameraAudio.ID, 1, false); err != nil {
		t.Fatalf("set ambient: %v", err)
	}
	if _, err := s.SetSceneAudio(scene.ID, commentaryAudio.ID, 1, false); err != nil {
		t.Fatalf("set commentary: %v", err)
	}

	if err := s.RemoveSource(commentary.ID); err != nil {
		t.Fatalf("remove commentary source: %v", err)
	}

	if _, err := s.Track(commentaryAudio.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed track lookup error = %v, want ErrNotFound", err)
	}
	if _, err := s.Source(commentary.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed source lookup error = %v, want ErrNotFound", err)
	}
	if _, err := s.Source(camera.ID); err != nil {
		t.Fatalf("camera source lost: %v", err)
	}

	got, err := s.Scene(scene.ID)
	if err != nil {
		t.Fatalf("scene after removal: %v", err)
	}
	if len(got.Audio) != 1 || got.Audio[0].Track != cameraAudio.ID {
		t.Fatalf("scene audio after removal = %+v, want only the camera's", got.Audio)
	}
	if len(got.Video) != 1 {
		t.Fatalf("scene video after removal = %v", got.Video)
	}

	groups := s.SyncGroups()
	if len(groups) != 1 {
		t.Fatalf("sync groups = %d, want 1", len(groups))
	}
	if len(groups[0].Tracks) != 2 {
		t.Fatalf("group tracks = %v, want the camera's two", groups[0].Tracks)
	}
	if groups[0].Reference != cameraVideo.ID {
		t.Fatalf("group reference = %s, want %s", groups[0].Reference, cameraVideo.ID)
	}
}

// TestRemoveSourceWithSeveralTracks removes a source carrying four tracks and
// expects every one of them to disappear, so no track outlives its source.
func TestRemoveSourceWithSeveralTracks(t *testing.T) {
	s := NewSession("many-tracks")
	src, err := s.AddSource("https://example.test/program.m3u8")
	if err != nil {
		t.Fatalf("add source: %v", err)
	}
	kinds := []MediaKind{MediaVideo, MediaAudio, MediaAudio, MediaSubtitle}
	var tracks []Track
	for i, kind := range kinds {
		track, err := s.AddTrack(src.ID, kind, fmt.Sprintf("track %d", i))
		if err != nil {
			t.Fatalf("add track %d: %v", i, err)
		}
		tracks = append(tracks, track)
	}
	if _, err := s.AddSyncGroup(tracks[0].ID, tracks[0].ID, tracks[1].ID); err != nil {
		t.Fatalf("add sync group: %v", err)
	}

	if err := s.RemoveSource(src.ID); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	if tracks := s.Tracks(); len(tracks) != 0 {
		t.Fatalf("tracks after source removal = %+v, want none", tracks)
	}
	if groups := s.SyncGroups(); len(groups) != 0 {
		t.Fatalf("sync groups after source removal = %+v, want none", groups)
	}
	for _, track := range tracks {
		if _, err := s.Track(track.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("track %s after source removal = %v, want ErrNotFound", track.ID, err)
		}
	}
}

// TestRemovingEveryGroupMemberRemovesGroup removes both sources of a group
// and expects the group itself to disappear rather than stay empty.
func TestRemovingEveryGroupMemberRemovesGroup(t *testing.T) {
	s := NewSession("empty-group")
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	if _, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID); err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	if err := s.RemoveSource(camera.ID); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	if groups := s.SyncGroups(); len(groups) != 0 {
		t.Fatalf("sync groups = %+v, want none", groups)
	}
}

// TestReferenceTrackRemovalMovesReference removes the reference track of a
// three-track group and expects another member to become the reference.
func TestReferenceTrackRemovalMovesReference(t *testing.T) {
	s := NewSession("reference")
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	_, commentaryAudio := addCommentary(t, s)
	if _, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID, commentaryAudio.ID); err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	if err := s.RemoveTrack(cameraVideo.ID); err != nil {
		t.Fatalf("remove reference track: %v", err)
	}
	groups := s.SyncGroups()
	if len(groups) != 1 {
		t.Fatalf("sync groups = %d, want 1", len(groups))
	}
	if groups[0].Reference != cameraAudio.ID {
		t.Fatalf("reference = %s, want %s", groups[0].Reference, cameraAudio.ID)
	}
	if _, err := s.Source(camera.ID); err != nil {
		t.Fatalf("source lost: %v", err)
	}
	if tracks := s.Sources()[0].Tracks; len(tracks) != 1 {
		t.Fatalf("source tracks = %v, want only the camera audio", tracks)
	}
}

// TestTrackBelongsToOneGroupOnly adds a track to a second group and expects
// a conflict without any group being changed.
func TestTrackBelongsToOneGroupOnly(t *testing.T) {
	s := NewSession("conflict")
	_, cameraVideo, cameraAudio := addCamera(t, s)
	_, commentaryAudio := addCommentary(t, s)

	first, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID)
	if err != nil {
		t.Fatalf("first group: %v", err)
	}
	before := s.SyncGroups()
	if _, err := s.AddSyncGroup(commentaryAudio.ID, commentaryAudio.ID, cameraVideo.ID); !errors.Is(err, ErrSyncConflict) {
		t.Fatalf("second group error = %v, want ErrSyncConflict", err)
	}
	if after := s.SyncGroups(); !reflect.DeepEqual(before, after) {
		t.Fatalf("sync groups changed after conflict: %+v -> %+v", before, after)
	}
	if len(first.Tracks) != 2 {
		t.Fatalf("first group tracks = %v", first.Tracks)
	}
}

// TestSessionsKeepIndependentIdentifiers gives two sessions the same objects
// and expects them to stay separate even though identifier names repeat: the
// names are positional per session, so a name only means something inside the
// session that issued it.
func TestSessionsKeepIndependentIdentifiers(t *testing.T) {
	first := NewSession("first")
	firstCamera, firstVideo, _ := addCamera(t, first)
	second := NewSession("second")
	secondCamera, secondVideo, _ := addCamera(t, second)

	if firstCamera.ID != secondCamera.ID || firstVideo.ID != secondVideo.ID {
		t.Fatalf("identifier names differ between sessions: %s/%s and %s/%s",
			firstCamera.ID, firstVideo.ID, secondCamera.ID, secondVideo.ID)
	}

	if err := first.RemoveSource(firstCamera.ID); err != nil {
		t.Fatalf("remove first camera: %v", err)
	}
	if _, err := first.Track(firstVideo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first session track after removal = %v, want ErrNotFound", err)
	}
	if _, err := second.Track(secondVideo.ID); err != nil {
		t.Fatalf("second session track lost: %v", err)
	}
	if got, err := second.Source(secondCamera.ID); err != nil {
		t.Fatalf("second session source lost: %v", err)
	} else if len(got.Tracks) != 2 {
		t.Fatalf("second session source tracks = %v, want both", got.Tracks)
	}
	if _, err := first.Source(secondCamera.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first session source lookup = %v, want ErrNotFound", err)
	}
}

// TestInvalidOperationsLeaveStateUnchanged runs every refused operation and
// checks that the configuration and the event log stayed as they were.
func TestInvalidOperationsLeaveStateUnchanged(t *testing.T) {
	s := NewSession("invalid")
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	_, commentaryAudio := addCommentary(t, s)
	scene, err := s.AddScene("mix")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := s.SetSceneVideo(scene.ID, cameraVideo.ID); err != nil {
		t.Fatalf("set video: %v", err)
	}
	if _, err := s.SetSceneAudio(scene.ID, cameraAudio.ID, 1, false); err != nil {
		t.Fatalf("set audio: %v", err)
	}
	if _, err := s.AddOutput(OutputDesktopWindow, scene.ID); err != nil {
		t.Fatalf("add output: %v", err)
	}

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"track on unknown source", func() error {
			_, err := s.AddTrack("source-404", MediaVideo, "missing")
			return err
		}, ErrNotFound},
		{"track with unknown kind", func() error {
			_, err := s.AddTrack(camera.ID, MediaKind(99), "odd")
			return err
		}, ErrMediaKind},
		{"audio track shown as video", func() error {
			_, err := s.SetSceneVideo(scene.ID, cameraAudio.ID)
			return err
		}, ErrMediaKind},
		{"video track mixed as audio", func() error {
			_, err := s.SetSceneAudio(scene.ID, cameraVideo.ID, 1, false)
			return err
		}, ErrMediaKind},
		{"volume above one", func() error {
			_, err := s.SetSceneAudio(scene.ID, commentaryAudio.ID, 1.5, false)
			return err
		}, ErrInvalid},
		{"volume below zero", func() error {
			_, err := s.SetSceneAudio(scene.ID, commentaryAudio.ID, -0.1, false)
			return err
		}, ErrInvalid},
		{"scene audio on unknown scene", func() error {
			_, err := s.SetSceneAudio("scene-404", cameraAudio.ID, 1, false)
			return err
		}, ErrNotFound},
		{"duplicate track in scene video", func() error {
			_, err := s.SetSceneVideo(scene.ID, cameraVideo.ID, cameraVideo.ID)
			return err
		}, ErrDuplicateTrack},
		{"unknown video track", func() error {
			_, err := s.SetSceneVideo(scene.ID, "track-404")
			return err
		}, ErrNotFound},
		{"missing reference in group", func() error {
			_, err := s.AddSyncGroup(commentaryAudio.ID, cameraVideo.ID)
			return err
		}, ErrNotMember},
		{"duplicate track in group", func() error {
			_, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraVideo.ID)
			return err
		}, ErrDuplicateTrack},
		{"empty group", func() error {
			_, err := s.AddSyncGroup(cameraVideo.ID)
			return err
		}, ErrInvalid},
		{"output to unknown scene", func() error {
			_, err := s.AddOutput(OutputDesktopWindow, "scene-404")
			return err
		}, ErrNotFound},
		{"output with unknown kind", func() error {
			_, err := s.AddOutput(OutputKind("printer"), scene.ID)
			return err
		}, ErrInvalid},
		{"remove unknown source", func() error {
			return s.RemoveSource("source-404")
		}, ErrNotFound},
		{"remove unknown track", func() error {
			return s.RemoveTrack("track-404")
		}, ErrNotFound},
		{"remove unknown scene", func() error {
			return s.RemoveScene("scene-404")
		}, ErrNotFound},
		{"remove unknown output", func() error {
			return s.RemoveOutput("output-404")
		}, ErrNotFound},
		{"remove scene used by an output", func() error {
			return s.RemoveScene(scene.ID)
		}, ErrInvalid},
		{"remove audio track absent from scene", func() error {
			_, err := s.RemoveSceneAudio(scene.ID, commentaryAudio.ID)
			return err
		}, ErrNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := s.Sources()
			tracks := s.Tracks()
			scenes := s.Scenes()
			groups := s.SyncGroups()
			outputs := s.Outputs()
			events := s.Events()

			err := tc.run()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}

			if !reflect.DeepEqual(sources, s.Sources()) {
				t.Fatalf("sources changed: %+v -> %+v", sources, s.Sources())
			}
			if !reflect.DeepEqual(tracks, s.Tracks()) {
				t.Fatalf("tracks changed: %+v -> %+v", tracks, s.Tracks())
			}
			if !reflect.DeepEqual(scenes, s.Scenes()) {
				t.Fatalf("scenes changed: %+v -> %+v", scenes, s.Scenes())
			}
			if !reflect.DeepEqual(groups, s.SyncGroups()) {
				t.Fatalf("sync groups changed: %+v -> %+v", groups, s.SyncGroups())
			}
			if !reflect.DeepEqual(outputs, s.Outputs()) {
				t.Fatalf("outputs changed: %+v -> %+v", outputs, s.Outputs())
			}
			if !reflect.DeepEqual(events, s.Events()) {
				t.Fatalf("events changed: %+v -> %+v", events, s.Events())
			}
		})
	}
}

// TestClosedSession checks that closing ends mutations, that closing twice is
// harmless, and that reading the configuration still works.
func TestClosedSession(t *testing.T) {
	s := NewSession("closing")
	_, cameraVideo, _ := addCamera(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !s.Closed() {
		t.Fatal("closed session reports open")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := s.AddSource("https://example.test/late.m3u8"); !errors.Is(err, ErrClosed) {
		t.Fatalf("add source after close = %v, want ErrClosed", err)
	}
	if _, err := s.AddScene("late"); !errors.Is(err, ErrClosed) {
		t.Fatalf("add scene after close = %v, want ErrClosed", err)
	}
	if err := s.RemoveTrack(cameraVideo.ID); !errors.Is(err, ErrClosed) {
		t.Fatalf("remove track after close = %v, want ErrClosed", err)
	}
	if tracks := s.Tracks(); len(tracks) != 2 {
		t.Fatalf("tracks after close = %d, want 2", len(tracks))
	}
	if groups := s.SyncGroups(); len(groups) != 0 {
		t.Fatalf("sync groups after close = %+v", groups)
	}
}

// TestSourceURIChecks covers the addresses a source accepts and refuses.
func TestSourceURIChecks(t *testing.T) {
	s := NewSession("uris")

	cases := []struct {
		uri  string
		want error
	}{
		{"", ErrEmptyURI},
		{"   ", ErrEmptyURI},
		{"https://user:secret@example.test/stream.m3u8", ErrCredentialsInURI},
		{"http://example.test/stream.m3u8", nil},
		{"https://example.test/stream.m3u8", nil},
		{"file:///media/sample.mp4", nil},
		{`C:\media\sample.mp4`, nil},
	}
	for _, tc := range cases {
		_, err := s.AddSource(tc.uri)
		if tc.want == nil {
			if err != nil {
				t.Fatalf("add source %q: %v", tc.uri, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("add source %q error = %v, want %v", tc.uri, err, tc.want)
		}
	}
	if sources := s.Sources(); len(sources) != 4 {
		t.Fatalf("sources = %d, want 4", len(sources))
	}
}

// TestSnapshotsAreCopies changes objects returned by the Session and expects
// the Session to keep its own state.
func TestSnapshotsAreCopies(t *testing.T) {
	s := NewSession("copies")
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	scene, err := s.AddScene("mix")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	updated, err := s.SetSceneVideo(scene.ID, cameraVideo.ID)
	if err != nil {
		t.Fatalf("set video: %v", err)
	}
	if _, err := s.SetSceneAudio(updated.ID, cameraAudio.ID, 1, false); err != nil {
		t.Fatalf("set audio: %v", err)
	}

	snapshot, err := s.Scene(scene.ID)
	if err != nil {
		t.Fatalf("read scene: %v", err)
	}
	snapshot.Video = append(snapshot.Video, "track-999")
	snapshot.Audio[0].Volume = 0.01
	if got, err := s.Scene(scene.ID); err != nil {
		t.Fatalf("read scene again: %v", err)
	} else if len(got.Video) != 1 || got.Audio[0].Volume != 1 {
		t.Fatalf("session scene changed through a snapshot: %+v", got)
	}

	sources := s.Sources()
	sources[0].Tracks = append(sources[0].Tracks, "track-999")
	if got, err := s.Source(camera.ID); err != nil {
		t.Fatalf("read source: %v", err)
	} else if len(got.Tracks) != 2 {
		t.Fatalf("session source changed through a snapshot: %+v", got)
	}
}

// TestEventsRecordSuccessfulChanges checks that the event log holds one entry
// per applied change, in order, and names the object.
func TestEventsRecordSuccessfulChanges(t *testing.T) {
	s := NewSession("events")
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	group, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID)
	if err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	scene, err := s.AddScene("mix")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := s.SetSceneVideo(scene.ID, cameraVideo.ID); err != nil {
		t.Fatalf("set video: %v", err)
	}
	if _, err := s.SetSceneAudio(scene.ID, cameraAudio.ID, 0.5, false); err != nil {
		t.Fatalf("set audio: %v", err)
	}
	if _, err := s.AddOutput(OutputWebPage, scene.ID); err != nil {
		t.Fatalf("add output: %v", err)
	}

	events := s.Events()
	want := []struct {
		kind   EventKind
		object string
	}{
		{EventSourceAdded, string(camera.ID)},
		{EventTrackAdded, string(cameraVideo.ID)},
		{EventTrackAdded, string(cameraAudio.ID)},
		{EventSyncGroupAdded, string(group.ID)},
		{EventSceneAdded, string(scene.ID)},
		{EventSceneVideoSet, string(scene.ID)},
		{EventSceneAudioSet, string(scene.ID)},
		{EventOutputAdded, "output-1"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(events), len(want), events)
	}
	for i, w := range want {
		if events[i].Kind != w.kind || events[i].Object != w.object {
			t.Fatalf("event %d = %+v, want {%s %s}", i, events[i], w.kind, w.object)
		}
		if events[i].Seq != uint64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, events[i].Seq, i+1)
		}
	}

	// A refused change adds no event.
	if _, err := s.SetSceneVideo(scene.ID, cameraAudio.ID); !errors.Is(err, ErrMediaKind) {
		t.Fatalf("refused change error = %v, want ErrMediaKind", err)
	}
	if got := len(s.Events()); got != len(want) {
		t.Fatalf("events after refusal = %d, want %d", got, len(want))
	}
}

// TestRemovalPaths removes one audio level, one output, one scene and one
// sync group, and expects each removal to be recorded and to leave the rest
// of the configuration alone.
func TestRemovalPaths(t *testing.T) {
	s := NewSession("removals")
	if s.Name() != "removals" {
		t.Fatalf("name = %q, want removals", s.Name())
	}
	camera, cameraVideo, cameraAudio := addCamera(t, s)
	_, commentaryAudio := addCommentary(t, s)
	group, err := s.AddSyncGroup(cameraVideo.ID, cameraVideo.ID, cameraAudio.ID)
	if err != nil {
		t.Fatalf("add sync group: %v", err)
	}
	scene, err := s.AddScene("mix")
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if _, err := s.SetSceneVideo(scene.ID, cameraVideo.ID); err != nil {
		t.Fatalf("set video: %v", err)
	}
	if _, err := s.SetSceneAudio(scene.ID, cameraAudio.ID, 1, false); err != nil {
		t.Fatalf("set audio: %v", err)
	}
	output, err := s.AddOutput(OutputWebPage, scene.ID)
	if err != nil {
		t.Fatalf("add output: %v", err)
	}

	level, err := s.RemoveSceneAudio(scene.ID, cameraAudio.ID)
	if err != nil {
		t.Fatalf("remove scene audio: %v", err)
	}
	if len(level.Audio) != 0 {
		t.Fatalf("scene audio after removal = %+v", level.Audio)
	}
	if _, err := s.Track(cameraAudio.ID); err != nil {
		t.Fatalf("removing an audio level removed the track: %v", err)
	}

	if err := s.RemoveOutput(output.ID); err != nil {
		t.Fatalf("remove output: %v", err)
	}
	if outputs := s.Outputs(); len(outputs) != 0 {
		t.Fatalf("outputs after removal = %+v", outputs)
	}

	if err := s.RemoveScene(scene.ID); err != nil {
		t.Fatalf("remove scene: %v", err)
	}
	if scenes := s.Scenes(); len(scenes) != 0 {
		t.Fatalf("scenes after removal = %+v", scenes)
	}
	if _, err := s.Scene(scene.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed scene lookup = %v, want ErrNotFound", err)
	}

	if err := s.RemoveSyncGroup(group.ID); err != nil {
		t.Fatalf("remove sync group: %v", err)
	}
	if groups := s.SyncGroups(); len(groups) != 0 {
		t.Fatalf("sync groups after removal = %+v", groups)
	}
	if tracks := s.Tracks(); len(tracks) != 3 {
		t.Fatalf("tracks after group removal = %d, want 3", len(tracks))
	}
	if _, err := s.Track(commentaryAudio.ID); err != nil {
		t.Fatalf("commentary track lost: %v", err)
	}
	if _, err := s.Source(camera.ID); err != nil {
		t.Fatalf("camera source lost: %v", err)
	}

	events := s.Events()
	want := []EventKind{
		EventSourceAdded, EventTrackAdded, EventTrackAdded,
		EventSourceAdded, EventTrackAdded,
		EventSyncGroupAdded,
		EventSceneAdded, EventSceneVideoSet, EventSceneAudioSet,
		EventOutputAdded,
		EventSceneAudioRemoved,
		EventOutputRemoved,
		EventSceneRemoved,
		EventSyncGroupRemoved,
	}
	if len(events) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(events), len(want), events)
	}
	for i, kind := range want {
		if events[i].Kind != kind {
			t.Fatalf("event %d = %s, want %s", i, events[i].Kind, kind)
		}
	}
	if events[len(events)-1].Object != string(group.ID) {
		t.Fatalf("last event object = %q, want %q", events[len(events)-1].Object, group.ID)
	}
}

// TestKindNames fixes the names media kinds and output capabilities are
// reported under, so a client that stores them can rely on the spelling.
func TestKindNames(t *testing.T) {
	names := map[MediaKind]string{
		MediaVideo:    "video",
		MediaAudio:    "audio",
		MediaSubtitle: "subtitle",
		MediaKind(99): "MediaKind(99)",
		MediaKind(0):  "MediaKind(0)",
		MediaKind(-1): "MediaKind(-1)",
	}
	for kind, want := range names {
		if got := kind.String(); got != want {
			t.Fatalf("MediaKind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
	if OutputDesktopWindow != "desktop-window" {
		t.Fatalf("desktop output name = %q", OutputDesktopWindow)
	}
	if OutputWebPage != "web-page" {
		t.Fatalf("web output name = %q", OutputWebPage)
	}
}

// addCamera adds a camera source with a video and an audio track.
func addCamera(t *testing.T, s *Session) (Source, Track, Track) {
	t.Helper()
	src, err := s.AddSource("https://example.test/camera.m3u8")
	if err != nil {
		t.Fatalf("add camera source: %v", err)
	}
	video, err := s.AddTrack(src.ID, MediaVideo, "camera")
	if err != nil {
		t.Fatalf("add camera video: %v", err)
	}
	audio, err := s.AddTrack(src.ID, MediaAudio, "ambient")
	if err != nil {
		t.Fatalf("add camera audio: %v", err)
	}
	return src, video, audio
}

// addCommentary adds a commentary source with one audio track.
func addCommentary(t *testing.T, s *Session) (Source, Track) {
	t.Helper()
	src, err := s.AddSource("https://example.test/commentary.m3u8")
	if err != nil {
		t.Fatalf("add commentary source: %v", err)
	}
	audio, err := s.AddTrack(src.ID, MediaAudio, "commentary")
	if err != nil {
		t.Fatalf("add commentary track: %v", err)
	}
	return src, audio
}
