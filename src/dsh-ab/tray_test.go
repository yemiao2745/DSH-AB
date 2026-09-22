package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getlantern/systray"
)

// TestStartFailurePopupDoesNotHoldTheOperationLock: the popup of a
// failed start was shown while opMu was held, so a modal dialog froze every other
// tray action behind it (the reported shape: "重启" confirmed at 16:35:04 started
// dsh at 16:38:30, exactly when the open dialog was dismissed).
//
// The measurement is direct: try to take opMu while the dialog is open. A
// sync.Mutex is not reentrant, so the same goroutine that shows the popup under
// the lock cannot take it, and the test goroutine that is not blocked on any
// dialog must still be able to.
func TestStartFailurePopupDoesNotHoldTheOperationLock(t *testing.T) {
	// A root with no slot-a: the start fails without starting anything.
	root := t.TempDir()
	cfg := DefaultConfig()
	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	st, err := LoadState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, ConfigReport{})
	a.tooltip = func(string) {} // no tray icon outside systray.Run
	shown, release := make(chan struct{}), make(chan struct{})
	lockFreeAtPopup := false
	a.popup = func(title, text string, isErr bool) {
		if a.opMu.TryLock() {
			a.opMu.Unlock()
			lockFreeAtPopup = true
		}
		close(shown)
		<-release // stand in for a user who has not dismissed the dialog yet
	}

	done := make(chan struct{})
	go func() {
		a.launch(true)
		close(done)
	}()

	select {
	case <-shown:
	case <-time.After(30 * time.Second):
		t.Fatal("启动失败没有产生任何弹窗")
	}
	if !lockFreeAtPopup {
		t.Error("弹窗是在持有 opMu 时显示的")
	}
	if a.opMu.TryLock() {
		a.opMu.Unlock()
	} else {
		t.Error("弹窗还开着时 opMu 仍被持有：其它托盘操作会一直等下去")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("launch 没有返回")
	}
}

// TestRollbackMenuTitleMatchesTheStatusPopup (user 2026-09-19, final wording):
// menu item 3 reads "槽位切换" in the default state and "槽位切换：待生效" while a
// registration is waiting - those are its only two states - and the item count and
// order stay untouched.
func TestRollbackMenuTitleMatchesTheStatusPopup(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct{ phase, want string }{
		{phaseNone, "槽位切换"},
		{phasePending, "槽位切换：待生效"},
	}
	for _, c := range cases {
		if got := rollbackMenuTitle(cfg, c.phase); got != c.want {
			t.Errorf("phase %s: menu item 3 = %q, want %q", c.phase, got, c.want)
		}
	}
	if got := cfg.Tray.Labels["rollback"]; got != "槽位切换" {
		t.Errorf("默认的 tray.labels.rollback = %q，应为 %q", got, "槽位切换")
	}
}

// TestOpenBrowserPopupIsDeferredAndInformational covers the other entry point that
// has to defer its popup. Two things are pinned at once: the preflight message of
// "打开浏览器" is shown after the lock is released, and it stays an information
// popup - a missing dsh process is a hint, not an error.
func TestOpenBrowserPopupIsDeferredAndInformational(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig()
	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	st, err := LoadState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, ConfigReport{})
	type popupCall struct {
		title    string
		isErr    bool
		lockFree bool
	}
	got := make(chan popupCall, 1)
	a.popup = func(title, text string, isErr bool) {
		lockFree := a.opMu.TryLock()
		if lockFree {
			a.opMu.Unlock()
		}
		got <- popupCall{title: title, isErr: isErr, lockFree: lockFree}
	}

	a.onOpenBrowser()

	select {
	case c := <-got:
		if !strings.Contains(c.title, "dsh 未运行") {
			t.Errorf("预检提示的标题 = %q，期望 dsh 未运行", c.title)
		}
		if !c.lockFree {
			t.Error("“打开浏览器”的预检提示是在持有 opMu 时显示的")
		}
		if c.isErr {
			t.Error("“dsh 未运行”是信息提示，不该用错误图标")
		}
	default:
		t.Fatal("dsh 未运行时“打开浏览器”没有给出任何提示")
	}
}

