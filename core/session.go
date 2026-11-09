package core

import (
	"fmt"
	"net/url"
	"strings"
)

// MediaKind is the kind of media a Track carries.
type MediaKind int

// Media kinds a Track can carry.
const (
	MediaVideo MediaKind = iota + 1
	MediaAudio
	MediaSubtitle
)

// String returns the kind's name.
func (k MediaKind) String() string {
	switch k {
	case MediaVideo:
		return "video"
	case MediaAudio:
		return "audio"
	case MediaSubtitle:
		return "subtitle"
	}
	return fmt.Sprintf("MediaKind(%d)", int(k))
}

// valid reports whether the kind is one a Track can carry.
func (k MediaKind) valid() bool {
	return k == MediaVideo || k == MediaAudio || k == MediaSubtitle
}

// OutputKind is the output capability a Scene is handed to.
type OutputKind string

// Output capabilities MuxShift declares.
const (
	// OutputDesktopWindow is the desktop player's window.
	OutputDesktopWindow OutputKind = "desktop-window"
	// OutputWebPage is a browser page playing the same session.
	OutputWebPage OutputKind = "web-page"
)

// Source is one playable address and the tracks read from it.
type Source struct {
	ID     SourceID
	URI    string
	Tracks []TrackID
}

// Track is one audio, video or subtitle track of a Source.
type Track struct {
	ID     TrackID
	Source SourceID
	Kind   MediaKind
	Label  string
}

// SyncGroup is a set of tracks that play together on a shared event clock.
// Reference is the track whose media time the others are mapped against.
type SyncGroup struct {
	ID        SyncGroupID
	Reference TrackID
	Tracks    []TrackID
}

// AudioLevel is one audio track's level inside a Scene.
type AudioLevel struct {
	Track  TrackID
	Volume float64
	Muted  bool
}

// Scene is what is shown and heard: the video tracks laid out, and the audio
// tracks mixed at their own levels.
type Scene struct {
	ID    SceneID
	Name  string
	Video []TrackID
	Audio []AudioLevel
}

// Output records which capability a Scene is handed to.
type Output struct {
	ID    OutputID
	Kind  OutputKind
	Scene SceneID
}

// EventKind names a change a Session applied.
type EventKind string

// Changes a Session applies. Only successful operations produce events.
const (
	EventSourceAdded       EventKind = "source.added"
	EventSourceRemoved     EventKind = "source.removed"
	EventTrackAdded        EventKind = "track.added"
	EventTrackRemoved      EventKind = "track.removed"
	EventSyncGroupAdded    EventKind = "syncgroup.added"
	EventSyncGroupRemoved  EventKind = "syncgroup.removed"
	EventSceneAdded        EventKind = "scene.added"
	EventSceneRemoved      EventKind = "scene.removed"
	EventSceneVideoSet     EventKind = "scene.video-set"
	EventSceneAudioSet     EventKind = "scene.audio-set"
	EventSceneAudioRemoved EventKind = "scene.audio-removed"
	EventOutputAdded       EventKind = "output.added"
	EventOutputRemoved     EventKind = "output.removed"
)

// Event is one applied change, in order. Object names the object the change
// concerned.
type Event struct {
	Seq    uint64
	Kind   EventKind
	Object string
}

// Session is one playback activity: the sources, tracks, sync groups, scenes
// and outputs the user configured. It holds configuration, not playback
// state; a media layer reports positions and buffering separately.
//
// Every operation either applies completely or leaves the Session unchanged.
// Objects returned by Session methods are copies: changing them changes
// nothing inside the Session.
type Session struct {
	name   string
	closed bool

	sourceN int
	trackN  int
	groupN  int
	sceneN  int
	outputN int

	eventSeq uint64

	sources []*Source
	tracks  []*Track
	groups  []*SyncGroup
	scenes  []*Scene
	outputs []*Output
	events  []Event
}

// NewSession starts an empty Session with the given name.
func NewSession(name string) *Session {
	return &Session{name: strings.TrimSpace(name)}
}

// Name returns the Session's name.
func (s *Session) Name() string { return s.name }

// Close ends the Session. Mutations after Close fail with ErrClosed; reading
// the configuration still works. Close may be called more than once.
func (s *Session) Close() error {
	s.closed = true
	return nil
}

