package main

import (
	"io"
	"strings"
	"testing"
)

// TestSingleInstanceMutexNameIsPerInstallation: the tray mutex has to differ between two
// installations (they must be able to run side by side) and stay the same for one installation
// however its root is spelled. The name used to be a constant, which is exactly what made a second
// DSH-AB impossible.
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

// TestCommandLineDecidesWithoutATray: which answers a command line can get, and the fact
// that exactly one of them leads to a tray, used to be three inline checks inside main -
// nothing could pin that a verb this exe does not have (the --slot-guard deleted from an
// old document, or a typo) is refused instead of falling through to systray.Run. Measured
// 2026-09-25: it started a whole tray plus a dsh and wrote a normal startup log.
func TestCommandLineDecidesWithoutATray(t *testing.T) {
	// 不带任何参数仍然是「启动托盘」，而且只有它启动。
	for _, args := range [][]string{nil, {}, {"web"}, {"slot-a"}} {
		what, _ := classifyCommandLine(args)
		if what != startTray {
			t.Errorf("参数 %v 被判成 %v，应当是启动托盘", args, what)
		}
		if code, tray := runCommandLine(args, io.Discard, io.Discard); !tray || code != 0 {
			t.Errorf("参数 %v 没有走启动托盘的出路：code=%d tray=%v", args, code, tray)
		}
	}

	// 认得的动词照旧：打印后退出，绝不建托盘。认得的动词站在哪里都算数。
	for _, tc := range []struct {
		args []string
		want commandLine
	}{
		{[]string{"--version"}, printVersion},
		{[]string{"-v"}, printVersion},
		{[]string{"--help"}, printHelp},
		{[]string{"-h"}, printHelp},
		{[]string{"--help", "--oops"}, printHelp},
	} {
		if what, _ := classifyCommandLine(tc.args); what != tc.want {
			t.Errorf("%v 被判成 %v，应当是 %v", tc.args, what, tc.want)
		}
		var out strings.Builder
		code, tray := runCommandLine(tc.args, &out, io.Discard)
		if tray || code != 0 {
			t.Errorf("%v 走到了托盘或非零退出：code=%d tray=%v", tc.args, code, tray)
		}
		if strings.TrimSpace(out.String()) == "" {
			t.Errorf("%v 什么都没打印", tc.args)
		}
	}
	// handleVersionFlag / handleHelpFlag 是打印那一步的实现，认得的东西必须和分类一致。
	var help strings.Builder
	if !handleHelpFlag([]string{"--help"}, &help) || !strings.Contains(help.String(), "DSH_AB.exe") {
		t.Errorf("--help 没有打印用法：%q", help.String())
	}
	var ver strings.Builder
	if !handleVersionFlag([]string{"-v"}, &ver) || !strings.Contains(ver.String(), effectiveDshabVersion()) {
		t.Errorf("-v 没有打印版本：%q", ver.String())
	}

	// 不认识的动词：用法写到 stderr，退出码 2，托盘一定不启动。
	for _, args := range [][]string{{"--slot-guard"}, {"--nonsense-verb"}, {"/slot-guard"}} {
		what, arg := classifyCommandLine(args)
		if what != rejectUnknownVerb || arg != args[0] {
			t.Fatalf("%v 被判成 %v（arg=%q），应当是被拒绝的未知动词", args, what, arg)
		}
		var out, errOut strings.Builder
		code, tray := runCommandLine(args, &out, &errOut)
		if tray {
			t.Fatalf("未知动词 %v 走到了 systray.Run", args)
		}
		if code == 0 {
			t.Errorf("未知动词 %v 的退出码是 0，应当非零", args)
		}
		if out.String() != "" {
			t.Errorf("未知动词 %v 往 stdout 写了东西：%q", args, out.String())
		}
		if !strings.Contains(errOut.String(), args[0]) {
			t.Errorf("未知动词 %v 的报错里没有那个参数：%q", args, errOut.String())
		}
		if !strings.Contains(errOut.String(), usageText()) {
			t.Errorf("未知动词 %v 没有把用法打到 stderr：%q", args, errOut.String())
		}
	}
}
