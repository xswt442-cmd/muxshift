//go:build windows

package win

import (
	"syscall"
	"unsafe"
)

const internationalKey = `Control Panel\International`

var procRegGetValueWLocale = syscall.NewLazyDLL("advapi32.dll").NewProc("RegGetValueW")

// Locale reads the Windows display locale (for example "zh-CN"). The UI
// library only reports the C locale, so following the system language needs the
// value the shell itself uses.
func Locale() string {
	subkey, err := syscall.UTF16PtrFromString(internationalKey)
	if err != nil {
		return ""
	}
	value, err := syscall.UTF16PtrFromString("LocaleName")
	if err != nil {
		return ""
	}
	buffer := make([]uint16, 128)
	size := uint32(len(buffer) * 2)
	result, _, _ := procRegGetValueWLocale.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(subkey)),
		uintptr(unsafe.Pointer(value)),
		uintptr(rrfRTSZ),
		0,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if result != 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer)
}