// Closed reports whether the Session was closed.
func (s *Session) Closed() bool { return s.closed }

// AddSource adds a playable address and returns it.
func (s *Session) AddSource(uri string) (Source, error) {
	if err := s.requireOpen("add source"); err != nil {
		return Source{}, err
	}
	clean, err := cleanURI(uri)
	if err != nil {
		return Source{}, fmt.Errorf("add source: %w", err)
	}
	s.sourceN++
	src := &Source{ID: SourceID(fmt.Sprintf("source-%d", s.sourceN)), URI: clean}
	s.sources = append(s.sources, src)
	s.record(EventSourceAdded, string(src.ID))
	return *src, nil
}

// RemoveSource removes a source, its tracks, and every reference to those
// tracks in sync groups and scenes. A sync group left without tracks is
// removed.
func (s *Session) RemoveSource(id SourceID) error {
	if err := s.requireOpen("remove source"); err != nil {
		return err
	}
	src, ok := s.source(id)
	if !ok {
		return fmt.Errorf("remove source %s: %w", id, ErrNotFound)
	}
	for len(src.Tracks) > 0 {
		s.removeTrack(src.Tracks[0])
	}
	s.sources = removeByID(s.sources, func(v *Source) bool { return v.ID == id })
	s.record(EventSourceRemoved, string(id))
	return nil
}

// AddTrack adds a track read from an existing source.
func (s *Session) AddTrack(source SourceID, kind MediaKind, label string) (Track, error) {
	if err := s.requireOpen("add track"); err != nil {
		return Track{}, err
	}
	if !kind.valid() {
		return Track{}, fmt.Errorf("add track to source %s: %w", source, ErrMediaKind)
	}
	src, ok := s.source(source)
	if !ok {
		return Track{}, fmt.Errorf("add track source %s: %w", source, ErrNotFound)
	}
	s.trackN++
	track := &Track{
		ID:     TrackID(fmt.Sprintf("track-%d", s.trackN)),
		Source: source,
		Kind:   kind,
		Label:  strings.TrimSpace(label),
	}
	s.tracks = append(s.tracks, track)
	src.Tracks = append(src.Tracks, track.ID)
	s.record(EventTrackAdded, string(track.ID))
	return *track, nil
}

// RemoveTrack removes a track and every reference to it. A sync group left
// without tracks is removed.
func (s *Session) RemoveTrack(id TrackID) error {
	if err := s.requireOpen("remove track"); err != nil {
		return err
	}
	if _, ok := s.track(id); !ok {
		return fmt.Errorf("remove track %s: %w", id, ErrNotFound)
	}
	s.removeTrack(id)
	s.record(EventTrackRemoved, string(id))
	return nil
}

// AddSyncGroup synchronizes tracks against a reference track chosen among
// them. Each track may belong to at most one group; adding a track that is
// already grouped fails with ErrSyncConflict.
func (s *Session) AddSyncGroup(reference TrackID, tracks ...TrackID) (SyncGroup, error) {
	if err := s.requireOpen("add sync group"); err != nil {
		return SyncGroup{}, err
	}
	if len(tracks) == 0 {
		return SyncGroup{}, fmt.Errorf("add sync group: %w", ErrInvalid)
	}
	seen := make(map[TrackID]bool, len(tracks))
	for _, id := range tracks {
		if seen[id] {
			return SyncGroup{}, fmt.Errorf("add sync group track %s: %w", id, ErrDuplicateTrack)
		}
		seen[id] = true
		if _, ok := s.track(id); !ok {
			return SyncGroup{}, fmt.Errorf("add sync group track %s: %w", id, ErrNotFound)
		}
		if _, ok := s.groupOf(id); ok {
			return SyncGroup{}, fmt.Errorf("add sync group track %s: %w", id, ErrSyncConflict)
		}
	}
	if !seen[reference] {
		return SyncGroup{}, fmt.Errorf("add sync group reference %s: %w", reference, ErrNotMember)
	}

	s.groupN++
	group := &SyncGroup{
		ID:        SyncGroupID(fmt.Sprintf("syncgroup-%d", s.groupN)),
		Reference: reference,
		Tracks:    append([]TrackID(nil), tracks...),
	}
	s.groups = append(s.groups, group)
	s.record(EventSyncGroupAdded, string(group.ID))
	return *group, nil
}