// TestConfigNoticeNeverBlocksTheStartupPath: a discarded config file must be
// reported, and the report is a modal dialog - so the ready path may only ask for
// it: whoever waits on a dialog holds the whole entry point hostage.
func TestConfigNoticeNeverBlocksTheStartupPath(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig()
	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	st, err := LoadState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	rep := ConfigReport{FileErr: errors.New("toml: line 5 (last key \"launch.env.DSH_HOME\"): invalid escape in string '\\d'")}
	a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, rep)

	shown, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	gotIcon := make(chan bool, 1)
	a.popup = func(title, text string, isErr bool) {
		once.Do(func() {
			gotIcon <- isErr
			close(shown)
		})
		<-release // stand in for a user who has not dismissed the notice yet
	}
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		a.configNotice()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("configNotice 阻塞了调用者：提示框没被点掉之前，启动路径走不下去")
	}
	select {
	case <-shown:
	case <-time.After(5 * time.Second):
		t.Fatal("配置读取失败时没有给出任何提示")
	}
	if isErr := <-gotIcon; isErr {
		t.Error("配置整份回退是提示，不是错误图标")
	}
}

// TestConfigNoticeShowsTheLogLineOnce walks the same wiring the user's dialog
// goes through: main.go logs the failure first (that is what makes the note a path
// rather than the "off" text), then the notice renders the body. That body must name
// the log exactly once - Popup used to prefix the caller's own "完整日志：" line.
func TestConfigNoticeShowsTheLogLineOnce(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig()
	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	st, err := LoadState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	rep := ConfigReport{FileErr: errors.New("toml: line 5: invalid escape in string '\\d'")}
	// main.go writes this line before the tray exists, so the log file is real here too.
	lg.Warnf("配置读取失败，本次使用默认值：%v", rep.FileErr)
	a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, rep)

	got := ""
	a.popup = func(title, text string, isErr bool) { got = text }
	a.showConfigNotice()
	if got == "" {
		t.Fatal("文件级失败必须给出提示框")
	}
	if !strings.Contains(got, "完整日志："+lg.Path()) {
		t.Errorf("提示框没有给出日志路径：\n%s", got)
	}
	if strings.Contains(got, "日志：完整日志：") {
		t.Errorf("提示框把日志前缀写了两遍：\n%s", got)
	}
}

// TestConfigNoticeDoesNotRepeatTheConfigWarning: one discarded config file
// earned two WARN lines - main.go's (which carries the parse error) and the
// notice's bare title. The notice owns the dialog; main.go owns the log line.
func TestConfigNoticeDoesNotRepeatTheConfigWarning(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig()
	logDir := t.TempDir()
	lg := NewLogger(logDir, LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	st, err := LoadState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	rep := ConfigReport{FileErr: errors.New("toml: line 5: invalid escape in string '\\d'")}
	a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, rep)

	shown := false
	a.popup = func(title, text string, isErr bool) { shown = true }
	a.showConfigNotice()
	if !shown {
		t.Fatal("文件级失败必须给出提示框")
	}
	lg.Close()
	data, err := os.ReadFile(filepath.Join(logDir, "dsh-ab.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("读日志文件：%v", err)
	}
	// A missing file counts as "wrote nothing", which is the point.
	if strings.Contains(string(data), "配置读取失败") {
		t.Errorf("提示框自己又写了一条 WARN（main.go 已经写过同一条）：\n%s", data)
	}
}

// TestStatusPopupReportsTheConfigState: the status popup says whether this run
// really uses what the config file says, so the user does not need the log for that.
func TestStatusPopupReportsTheConfigState(t *testing.T) {
	cases := []struct {
		name string
		rep  ConfigReport
		want string
	}{
		{"loaded", ConfigReport{}, "配置：正常"},
		{"repaired", ConfigReport{Repairs: []string{"a", "b"}}, "配置：2 项已回退"},
		{"discarded", ConfigReport{FileErr: errors.New("boom")}, "配置：读取失败，使用默认值"},
	}
	for _, c := range cases {
		root := t.TempDir()
		cfg := DefaultConfig()
		lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
		st, err := LoadState(filepath.Join(root, "state"))
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, c.rep)
		text := a.statusText()
		lg.Close()
		if !strings.Contains(text, c.want) {
			t.Errorf("%s：状态弹窗里没有 %q：\n%s", c.name, c.want, text)
		}
	}
}

