// Command muxshift-desktop shows media sources in a MyGo window.
//
// It exists to find out what the native video path of MyGo actually does before
// anything is built on it: open local files and HLS addresses, play, pause,
// mute, switch between sources, close them, and report the real state of each
// player rather than an assumed one.
//
//	go run . -media <file-or-url> [-media <another>]
//
// -media accepts a local path or an http(s) address and may be given more than
// once. -shot writes a PNG of the window and quits, so playback on a machine
// without a person watching can still be checked. -scenario runs one of the
// scripted checks in scenarios.go.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"
)

// source is one media address, the player opened for it, and what stopped it
// from opening.
type source struct {
	uri     string
	player  *ui.VideoPlayer
	openErr error
	shown   bool
}

// state is every source the window knows, in the order they were given.
type state struct {
	sources []*source
}

// add opens uri and keeps its player, so the window can show it.
func (s *state) add(uri string) *source {
	src := &source{uri: uri}
	s.open(src)
	s.sources = append(s.sources, src)
	return src
}

// open opens the player of src, replacing one it had.
func (s *state) open(src *source) {
	s.close(src)
	player, err := openPlayer(src.uri)
	if err != nil {
		src.openErr = err
		return
	}
	src.openErr = nil
	src.player = player
	src.shown = true
	if isNetworkAddress(src.uri) {
		wakeNetworkPlayer(player)
	}
}

// close stops showing the player of src and then closes it.
//
// The order matters: a player closed while a frame still draws it crashes the
// process in Media Foundation, so the view drops it first, a frame goes by, and
// only then the player is closed. Both steps run on the main thread, where
// drawing happens.
func (s *state) close(src *source) {
	if src.player == nil {
		return
	}
	mygo.RunOnMain(func() { src.shown = false })
	time.Sleep(100 * time.Millisecond)
	p := src.player
	src.player = nil
	mygo.RunOnMain(func() { p.Close() })
}

// release closes every player.
func (s *state) release() {
	for _, src := range s.sources {
		s.close(src)
	}
}

// openPlayer opens a local file or a network address.
func openPlayer(uri string) (*ui.VideoPlayer, error) {
	if isNetworkAddress(uri) {
		return video.OpenURL(uri)
	}
	return video.Open(uri)
}

// isNetworkAddress reports whether uri is something to stream rather than a
// file on this machine.
func isNetworkAddress(uri string) bool {
	return strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://")
}

// wakeNetworkPlayer nudges a network player once its first second went by
// without a picture.
//
// In this MyGo build a network source plays, reports its duration and moves its
// position, but its first picture never reaches the window: the player stays
// not ready with a black area, and a seek is what makes Media Foundation hand a
// frame over. A seek to just ahead of the current position keeps the playhead
// where it was and is enough. A second video in the same window does it too,
// which is why the P1 probe, which showed a local file first, did not see this.
func wakeNetworkPlayer(player *ui.VideoPlayer) {
	go func() {
		switch wakeMode {
		case "none":
			return
		case "once":
			time.Sleep(1500 * time.Millisecond)
			mygo.RunOnMain(func() {
				if player.Ready() || player.Err() != nil {
					return
				}
				player.Seek(player.Position() + time.Millisecond)
			})
			return
		}
		// One seek did not always bring the frame in the same second, so try a
		// few times, half a second apart, and stop as soon as it arrives.
		for i := 0; i < 8; i++ {
			time.Sleep(500 * time.Millisecond)
			done := make(chan bool, 1)
			mygo.RunOnMain(func() {
				if player.Ready() || player.Err() != nil {
					done <- true
					return
				}
				player.Seek(player.Position() + time.Millisecond)
				done <- false
			})
			if <-done {
				return
			}
		}
	}()
}

// wakeMode selects how a network player is nudged into showing its first
// frame: once, retrying, or not at all.
var wakeMode = "retry"

// status is one player's state as a line, so the window reports what actually
// happened: an opening error, buffering, or the position it reached.
func status(src *source) string {
	switch {
	case src.openErr != nil:
		return "error: " + src.openErr.Error()
	case src.player == nil:
		return "no media"
	case src.player.Err() != nil:
		return "error: " + src.player.Err().Error()
	case !src.player.Ready():
		return "opening"
	case src.player.Buffering():
		return "buffering"
	}
	return fmt.Sprintf("%s / %s", clock(src.player.Position()), clock(src.player.Duration()))
}

