// DSH-AB is the entry point, slot switcher and tray host for a DSH
// installation. It never reads, parses or writes anything inside dsh: it only
// starts it, stops it and probes whether its port answers.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/getlantern/systray"
)

// usageText is what --help prints: the verbs this exe has, and what happens with
// none of them. --version and --help are the whole list; both answer and exit without
// a tray, so naming them here answers "what can this exe do" without starting one.
func usageText() string {
	return "用法：DSH_AB.exe [动词]\n" +
		"  --version   打印 DSH-AB 与 dsh 的版本后退出\n" +
		"  --help, -h  打印这一行后退出\n" +
		"不带动词：启动托盘（已经在运行时只弹一句提示）。\n"
}

// commandLine is what one command line asks this exe to do. These four are the whole
// verb table this program has.
type commandLine int

const (
	startTray         commandLine = iota // no argument asks for anything
	printVersion                         // --version / -v
	printHelp                            // --help / -h
	rejectUnknownVerb                    // looks like a verb, is not one
)

// classifyCommandLine decides a command line without starting anything, and names the
// argument that decided it. It is pure, so the three answers a test has to pin - a
// recognised verb, no verb at all, and a verb this exe does not have - need no tray and
// no desktop. A recognised verb wins wherever it stands ("--help --oops" is a help
// request: the caller did ask for something this exe has); anything else with a leading
// '-' or '/' is a verb this build does not have, because no other argument this program
// takes is one - a typo, or a verb that was deleted from an old document, must not be
// read as "start the tray".
func classifyCommandLine(args []string) (commandLine, string) {
	for _, a := range args {
		switch a {
		case "--version", "-v":
			return printVersion, a
		case "--help", "-h":
			return printHelp, a
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "/") {
			return rejectUnknownVerb, a
		}
	}
	return startTray, ""
}

// runCommandLine answers every command line except "start the tray" and reports whether
// the tray is what is left to do. This is the whole decision main acts on, and it is a
// function rather than three statements inside main for one reason: the returned bool is
// the only thing that may lead to systray.Run, so a test can pin that a verb this exe
// does not have never reaches it. Measured 2026-09-25: DSH_AB.exe --slot-guard (a verb
// deleted this round) started a whole tray and a dsh and wrote a normal startup log.
func runCommandLine(args []string, stdout, stderr io.Writer) (code int, tray bool) {
	switch what, arg := classifyCommandLine(args); what {
	case printVersion:
		handleVersionFlag([]string{arg}, stdout)
	case printHelp:
		handleHelpFlag([]string{arg}, stdout)
	case rejectUnknownVerb:
		fmt.Fprintf(stderr, "未知动词：%s\n\n", arg)
		fmt.Fprint(stderr, usageText())
		return 2, false
	default:
		return 0, true
	}
	return 0, false
}

// handleHelpFlag answers --help and -h and reports whether the program must stop.
// Like handleVersionFlag it runs before main looks for the installation root, so
// asking for help never opens a window and never starts a tray: a help request that
// fell through to the tray left the caller with a stray process and no usage line.
func handleHelpFlag(args []string, out io.Writer) bool {
	if what, _ := classifyCommandLine(args); what == printHelp {
		fmt.Fprint(out, usageText())
		return true
	}
	return false
}

// embeddedIcon is the finished icon resource of this project
// (srcdsh-abassetsdsh.ico). Nothing inside the dsh package is modified.
//
//go:embed assets/dsh.ico
var embeddedIcon []byte

// singleInstanceMutexName derives the name of the mutex that keeps a second copy of *this*
// installation from starting a second tray.
//
// It is derived from the installation root on purpose: the name used to be the constant
// "Local\\dsh-ab-single-instance", and the installer watched the same name with AppMutex, so a
// second DSH-AB on the same machine could neither be installed nor run. Two installations are
// allowed to coexist; a second start of the same one is not.
func singleInstanceMutexName(root string) string {
	key := filepath.Clean(root)
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	// Windows paths are case-insensitive, and a trailing separator names the same directory.
	key = strings.ToLower(strings.TrimRight(key, `\/`))
	sum := sha256.Sum256([]byte(key))
	return "Local\\dsh-ab-" + hex.EncodeToString(sum[:16])
}