// newTrayTestApp builds an App over an installation root with a stub popup, so the
// tests below drive the real tray handlers and the real launch path without a
// desktop.
func newTrayTestApp(t *testing.T, root string, cfg *Config) (*App, *State) {
	t.Helper()
	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	st, err := LoadState(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	a := NewApp(root, cfg, st, NewRunner(root, cfg, lg), lg, ConfigReport{})
	a.popup = func(string, string, bool) {}
	a.tooltip = func(string) {}
	return a, st
}

// launchTestConfig is DefaultConfig with the browser off, the start budget
// dialled down and every confirmation off: these tests walk the real start path
// without a desktop, a browser or a working dsh.
func launchTestConfig() *Config {
	cfg := DefaultConfig()
	cfg.Browser.OpenOnStart = false
	cfg.Launch.StartTimeoutS = 1
	cfg.Launch.ProbeIntervalMs = 50
	cfg.Rollback.Confirm = false
	cfg.Rollback.ConfirmRestart = false
	return cfg
}

// TestRollbackHasOnlyTwoStates (user 2026-09-19): menu item 3 has exactly two
// states - default and waiting - and undo goes straight back to the default
// state, leaving no archived third state on disk.
func TestRollbackHasOnlyTwoStates(t *testing.T) {
	root := t.TempDir()
	cfg := launchTestConfig()
	writeSlot(t, root, slotA, cfg)
	writeSlot(t, root, slotB, cfg)
	a, st := newTrayTestApp(t, root, cfg)

	if got := rollbackMenuTitle(cfg, st.RollbackPhase()); got != "槽位切换" {
		t.Fatalf("默认态菜单文字 = %q", got)
	}

	a.onRollback()
	if st.RollbackPhase() != phasePending {
		t.Fatalf("登记后 phase = %q，应为 pending", st.RollbackPhase())
	}
	if got := rollbackMenuTitle(cfg, st.RollbackPhase()); got != "槽位切换：待生效" {
		t.Fatalf("待生效态菜单文字 = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "pending.json")); err != nil {
		t.Fatalf("登记后应当有 pending.json：%v", err)
	}

	a.onRollback() // 第二击 = 撤销
	if st.RollbackPhase() != phaseNone {
		t.Fatalf("撤销后 phase = %q，应直接回到默认态", st.RollbackPhase())
	}
	if got := rollbackMenuTitle(cfg, st.RollbackPhase()); got != "槽位切换" {
		t.Fatalf("撤销后菜单文字 = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("撤销后 pending.json 必须消失，err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "undone.json")); !os.IsNotExist(err) {
		t.Fatalf("两态模型不得留下归档文件 undone.json，err=%v", err)
	}
}

// TestStatusPopupNeverShowsAThirdState: the status popup reports the same two
// states as the menu.
func TestStatusPopupNeverShowsAThirdState(t *testing.T) {
	root := t.TempDir()
	cfg := launchTestConfig()
	writeSlot(t, root, slotA, cfg)
	writeSlot(t, root, slotB, cfg)
	a, _ := newTrayTestApp(t, root, cfg)

	if text := a.statusText(); !strings.Contains(text, "槽位切换：未登记") {
		t.Fatalf("默认态状态弹窗里没有“槽位切换：未登记”：\n%s", text)
	}
	a.onRollback()
	if text := a.statusText(); !strings.Contains(text, "槽位切换：待生效") {
		t.Fatalf("待生效态状态弹窗里没有“槽位切换：待生效”：\n%s", text)
	}
	a.onRollback()
	text := a.statusText()
	if strings.Contains(text, "已撤销") || strings.Contains(text, "归档") {
		t.Fatalf("撤销后状态弹窗出现了第三态：\n%s", text)
	}
	if !strings.Contains(text, "槽位切换：未登记") {
		t.Fatalf("撤销后状态弹窗没有回到“槽位切换：未登记”：\n%s", text)
	}
}

// TestOpeningTheAppKeepsTheRegistration: closing the program and
// opening it again must not switch slots. That start is launch(true) - the one
// onReady performs - and it reads the registration without applying it.
func TestOpeningTheAppKeepsTheRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := launchTestConfig()
	writeSlot(t, root, slotA, cfg)
	writeSlot(t, root, slotB, cfg)
	a, st := newTrayTestApp(t, root, cfg)
	if err := st.Register(slotB); err != nil {
		t.Fatal(err)
	}

	a.launch(true) // 双击入口打开程序

	if st.ActiveSlot() != slotA {
		t.Errorf("手动重开后活动槽 = %q，应保持 slot-a", st.ActiveSlot())
	}
	if st.RollbackPhase() != phasePending {
		t.Errorf("手动重开后登记丢了：phase = %q", st.RollbackPhase())
	}
	if _, err := os.Stat(filepath.Join(root, "state", "pending.json")); err != nil {
		t.Errorf("手动重开后 pending.json 必须保留：%v", err)
	}
}

// TestTrayRestartAppliesTheRegistration: only the tray's 重启 applies a waiting
// registration.
func TestTrayRestartAppliesTheRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := launchTestConfig()
	writeSlot(t, root, slotA, cfg)
	writeSlot(t, root, slotB, cfg)
	a, st := newTrayTestApp(t, root, cfg)
	if err := st.Register(slotB); err != nil {
		t.Fatal(err)
	}

	a.onRestart() // 托盘第 2 项

	if st.ActiveSlot() != slotB {
		t.Errorf("点“重启”后活动槽 = %q，应为 slot-b", st.ActiveSlot())
	}
	if st.Pending != nil {
		t.Errorf("生效后登记应被清空：%+v", st.Pending)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "pending.json")); !os.IsNotExist(err) {
		t.Errorf("生效后 pending.json 应消失，err=%v", err)
	}
}

