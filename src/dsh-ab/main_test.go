package main

import (
	"strings"
	"testing"
)

// TestSingleInstanceMutexNameIsPerInstallation: the tray mutex has to differ between two
// installations (they must be able to run side by side) and stay the same for one installation
// however its root is spelled. The name used to be a constant, which is exactly what made a second
// DSH-AB impossible (docs\dev\DEFECTS.md DEF-015).
func TestSingleInstanceMutexNameIsPerInstallation(t *testing.T) {
	a := singleInstanceMutexName(`C:\DSH-AB`)
	b := singleInstanceMutexName(`D:\DSH-AB`)
	if a == b {
		t.Fatalf("两份不同的安装拿到同一个互斥体名 %q：第二份将无法启动", a)
	}
	if !strings.HasPrefix(a, `Local\dsh-ab-`) {
		t.Fatalf("互斥体名 %q 不在 Local\\ 命名空间里", a)
	}
	for _, same := range []string{`C:\DSH-AB`, `c:\dsh-ab`, `C:\DSH-AB\`, `C:\DSH-AB\.`} {
		if got := singleInstanceMutexName(same); got != a {
			t.Errorf("%q 与 %q 是同一份安装，却得到 %q 与 %q", same, `C:\DSH-AB`, got, a)
		}
	}
}
