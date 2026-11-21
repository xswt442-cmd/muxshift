//go:build windows

package win

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	ntdll             = syscall.NewLazyDLL("ntdll.dll")
	procRtlGetVersion = ntdll.NewProc("RtlGetVersion")
	kernel32Memory    = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemory  = kernel32Memory.NewProc("GlobalMemoryStatusEx")
)

type osVersionInfo struct {
	Size         uint32
	Major        uint32
	Minor        uint32
	Build        uint32
	Platform     uint32
	CSDVersion   [128]uint16
	ServicePack  uint16
	ServicePack2 uint16
	SuiteMask    uint16
	ProductType  byte
	Reserved     byte
}

type memoryStatus struct {
	Length            uint32
	MemoryLoad        uint32
	TotalPhysical     uint64
	AvailablePhysical uint64
	TotalPageFile     uint64
	AvailablePageFile uint64
	TotalVirtual      uint64
	AvailableVirtual  uint64
	AvailableExtended uint64
}

// SystemInfo names the Windows release and the physical memory the machine
// reports. It is read from the same APIs the operating system uses, not from a
// version string chosen in the application.
func SystemInfo() (string, uint64) {
	info := osVersionInfo{Size: uint32(unsafe.Sizeof(osVersionInfo{}))}
	if ok, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		name := "Windows"
		switch {
		case info.Major == 10 && info.Build >= 22000:
			name = "Windows 11"
		case info.Major == 10:
			name = "Windows 10"
		case info.Major == 6 && info.Minor == 3:
			name = "Windows 8.1"
		case info.Major == 6 && info.Minor == 2:
			name = "Windows 8"
		case info.Major == 6 && info.Minor == 1:
			name = "Windows 7"
		}
		if info.Build != 0 {
			name = fmt.Sprintf("%s (build %d)", name, info.Build)
		}
		memory := memoryStatus{Length: uint32(unsafe.Sizeof(memoryStatus{}))}
		if ok, _, _ := procGlobalMemory.Call(uintptr(unsafe.Pointer(&memory))); ok != 0 {
			return name, memory.TotalPhysical
		}
		return name, 0
	}
	return "Windows", 0
}
