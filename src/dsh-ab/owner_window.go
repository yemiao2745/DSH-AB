package main

import (
	"os"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every popup is a MessageBoxW, and a MessageBoxW whose owner is NULL is
// a root-level window. Windows then gives that dialog a taskbar button of its own
// — it belongs to nobody — and there is no window whose icon it could inherit, so
// the taskbar showed a stray, wrong icon next to the tray icon. The single popup
// mechanism therefore owns exactly one invisible window and hands it to
// MessageBoxW as the owner.
const (
	wsExToolWindow = 0x00000080 // no taskbar button, no Alt-Tab entry
	wmSetIcon      = 0x0080
	wmGetIcon      = 0x007F
	iconSmall      = 0
	iconBig        = 1
)

var (
	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
	procCreateWindowExW  = modUser32.NewProc("CreateWindowExW")
	procSendMessageW     = modUser32.NewProc("SendMessageW")
	procExtractIconExW   = modShell32.NewProc("ExtractIconExW")
)

var (
	popupOwnerOnce sync.Once
	popupOwnerHWND uintptr
)

// popupOwner is the owner window of every popup, created once and reused for the
// rest of the process. main() creates it before the tray starts, on the thread
// that then pumps messages (the systray event loop's thread): Windows disables a
// dialog's owner for as long as the dialog is up, and doing that to a window whose
// thread never pumps would block the popup thread forever. Anything that shows a
// popup before main() got there still gets a window, from whatever thread asks
// first.
//
// It is created from the pre-registered "STATIC" class on purpose: the window only
// ever owns dialogs and carries an icon, and DefWindowProc is the right handler
// for both — so there is no window class to register and no Go callback to keep
// alive. It is never shown, and it is never destroyed explicitly either:
// DestroyWindow only works from the thread that created the window, and process
// teardown destroys it with everything else.
//
// A failure here is not fatal: popupOwner returns 0 and the popup behaves exactly
// as it did before the owner window existed.
func popupOwner() uintptr {
	popupOwnerOnce.Do(func() {
		// 建窗口和给它设图标必须是同一条线程：CreateWindowExW 建的窗口属于调用它的线程，
		// 而 SendMessageW 给一条没有在取消息的线程发的消息会一直等下去。goroutine 在两次调用
		// 之间换线程（Go 随时可以这么做）时，setPopupOwnerIcon 就会永久卡住 —— 在 HEAD 上
		// 直接复现过（同样的测试有时通过、有时一卡十分钟）。把本 goroutine 钉在这条线程上，
		// 且不解除：它此后就是这条 goroutine（生产里是 main，也正是 systray 取消息的那条）的线程。
		runtime.LockOSThread()

		class, err := windows.UTF16PtrFromString("STATIC")
		if err != nil {
			return
		}
		title, err := windows.UTF16PtrFromString("DSH-AB")
		if err != nil {
			return
		}
		instance, _, _ := procGetModuleHandleW.Call(0)
		hwnd, _, _ := procCreateWindowExW.Call(
			wsExToolWindow,
			uintptr(unsafe.Pointer(class)),
			uintptr(unsafe.Pointer(title)),
			0, // never WS_VISIBLE: the owner itself must not show up anywhere
			0, 0, 0, 0,
			0, 0, instance, 0,
		)
		if hwnd == 0 {
			return
		}
		popupOwnerHWND = hwnd
		setPopupOwnerIcon(hwnd)
	})
	return popupOwnerHWND
}

// setPopupOwnerIcon gives the owner the icon of this executable, so the dialog
// inherits the DSH_AB.exe icon. The icon is taken from the executable's own icon
// resource (build\dsh-ab.exe.manifest and src\dsh-ab\assets\dsh.ico, linked in as
// rsrc_windows_amd64.syso) by index, so it does not depend on the resource id rsrc
// happens to give it.
func setPopupOwnerIcon(hwnd uintptr) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	path, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return
	}
	var large, small uintptr
	n, _, _ := procExtractIconExW.Call(
		uintptr(unsafe.Pointer(path)), 0,
		uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1)
	if int32(n) <= 0 {
		return
	}
	if large != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconBig, large)
	}
	if small != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	}
}