// clock is a duration as minutes and seconds.
func clock(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// view builds the window's interface from the state on every frame.
//
// The videos sit directly in the column. A video nested in a Box did not get
// its first picture for a network source in this MyGo build: the player stayed
// not ready with a black area while its clock ran, and the same address played
// once the video was a direct child.
func (s *state) view(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		shown := 0
		for _, src := range s.sources {
			if src.shown && src.player != nil {
				ui.Video(c, src.player).Fill()
				shown++
			}
		}
		if shown == 0 && len(s.sources) > 0 {
			ui.Text(c, status(s.sources[0])).Center()
		}
		ui.Row(c).Gap(8).Padding(12).AlignItems(ui.Center).Children(func() {
			main := s.first()
			if main != nil && main.player != nil {
				p := main.player
				label := "Pause"
				if !p.Playing() {
					label = "Play"
				}
				if ui.Button(c.Key("play"), label).Width(88).Clicked() {
					if p.Playing() {
						p.Pause()
					} else {
						p.Play()
					}
				}
				if ui.Button(c.Key("mute"), muteLabel(p)).Width(88).Clicked() {
					p.SetMuted(!p.Muted())
				}
			}
			if ui.Button(c.Key("reload"), "Reload").Width(88).Clicked() && main != nil {
				s.open(main)
			}
			if ui.Button(c.Key("quit"), "Close").Width(88).Clicked() {
				s.release()
				mygo.App.Quit()
			}
			ui.Text(c, s.status()).FontFeatures("tnum")
		})
	})
}

// first is the source the buttons drive: the first one given.
func (s *state) first() *source {
	if len(s.sources) == 0 {
		return nil
	}
	return s.sources[0]
}

// status is every source as one line, with its index.
func (s *state) status() string {
	switch len(s.sources) {
	case 0:
		return "no media"
	case 1:
		return status(s.sources[0])
	}
	parts := make([]string, 0, len(s.sources))
	for i, src := range s.sources {
		parts = append(parts, fmt.Sprintf("%d: %s", i+1, status(src)))
	}
	return strings.Join(parts, "   ")
}

// muteLabel names what the mute button does next.
func muteLabel(p *ui.VideoPlayer) string {
	if p.Muted() {
		return "Unmute"
	}
	return "Mute"
}

// capture writes a PNG of the window, or reports why it could not.
//
// It recovers from a panic in the rasteriser: with more than one video in the
// window, MyGo's read-back of a frame that was never delivered panics with
// "slice bounds out of range [:2560] with capacity 0" in mfEngine.readBack
// (mf_windows.go:574, reached from Bitmap.showPicture and raster.restorePixels).
// Recovering keeps the rest of a check running, and the failure is reported
// rather than hidden.
func capture(window *mygo.Window, path string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("capture panicked: %v", r)
		}
	}()
	png, err := window.CapturePage()
	if err == nil {
		err = os.WriteFile(path, png, 0o644)
	}
	if err != nil {
		log.Printf("capture %s: %v", path, err)
		return err
	}
	log.Printf("captured %s", path)
	return nil
}

func main() {
	var media multiFlag
	flag.Var(&media, "media", "local file or http(s) address to play; may be repeated")
	shot := flag.String("shot", "", "write a PNG of the window here and quit")
	shotDelay := flag.Duration("shot-delay", 6*time.Second, "how long to let the first picture arrive before -shot")
	trace := flag.Bool("trace", false, "log every player's state twice a second")
	scenario := flag.String("scenario", "", "run a scripted check: "+scenarioNames())
	dir := flag.String("dir", "", "directory the scenario writes its PNGs into")
	flag.StringVar(&wakeMode, "wake", wakeMode, "how to nudge a network player into its first frame: once, retry, none")
	flag.Parse()
	if len(media) == 0 {
		log.Fatal("muxshift-desktop: -media is required")
	}
	for _, uri := range media {
		if !isNetworkAddress(uri) {
			if _, err := os.Stat(uri); err != nil {
				log.Fatalf("muxshift-desktop: %v", err)
			}
		}
	}
	if *scenario != "" && *dir == "" {
		log.Fatal("muxshift-desktop: -dir is required with -scenario")
	}
	if *scenario != "" {
		if _, ok := scenarios[*scenario]; !ok {
			log.Fatalf("muxshift-desktop: unknown scenario %q, want %s", *scenario, scenarioNames())
		}
	}

	s := &state{}
	for _, uri := range media {
		s.add(uri)
	}
	if *trace {
		go func() {
			for range time.Tick(500 * time.Millisecond) {
				for i, src := range s.sources {
					if src.player == nil {
						log.Printf("trace %d: no player", i+1)
						continue
					}
					p := src.player
					log.Printf("trace %d: ready=%v playing=%v buffering=%v duration=%s position=%s err=%v",
						i+1, p.Ready(), p.Playing(), p.Buffering(), p.Duration(), p.Position(), p.Err())
				}
			}
		}()
	}

	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{
			Title:   "MuxShift",
			Width:   960,
			Height:  600,
			Content: ui.View(s.view),
		})
		window.OnClose(func(*mygo.CloseEvent) { s.release() })
		if *shot != "" {
			time.AfterFunc(*shotDelay, func() {
				// The window and the players belong to the main thread: capturing and
				// closing from the timer's goroutine races a frame that is drawing.
				mygo.RunOnMain(func() {
					capture(window, *shot)
					s.release()
					mygo.App.Quit()
				})
			})
		}
		if *scenario != "" {
			scenarios[*scenario](window, s, *dir)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// multiFlag collects a flag given more than once, in order.
type multiFlag []string

// String is the addresses joined for printing.
func (m *multiFlag) String() string { return strings.Join(*m, ", ") }

// Set adds one address.
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