func main() {
	// 命令行只在这里解释一次，而且三条不建托盘的出路全在下面之前返回：
	//   * --version：问这个 exe 里到底编了哪个版本，是安装器和 build/py/verify.py
	//     对已构建的 exe 做的第一件事，绝不能启动任何东西；
	//   * --help / -h：只打印一行用法就退出，绝不建托盘、不抢单实例互斥；
	//   * 一个本 exe 没有的动词：旧文档里已被删除的 --slot-guard、或者一个拼错的开关，
	//     以前会静默往下走，把一次调用变成第二个托盘加一个 dsh（2026-09-25 实测）。
	if code, tray := runCommandLine(os.Args[1:], os.Stdout, os.Stderr); !tray {
		if code != 0 {
			os.Exit(code)
		}
		return
	}

	root, err := executableRoot()
	if err != nil {
		alertErr("DSH-AB", "无法确定安装目录：\n"+err.Error())
		return
	}

	// Before the tray starts, and on this thread: the owner window of every popup
	// must belong to the thread that pumps messages from here on (see
	// owner_window.go). Every popup path below, including the very first ones,
	// then has an owner to hand to MessageBoxW.
	popupOwner()

	// One mutex per installation, not per machine: the name is derived from the root above.
	if !acquireSingleInstance(singleInstanceMutexName(root)) {
		alert("DSH-AB", "DSH-AB 已经在运行。\n请使用任务栏通知区域里的图标。")
		return
	}

	cfg, cfgReport := LoadConfig(filepath.Join(root, "dsh-ab.toml"))
	if cfg == nil {
		cfg = DefaultConfig()
	}
	lg := NewLogger(cfg.Abs(root, cfg.Logs.Dir), cfg.Logs.Level, cfg.Logs.MaxFiles, cfg.Logs.MaxSizeMB)
	if cfgReport.FileErr != nil {
		lg.Warnf("配置读取失败，本次使用默认值：%v", cfgReport.FileErr)
	}
	for _, w := range cfgReport.Repairs {
		lg.Warnf("%s", w)
	}

	st, stErr := LoadState(filepath.Join(root, "state"))
	if stErr != nil {
		lg.Errorf("状态目录不可用：%v", stErr)
		alertErr("DSH-AB", "状态目录不可用：\n"+stErr.Error())
		lg.Close()
		return
	}
	for _, w := range st.Warnings {
		lg.Warnf("%s", w)
	}

	run := NewRunner(root, cfg, lg)
	app := NewApp(root, cfg, st, run, lg, cfgReport)
	// 名字和快捷方式的位置每次启动对齐一次。只动 DSH-AB 自己的条目与快捷方式，
	// 不碰 dsh，也不碰别的安装；失败只写日志，绝不影响启动。名字退化到 tag（端口不可用或
	// 与别的安装冲突）时带出那条要给用户看的弹窗，它在托盘起来之后才显示。
	app.name, app.nameNotice = alignInstallIdentity(root, cfg.Ports, lg)
	// The version belongs in the startup line: the log is what the user reads when
	// a start failed, and it has to say which build and which copy produced it - and
	// which slot that copy is about to run, with the dsh version really in it (the
	// build-time dshVersion says nothing about a slot somebody upgraded by hand).
	lg.Info("DSH-AB 启动：DSH-AB %s，活动槽 %s（dsh %s），安装根 %s",
		effectiveDshabVersion(), st.ActiveSlot(), slotVersionText(dshVersionInSlot(root, cfg, st.ActiveSlot())), root)

	systray.Run(app.onReady, app.onExit)
}

// executableRoot is the installation root: the directory holding DSH_AB.exe.
func executableRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}
