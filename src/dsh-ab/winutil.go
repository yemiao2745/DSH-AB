package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modShell32         = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW  = modShell32.NewProc("ShellExecuteW")
	procSHChangeNotify = modShell32.NewProc("SHChangeNotify")
	modKernel32        = windows.NewLazySystemDLL("kernel32.dll")
	procCreateMutexW   = modKernel32.NewProc("CreateMutexW")
)

const swShowNormal = 1

// SHChangeNotify tells the running shell that the shell namespace changed. SHCNE_RENAMEITEM with
// SHCNF_PATHW takes the old and the new path. This is what makes a moved shortcut visible to a
// StartMenuExperienceHost that is already running (2026-09-22 真机：见 naming.go 的 moveLinkFile).
const (
	shcneRenameItem = 0x00000001
	shcnfPathW      = 0x00000005
)

// notifyShellRenameItem reports that the item at oldPath now lives at newPath. SHChangeNotify
// returns nothing, so only the argument conversion can fail.
func notifyShellRenameItem(oldPath, newPath string) error {
	oldP, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newP, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	procSHChangeNotify.Call(
		shcneRenameItem,
		shcnfPathW,
		uintptr(unsafe.Pointer(oldP)),
		uintptr(unsafe.Pointer(newP)),
	)
	return nil
}

// shellOpen hands a file or URL to the shell, which is how the default browser
// is started without spawning a console.
func shellOpen(file, params string) error {
	op, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	f, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	var paramsPtr *uint16
	if params != "" {
		if paramsPtr, err = windows.UTF16PtrFromString(params); err != nil {
			return err
		}
	}
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(f)),
		uintptr(unsafe.Pointer(paramsPtr)),
		0,
		swShowNormal,
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW 失败，代码 %d", ret)
	}
	return nil
}

// acquireSingleInstance reports whether this process is the first holder of the
// named mutex. A second double click must not start a second tray.
func acquireSingleInstance(name string) bool {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return true
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return true
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
		return false
	}
	return true
}
