//go:build windows

package main

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
)

// windowIcon is the .ico committed next to this file. Windows only accepts an
// icon file for LoadImage, so the bytes go to a temporary file, the handle is
// loaded from there, and the file is removed again: the returned handle owns
// its own pixels, not the path.
//
//go:embed icon.ico
var windowIcon []byte

const (
	wmSetIcon      = 0x0080
	wmGetIcon      = 0x007F
	iconSmall      = 0
	iconBig        = 1
	imageIcon      = 1
	lrLoadFromFile = 0x0010
)

var (
	user32      = syscall.NewLazyDLL("user32.dll")
	loadImage   = user32.NewProc("LoadImageW")
	sendMessage = user32.NewProc("SendMessageW")
)

// installWindowIcon puts the project icon on the window's title bar and
// taskbar button. MyGo has no option for this, so the window handle it
// exposes is used directly; a failure is logged and does not stop the app.
func installWindowIcon(window *mygo.Window) {
	path := filepath.Join(os.TempDir(), "muxshift-window.ico")
	if err := os.WriteFile(path, windowIcon, 0o600); err != nil {
		log.Printf("icon: write %s: %v", path, err)
		return
	}
	defer os.Remove(path)

	handle, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		log.Printf("icon: %v", err)
		return
	}
	hwnd := window.NativeHandle()
	if hwnd == 0 {
		log.Printf("icon: the window has no handle yet")
		return
	}
	for _, slot := range []struct {
		kind uintptr
		size int32
	}{
		{iconBig, 0},
		{iconSmall, 0},
	} {
		icon, _, callErr := loadImage.Call(
			0,
			uintptr(unsafe.Pointer(handle)),
			imageIcon,
			uintptr(slot.size),
			uintptr(256),
			lrLoadFromFile,
		)
		if icon == 0 {
			log.Printf("icon: LoadImage: %v", callErr)
			continue
		}
		sendMessage.Call(hwnd, wmSetIcon, slot.kind, icon)
		kept, _, _ := sendMessage.Call(hwnd, wmGetIcon, slot.kind, 0)
		log.Printf("icon: slot %d set %#x, the window reports %#x", slot.kind, icon, kept)
	}
}
