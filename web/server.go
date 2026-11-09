// Command web serves the MuxShift web client and the session it reads.
//
// The client is a static page in this directory; this program serves it, the
// test media it plays, and a small JSON view of one Core session. Everything is
// bound to the loopback interface, and every request to the session or to the
// media has to carry the token printed at startup, so a page from somewhere else
// cannot drive this process.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/xswt442-cmd/muxshift/core"
)

//go:embed index.html app.js styles.css vendor
var assets embed.FS

// mediaTypes maps the file endings the client plays to the type the browser
// needs; Go's default table knows neither of them on Windows.
var mediaTypes = map[string]string{
	".m3u8": "application/vnd.apple.mpegurl",
	".ts":   "video/mp2t",
	".mp4":  "video/mp4",
}

// source is one media address the page can show, together with the Core tracks
// that were registered for it.
type source struct {
	ID    core.SourceID
	URI   string
	Video core.TrackID
	Audio core.TrackID
}

// sessionState is the piece of one Core session this client can see and change.
// The player objects live in the browser; the session only decides which source
// is shown, which one is heard, and at what level.
type sessionState struct {
	mu      sync.Mutex
	session *core.Session
	scene   core.SceneID
	sources []source
}

func newSessionState(uris []string) (*sessionState, error) {
	session := core.NewSession("web")
	scene, err := session.AddScene("main")
	if err != nil {
		return nil, err
	}
	if _, err := session.AddOutput(core.OutputWebPage, scene.ID); err != nil {
		return nil, err
	}
	state := &sessionState{session: session, scene: scene.ID}
	for _, uri := range uris {
		src, err := session.AddSource(uri)
		if err != nil {
			return nil, err
		}
		video, err := session.AddTrack(src.ID, core.MediaVideo, "video (the player does not enumerate tracks)")
		if err != nil {
			return nil, err
		}
		audio, err := session.AddTrack(src.ID, core.MediaAudio, "audio (the player does not enumerate tracks)")
		if err != nil {
			return nil, err
		}
		state.sources = append(state.sources, source{ID: src.ID, URI: uri, Video: video.ID, Audio: audio.ID})
	}
	if len(state.sources) > 0 {
		if _, err := session.SetSceneVideo(scene.ID, state.sources[0].Video); err != nil {
			return nil, err
		}
		if _, err := session.SetSceneAudio(scene.ID, state.sources[0].Audio, 1, false); err != nil {
			return nil, err
		}
	}
	return state, nil
}

// view is what the page receives: the session's sources and the scene it
// currently presents.
type view struct {
	Session string    `json:"session"`
	Scene   string    `json:"scene"`
	Video   string    `json:"videoSource"`
	Sources []srcView `json:"sources"`
}

type srcView struct {
	ID     string  `json:"id"`
	URI    string  `json:"uri"`
	URL    string  `json:"url"`
	Video  string  `json:"video"`
	Audio  string  `json:"audio"`
	Shown  bool    `json:"shown"`
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted"`
}