// RemoveSyncGroup removes a sync group without touching its tracks.
func (s *Session) RemoveSyncGroup(id SyncGroupID) error {
	if err := s.requireOpen("remove sync group"); err != nil {
		return err
	}
	if _, ok := s.group(id); !ok {
		return fmt.Errorf("remove sync group %s: %w", id, ErrNotFound)
	}
	s.groups = removeByID(s.groups, func(v *SyncGroup) bool { return v.ID == id })
	s.record(EventSyncGroupRemoved, string(id))
	return nil
}

// AddScene adds a scene that shows and hears nothing yet.
func (s *Session) AddScene(name string) (Scene, error) {
	if err := s.requireOpen("add scene"); err != nil {
		return Scene{}, err
	}
	s.sceneN++
	scene := &Scene{ID: SceneID(fmt.Sprintf("scene-%d", s.sceneN)), Name: strings.TrimSpace(name)}
	s.scenes = append(s.scenes, scene)
	s.record(EventSceneAdded, string(scene.ID))
	return *scene, nil
}

// RemoveScene removes a scene. Removing a scene that an output still hands
// out is refused with ErrInvalid: remove the output first.
func (s *Session) RemoveScene(id SceneID) error {
	if err := s.requireOpen("remove scene"); err != nil {
		return err
	}
	if _, ok := s.scene(id); !ok {
		return fmt.Errorf("remove scene %s: %w", id, ErrNotFound)
	}
	for _, out := range s.outputs {
		if out.Scene == id {
			return fmt.Errorf("remove scene %s: still used by output %s: %w", id, out.ID, ErrInvalid)
		}
	}
	s.scenes = removeByID(s.scenes, func(v *Scene) bool { return v.ID == id })
	s.record(EventSceneRemoved, string(id))
	return nil
}

// SetSceneVideo sets the video tracks a scene shows, in layout order. Every
// track must exist and be a video track.
func (s *Session) SetSceneVideo(id SceneID, tracks ...TrackID) (Scene, error) {
	if err := s.requireOpen("set scene video"); err != nil {
		return Scene{}, err
	}
	scene, ok := s.scene(id)
	if !ok {
		return Scene{}, fmt.Errorf("set scene video %s: %w", id, ErrNotFound)
	}
	seen := make(map[TrackID]bool, len(tracks))
	for _, trackID := range tracks {
		if seen[trackID] {
			return Scene{}, fmt.Errorf("set scene video %s track %s: %w", id, trackID, ErrDuplicateTrack)
		}
		seen[trackID] = true
		track, ok := s.track(trackID)
		if !ok {
			return Scene{}, fmt.Errorf("set scene video %s track %s: %w", id, trackID, ErrNotFound)
		}
		if track.Kind != MediaVideo {
			return Scene{}, fmt.Errorf("set scene video %s track %s: %w", id, trackID, ErrMediaKind)
		}
	}
	scene.Video = append([]TrackID(nil), tracks...)
	s.record(EventSceneVideoSet, string(id))
	return copyScene(scene), nil
}

// SetSceneAudio sets one audio track's level in a scene, adding the track to
// the scene when it was not there yet. Volume must be within 0 and 1.
func (s *Session) SetSceneAudio(id SceneID, track TrackID, volume float64, muted bool) (Scene, error) {
	if err := s.requireOpen("set scene audio"); err != nil {
		return Scene{}, err
	}
	scene, ok := s.scene(id)
	if !ok {
		return Scene{}, fmt.Errorf("set scene audio %s: %w", id, ErrNotFound)
	}
	t, ok := s.track(track)
	if !ok {
		return Scene{}, fmt.Errorf("set scene audio %s track %s: %w", id, track, ErrNotFound)
	}
	if t.Kind != MediaAudio {
		return Scene{}, fmt.Errorf("set scene audio %s track %s: %w", id, track, ErrMediaKind)
	}
	if volume < 0 || volume > 1 {
		return Scene{}, fmt.Errorf("set scene audio %s track %s: %w", id, track, ErrInvalid)
	}
	level := AudioLevel{Track: track, Volume: volume, Muted: muted}
	replaced := false
	for i := range scene.Audio {
		if scene.Audio[i].Track == track {
			scene.Audio[i] = level
			replaced = true
			break
		}
	}
	if !replaced {
		scene.Audio = append(scene.Audio, level)
	}
	s.record(EventSceneAudioSet, string(id))
	return copyScene(scene), nil
}

