package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

var procGetWindowLongW = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowLongW")

// The single popup mechanism needs an owner window. A MessageBoxW called
// with a NULL owner is a root-level window: Windows gives it a taskbar button of
// its own and it inherits no icon, which is the stray icon the user reported. The
// owner itself must stay invisible and must not become a taskbar button either.
func TestPopupOwnerIsAHiddenToolWindowWithTheAppIcon(t *testing.T) {
	hwnd := popupOwner()
	if hwnd == 0 {
		t.Fatal("popupOwner() = 0：弹窗又会变成没有属主的顶层窗口")
	}
	if again := popupOwner(); again != hwnd {
		t.Fatalf("popupOwner() 每次返回不同的窗口：%#x 然后 %#x", hwnd, again)
	}

	if windows.IsWindowVisible(windows.HWND(hwnd)) {
		t.Error("属主窗口是可见的：它会自己出现在桌面/任务栏上")
	}

	gwlExStyle := int32(-20)
	ex, _, _ := procGetWindowLongW.Call(hwnd, uintptr(uint32(gwlExStyle)))
	if ex&wsExToolWindow == 0 {
		t.Errorf("属主窗口没有 WS_EX_TOOLWINDOW（exstyle=%#x）：它会占一个任务栏按钮", ex)
	}

	for _, kind := range []struct {
		name string
		id   uintptr
	}{{"ICON_SMALL", iconSmall}, {"ICON_BIG", iconBig}} {
		icon, _, _ := procSendMessageW.Call(hwnd, wmGetIcon, kind.id, 0)
		if icon == 0 {
			t.Errorf("WM_GETICON(%s) = 0：弹窗拿不到 DSH_AB.exe 的图标", kind.name)
		}
	}
}