func (s *sessionState) snapshot() (view, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// snapshotLocked builds the view of the session; the caller holds the lock.
func (s *sessionState) snapshotLocked() (view, error) {
	current, err := s.session.Scene(s.scene)
	if err != nil {
		return view{}, err
	}
	shown := map[core.TrackID]bool{}
	for _, track := range current.Video {
		shown[track] = true
	}
	levels := map[core.TrackID]core.AudioLevel{}
	for _, level := range current.Audio {
		levels[level.Track] = level
	}
	out := view{Session: string(s.session.Name()), Scene: string(current.ID)}
	for _, src := range s.sources {
		entry := srcView{ID: string(src.ID), URI: src.URI, URL: mediaURL(src.URI), Video: string(src.Video), Audio: string(src.Audio)}
		entry.Shown = shown[src.Video]
		if level, ok := levels[src.Audio]; ok {
			entry.Volume = level.Volume
			entry.Muted = level.Muted
		} else {
			entry.Muted = true
		}
		if entry.Shown {
			out.Video = entry.ID
		}
		out.Sources = append(out.Sources, entry)
	}
	return out, nil
}

// changeScene applies one client request: which source is shown, which one is
// heard, and at what level. Sources that are not named are muted, which is the
// same rule the desktop client applies.
type changeScene struct {
	Video  string  `json:"video"`
	Audio  string  `json:"audio"`
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted"`
}

func (s *sessionState) change(request changeScene) (view, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var video, audio *source
	for i := range s.sources {
		if string(s.sources[i].ID) == request.Video {
			video = &s.sources[i]
		}
		if string(s.sources[i].ID) == request.Audio {
			audio = &s.sources[i]
		}
	}
	if video == nil || audio == nil {
		return view{}, fmt.Errorf("unknown source in the request")
	}
	if _, err := s.session.SetSceneVideo(s.scene, video.Video); err != nil {
		return view{}, err
	}
	current, err := s.session.Scene(s.scene)
	if err != nil {
		return view{}, err
	}
	for _, level := range current.Audio {
		if level.Track != audio.Audio {
			if _, err := s.session.RemoveSceneAudio(s.scene, level.Track); err != nil {
				return view{}, err
			}
		}
	}
	if _, err := s.session.SetSceneAudio(s.scene, audio.Audio, request.Volume, request.Muted); err != nil {
		return view{}, err
	}
	return s.snapshotLocked()
}

// mediaURL turns one address into the path the page asks this server for.
func mediaURL(uri string) string {
	name := strings.TrimPrefix(uri, "/media/")
	return "/media/" + name
}

func main() {
	mediaRoot := flag.String("media-root", "", "directory the demo media is read from")
	addr := flag.String("addr", "127.0.0.1:0", "address to listen on")
	token := flag.String("token", "", "token to require; a random one is generated when empty")
	var media multiFlag
	flag.Var(&media, "media", "path under -media-root to offer as one source; may be repeated")
	flag.Parse()
	if *mediaRoot == "" || len(media) == 0 {
		log.Fatal("muxshift-web: -media-root and at least one -media are required")
	}
	root, err := filepath.Abs(*mediaRoot)
	if err != nil {
		log.Fatalf("muxshift-web: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		log.Fatalf("muxshift-web: %v", err)
	}
	state, err := newSessionState(media)
	if err != nil {
		log.Fatalf("muxshift-web: %v", err)
	}
	tokenValue := *token
	if tokenValue == "" {
		buffer := make([]byte, 16)
		if _, err := rand.Read(buffer); err != nil {
			log.Fatalf("muxshift-web: %v", err)
		}
		tokenValue = hex.EncodeToString(buffer)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("muxshift-web: %v", err)
	}
	mux := http.NewServeMux()
	pages, err := fs.Sub(assets, ".")
	if err != nil {
		log.Fatalf("muxshift-web: %v", err)
	}
	// The page is served with the token in a cookie so the media elements and
	// hls.js can fetch the stream without appending a query parameter to every
	// playlist and segment.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if authorized(r, tokenValue) {
			http.SetCookie(w, &http.Cookie{
				Name:     "muxshift_token",
				Value:    tokenValue,
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				HttpOnly: true,
			})
		}
		http.FileServer(http.FS(pages)).ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, tokenValue) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshot, err := state.snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, snapshot)
	})
	mux.HandleFunc("/api/scene", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, tokenValue) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request changeScene
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		snapshot, err := state.change(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, snapshot)
	})
	mux.HandleFunc("/media/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, tokenValue) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		serveMedia(w, r, root)
	})
	server := &http.Server{Handler: mux}
	log.Printf("web: listening on http://%s/?token=%s", listener.Addr().String(), tokenValue)
	if err := server.Serve(listener); err != nil {
		log.Fatalf("muxshift-web: %v", err)
	}
}

// authorized compares the request token without leaking its length or content
// through timing. The token may arrive as a query parameter, a header, or the
// cookie the page sets, because media elements and hls.js cannot add headers.
func authorized(r *http.Request, token string) bool {
	got := r.URL.Query().Get("token")
	if got == "" {
		got = r.Header.Get("X-MuxShift-Token")
	}
	if got == "" {
		if cookie, err := r.Cookie("muxshift_token"); err == nil {
			got = cookie.Value
		}
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// serveMedia serves one file under the media root and refuses anything that
// resolves outside it, so the client cannot ask this process for other files.
func serveMedia(w http.ResponseWriter, r *http.Request, root string) {
	name := strings.TrimPrefix(r.URL.Path, "/media/")
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(root, cleaned)
	if !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if kind, ok := mediaTypes[strings.ToLower(filepath.Ext(full))]; ok {
		w.Header().Set("Content-Type", kind)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, full)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Printf("web: encode: %v", err)
	}
}

// multiFlag collects one address per -media flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