// RemoveSceneAudio removes an audio track from a scene's mix.
func (s *Session) RemoveSceneAudio(id SceneID, track TrackID) (Scene, error) {
	if err := s.requireOpen("remove scene audio"); err != nil {
		return Scene{}, err
	}
	scene, ok := s.scene(id)
	if !ok {
		return Scene{}, fmt.Errorf("remove scene audio %s: %w", id, ErrNotFound)
	}
	found := false
	for i := range scene.Audio {
		if scene.Audio[i].Track == track {
			scene.Audio = append(scene.Audio[:i], scene.Audio[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return Scene{}, fmt.Errorf("remove scene audio %s track %s: %w", id, track, ErrNotFound)
	}
	s.record(EventSceneAudioRemoved, string(id))
	return copyScene(scene), nil
}

// AddOutput hands a scene to an output capability.
func (s *Session) AddOutput(kind OutputKind, scene SceneID) (Output, error) {
	if err := s.requireOpen("add output"); err != nil {
		return Output{}, err
	}
	if kind != OutputDesktopWindow && kind != OutputWebPage {
		return Output{}, fmt.Errorf("add output %q: %w", kind, ErrInvalid)
	}
	if _, ok := s.scene(scene); !ok {
		return Output{}, fmt.Errorf("add output scene %s: %w", scene, ErrNotFound)
	}
	s.outputN++
	out := &Output{ID: OutputID(fmt.Sprintf("output-%d", s.outputN)), Kind: kind, Scene: scene}
	s.outputs = append(s.outputs, out)
	s.record(EventOutputAdded, string(out.ID))
	return *out, nil
}

// RemoveOutput stops handing a scene to an output capability.
func (s *Session) RemoveOutput(id OutputID) error {
	if err := s.requireOpen("remove output"); err != nil {
		return err
	}
	if _, ok := s.output(id); !ok {
		return fmt.Errorf("remove output %s: %w", id, ErrNotFound)
	}
	s.outputs = removeByID(s.outputs, func(v *Output) bool { return v.ID == id })
	s.record(EventOutputRemoved, string(id))
	return nil
}

// Source returns one source.
func (s *Session) Source(id SourceID) (Source, error) {
	src, ok := s.source(id)
	if !ok {
		return Source{}, fmt.Errorf("source %s: %w", id, ErrNotFound)
	}
	return *src, nil
}

// Track returns one track.
func (s *Session) Track(id TrackID) (Track, error) {
	track, ok := s.track(id)
	if !ok {
		return Track{}, fmt.Errorf("track %s: %w", id, ErrNotFound)
	}
	return *track, nil
}

// Scene returns one scene.
func (s *Session) Scene(id SceneID) (Scene, error) {
	scene, ok := s.scene(id)
	if !ok {
		return Scene{}, fmt.Errorf("scene %s: %w", id, ErrNotFound)
	}
	return copyScene(scene), nil
}

// Sources returns every source in the order they were added.
func (s *Session) Sources() []Source {
	out := make([]Source, len(s.sources))
	for i, src := range s.sources {
		out[i] = *src
		out[i].Tracks = append([]TrackID(nil), src.Tracks...)
	}
	return out
}

// Tracks returns every track in the order they were added.
func (s *Session) Tracks() []Track {
	out := make([]Track, len(s.tracks))
	for i, track := range s.tracks {
		out[i] = *track
	}
	return out
}

// SyncGroups returns every sync group in the order they were added.
func (s *Session) SyncGroups() []SyncGroup {
	out := make([]SyncGroup, len(s.groups))
	for i, group := range s.groups {
		out[i] = *group
		out[i].Tracks = append([]TrackID(nil), group.Tracks...)
	}
	return out
}

// Scenes returns every scene in the order they were added.
func (s *Session) Scenes() []Scene {
	out := make([]Scene, len(s.scenes))
	for i, scene := range s.scenes {
		out[i] = copyScene(scene)
	}
	return out
}

// Outputs returns every output in the order they were added.
func (s *Session) Outputs() []Output {
	out := make([]Output, len(s.outputs))
	for i, o := range s.outputs {
		out[i] = *o
	}
	return out
}

// Events returns every applied change in order.
func (s *Session) Events() []Event {
	return append([]Event(nil), s.events...)
}

// requireOpen reports a mutation attempted on a closed Session.
func (s *Session) requireOpen(op string) error {
	if s.closed {
		return fmt.Errorf("%s: %w", op, ErrClosed)
	}
	return nil
}

// removeTrack deletes one track and every reference to it. It does not record
// an event: its callers record what the user asked for.
func (s *Session) removeTrack(id TrackID) {
	track, ok := s.track(id)
	if !ok {
		return
	}
	if src, ok := s.source(track.Source); ok {
		src.Tracks = removeValue(src.Tracks, id)
	}
	s.tracks = removeByID(s.tracks, func(v *Track) bool { return v.ID == id })

	for _, scene := range s.scenes {
		scene.Video = removeValue(scene.Video, id)
		for i := range scene.Audio {
			if scene.Audio[i].Track == id {
				scene.Audio = append(scene.Audio[:i], scene.Audio[i+1:]...)
				break
			}
		}
	}

	for i := 0; i < len(s.groups); i++ {
		group := s.groups[i]
		kept := removeValue(group.Tracks, id)
		if len(kept) == 0 {
			s.groups = append(s.groups[:i], s.groups[i+1:]...)
			i--
			continue
		}
		if group.Reference == id {
			group.Reference = kept[0]
		}
		group.Tracks = kept
	}
}

func (s *Session) source(id SourceID) (*Source, bool) {
	for _, src := range s.sources {
		if src.ID == id {
			return src, true
		}
	}
	return nil, false
}

func (s *Session) track(id TrackID) (*Track, bool) {
	for _, track := range s.tracks {
		if track.ID == id {
			return track, true
		}
	}
	return nil, false
}

func (s *Session) group(id SyncGroupID) (*SyncGroup, bool) {
	for _, group := range s.groups {
		if group.ID == id {
			return group, true
		}
	}
	return nil, false
}

func (s *Session) groupOf(id TrackID) (*SyncGroup, bool) {
	for _, group := range s.groups {
		for _, member := range group.Tracks {
			if member == id {
				return group, true
			}
		}
	}
	return nil, false
}

func (s *Session) scene(id SceneID) (*Scene, bool) {
	for _, scene := range s.scenes {
		if scene.ID == id {
			return scene, true
		}
	}
	return nil, false
}

func (s *Session) output(id OutputID) (*Output, bool) {
	for _, out := range s.outputs {
		if out.ID == id {
			return out, true
		}
	}
	return nil, false
}

func (s *Session) record(kind EventKind, object string) {
	s.eventSeq++
	s.events = append(s.events, Event{Seq: s.eventSeq, Kind: kind, Object: object})
}

func copyScene(scene *Scene) Scene {
	out := *scene
	out.Video = append([]TrackID(nil), scene.Video...)
	out.Audio = append([]AudioLevel(nil), scene.Audio...)
	return out
}

func removeByID[T any](items []T, match func(T) bool) []T {
	kept := items[:0]
	for _, item := range items {
		if !match(item) {
			kept = append(kept, item)
		}
	}
	return kept
}

func removeValue(ids []TrackID, id TrackID) []TrackID {
	kept := ids[:0]
	for _, item := range ids {
		if item != id {
			kept = append(kept, item)
		}
	}
	return kept
}

// cleanURI validates a source address and returns it trimmed. A URI that
// carries user information is rejected: configuration never holds credentials.
func cleanURI(uri string) (string, error) {
	clean := strings.TrimSpace(uri)
	if clean == "" {
		return "", ErrEmptyURI
	}
	if strings.Contains(clean, "://") {
		parsed, err := url.Parse(clean)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if parsed.Scheme == "" {
			return "", fmt.Errorf("%w: missing scheme", ErrInvalid)
		}
		if parsed.User != nil {
			return "", ErrCredentialsInURI
		}
	}
	return clean, nil
}