// TestRollbackConfirmTitlesMatchTheMenu (user 2026-09-19, "都改成"): the whole
// button carries only two texts, so the two confirmation dialogs of menu item 3
// use exactly those, not a third wording of their own.
func TestRollbackConfirmTitlesMatchTheMenu(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct{ phase, target string }{
		{phaseNone, slotB},
		{phasePending, slotB},
	}
	for _, c := range cases {
		title, text := rollbackConfirm(c.phase, c.target)
		if want := rollbackMenuTitle(cfg, c.phase); title != want {
			t.Errorf("phase %s：确认框标题 = %q，应为菜单文字 %q", c.phase, title, want)
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("phase %s：确认框没有正文", c.phase)
		}
	}
	if title, _ := rollbackConfirm(phaseNone, slotB); strings.Contains(title, "回滚") || strings.Contains(title, "登记") {
		t.Errorf("默认态确认框标题 = %q，只能有两种文字", title)
	}
	if title, _ := rollbackConfirm(phasePending, slotB); strings.Contains(title, "撤销") {
		t.Errorf("待生效态确认框标题 = %q，只能有两种文字", title)
	}
	// The word 回滚 is gone from everything the user reads (user 2026-09-19,
	// "回滚不好听"): the registration dialog says 切换到, like the menu it belongs to.
	if _, text := rollbackConfirm(phaseNone, slotB); strings.Contains(text, "回滚") {
		t.Errorf("默认态确认框正文 = %q，里面还有「回滚」", text)
	}
	if _, text := rollbackConfirm(phaseNone, slotB); !strings.Contains(text, "切换到 "+slotB) {
		t.Errorf("默认态确认框正文 = %q，应为「…切换到 slot-b？…」", text)
	}
}

