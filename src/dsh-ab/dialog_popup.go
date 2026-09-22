package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// MessageBoxW is the only popup mechanism in the whole program: system font,
// system DPI, system theme, dismissable with the mouse.
const (
	mbTopmost       = 0x00040000
	mbSetForeground = 0x00010000
)

var (
	modUser32       = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = modUser32.NewProc("MessageBoxW")
)

func messageBox(title, text string, flags uint32) int {
	caption, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	body, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	// The owner is what keeps the dialog out of the taskbar and lets it inherit
	// the DSH_AB.exe icon (DEF-005); an ownerless MessageBox would be a second,
	// wrongly-iconed taskbar button next to the tray icon.
	ret, _, _ := procMessageBoxW.Call(
		popupOwner(),
		uintptr(unsafe.Pointer(body)),
		uintptr(unsafe.Pointer(caption)),
		uintptr(flags|mbTopmost|mbSetForeground),
	)
	return int(ret)
}
