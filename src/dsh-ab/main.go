// DSH-AB is the entry point, slot switcher and tray host for a DSH
// installation. It never reads, parses or writes anything inside dsh: it only
// starts it, stops it and probes whether its port answers.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/getlantern/systray"
)

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
// allowed to coexist (docs\\dev\\DEFECTS.md DEF-015); a second start of the same one is not.
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
	// Asking which versions this build carries must not start anything: it is the
	// first thing the installer and build/verify-silent.ps1 do with a built exe.
	if handleVersionFlag(os.Args[1:], os.Stdout) {
		return
	}

	// Before the tray starts, and on this thread: the owner window of every popup
	// must belong to the thread that pumps messages from here on (see
	// owner_window.go). Every popup path below, including the very first ones,
	// then has an owner to hand to MessageBoxW.
	popupOwner()

	root, err := executableRoot()
	if err != nil {
		alertErr("DSH-AB", "无法确定安装目录：\n"+err.Error())
		return
	}

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
	// P2/P3: 名字和快捷方式的位置每次启动对齐一次。只动 DSH-AB 自己的条目与快捷方式，
	// 不碰 dsh，也不碰别的安装；失败只写日志，绝不影响启动。名字退化到 tag（端口不可用或
	// 与别的安装冲突）时带出那条要给用户看的弹窗，它在托盘起来之后才显示（DEF-007）。
	app.name, app.nameNotice = alignInstallIdentity(root, cfg.Ports, lg)
	// The version belongs in the startup line: the log is what the user reads when
	// a start failed, and it has to say which build and which copy produced it (A5).
	lg.Info("DSH-AB 启动：%s，安装根 %s", versionText(effectiveDshabVersion(), dshVersion), root)

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