// TestMenuOrderIsInformationFirst (2026-09-19): the tray lists what the
// user can look at first - 打开浏览器 / 当前状态 / log：<level> - then the two
// actions, then 退出. The same six items as before, in this order.
func TestMenuOrderIsInformationFirst(t *testing.T) {
	a, _ := newTrayTestApp(t, t.TempDir(), DefaultConfig())

	var titles []string
	a.addMenuItems(func(title, tooltip string) *systray.MenuItem {
		titles = append(titles, title)
		return nil
	})

	want := []string{"打开浏览器", "当前状态", "log：auto", "槽位切换", "重启", "退出"}
	if len(titles) != len(want) {
		t.Fatalf("菜单项数 = %d，应为 %d：%v", len(titles), len(want), titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("第 %d 项 = %q，应为 %q（实际顺序 %v）", i+1, titles[i], want[i], titles)
		}
	}
}

// TestStatusPopupReportsWhichCopyIsTalking: two installations run side by
// side and their status popups look alike - same menu, same words - so the popup
// names the installation root, the build, and where its own log file is.
func TestStatusPopupReportsWhichCopyIsTalking(t *testing.T) {
	root := t.TempDir()
	a, _ := newTrayTestApp(t, root, launchTestConfig())

	text := a.statusText()
	for _, want := range []string{
		"安装根：" + root,
		"版本：" + versionText(effectiveDshabVersion(), dshVersion),
		"日志路径：" + a.lg.Path(),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("状态弹窗里没有 %q：\n%s", want, text)
		}
	}
}

// TestTrayTooltipCarriesStateAndPort：只有一份时也要显示端口，于是 tooltip
// 就是这两句话，一字不多。它不再显示安装名——名字已经在应用和功能条目与快捷方式上——配置键
// tray.tooltip 也因此删掉了（设了没用）。两句话都短，Windows 的 127 字符上限再也够不着。
func TestTrayTooltipCarriesStateAndPort(t *testing.T) {
	if got := tooltipText(true, 3190); got != "DSH 运行中（端口 3190）" {
		t.Errorf("运行中的托盘提示 = %q，应为 %q", got, "DSH 运行中（端口 3190）")
	}
	if got := tooltipText(false, 3190); got != "DSH 未运行（端口 3190）" {
		t.Errorf("未运行时的托盘提示 = %q，应为 %q", got, "DSH 未运行（端口 3190）")
	}
}

// TestStatusPopupUsesTheMenuLabel (2026-09-19): menu item 2 and the popup it
// opens read the same words - 当前状态 - so the default label and the popup title
// cannot drift apart.
func TestStatusPopupUsesTheMenuLabel(t *testing.T) {
	a, _ := newTrayTestApp(t, t.TempDir(), DefaultConfig())

	if got := a.cfg.Tray.Labels["status"]; got != "当前状态" {
		t.Errorf("tray.labels.status 默认 = %q，应为 %q", got, "当前状态")
	}

	shown, title, text := false, "", ""
	a.popup = func(gotTitle, gotText string, isErr bool) {
		shown, title, text = true, gotTitle, gotText
		if isErr {
			t.Error("状态弹窗不该是错误框")
		}
	}
	a.onStatus()

	if !shown {
		t.Fatal("点「当前状态」没有弹出任何窗口")
	}
	// 标题以本安装的名字开头，后面才是菜单文字，两份共存的弹窗不再长得一样。
	want := a.displayName() + " · " + a.cfg.Tray.Labels["status"]
	if title != want {
		t.Errorf("状态弹窗标题 = %q，应为 %q", title, want)
	}
	if strings.TrimSpace(text) == "" {
		t.Error("状态弹窗没有正文")
	}
}
