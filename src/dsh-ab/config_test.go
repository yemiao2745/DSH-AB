package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultConfigMatchesPlan pins every knob of docs/PLAN.md section 4 to its documented default.
// 生产端口默认 3090（见 config.go 的 defaultProductionPort）：这一行就是钉住它的地方。
func TestDefaultConfigMatchesPlan(t *testing.T) {
	c := DefaultConfig()
	if c.Ports.Production != 3090 || c.Ports.Host != "127.0.0.1" {
		t.Fatalf("ports defaults = %+v", c.Ports)
	}
	if !c.Browser.OpenOnStart || c.Browser.BrowserExe != "" {
		t.Fatalf("browser defaults = %+v", c.Browser)
	}
	if c.Launch.NodeExe != "node/node.exe" || c.Launch.DshEntry != "app/node_modules/@deepseek-ai/dsh/lib/bin.js" {
		t.Fatalf("launch paths = %+v", c.Launch)
	}
	if c.Launch.StartTimeoutS != 60 || c.Launch.ProbeIntervalMs != 500 || c.Launch.StopGraceS != 10 || !c.Launch.KillTree {
		t.Fatalf("launch timing = %+v", c.Launch)
	}
	if c.Tray.Icon != "embedded" {
		t.Fatalf("tray defaults = %+v", c.Tray)
	}
	for _, k := range []string{"open", "restart", "rollback", "status", "log", "exit"} {
		if c.Tray.Labels[k] == "" {
			t.Fatalf("tray label %q missing (six fixed items)", k)
		}
	}
	if c.Tray.Labels["log"] != "log：{level}" {
		t.Fatalf("log label template = %q", c.Tray.Labels["log"])
	}
	if c.Logs.Level != "auto" || c.Logs.Dir != "logs" || c.Logs.MaxFiles != 20 || c.Logs.MaxSizeMB != 5 {
		t.Fatalf("logs defaults = %+v", c.Logs)
	}
	if c.Health.PollIntervalS != 30 || c.Health.PopupThrottleMin != 5 || !c.Health.PopupOnProcessDead || !c.Health.PopupOnPortDown {
		t.Fatalf("health defaults = %+v", c.Health)
	}
	if !c.Rollback.Confirm || !c.Rollback.RequireOtherSlot || !c.Rollback.ConfirmRestart || !c.Rollback.ConfirmExit {
		t.Fatalf("rollback defaults = %+v", c.Rollback)
	}
}

// TestLoadConfigOverlaysPresentKeys: a partial file changes only what it names, everything else keeps its default.
func TestLoadConfigOverlaysPresentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dsh-ab.toml")
	body := `[ports]
	production = 4190

	[browser]
	open_on_start = false
	extra_args = ["--new-window"]

	[tray.labels]
	open = "Open browser"

	[launch.env]
	DSH_TELEMETRY = "0"
	`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, rep := LoadConfig(path)
	if rep.FileErr != nil {
		t.Fatalf("LoadConfig: %v", rep.FileErr)
	}
	if len(rep.Repairs) != 0 || rep.StatusLine() != "配置：正常" {
		t.Fatalf("一份干净的文件不该有任何回退：repairs=%v %q", rep.Repairs, rep.StatusLine())
	}
	if _, _, ok := rep.Popup("log"); ok {
		t.Fatal("一份干净的文件不该弹任何提示框")
	}
	if c.Ports.Production != 4190 {
		t.Fatalf("production port = %d, want 4190", c.Ports.Production)
	}
	if c.Browser.OpenOnStart {
		t.Fatal("open_on_start should be false")
	}
	if len(c.Browser.ExtraArgs) != 1 || c.Browser.ExtraArgs[0] != "--new-window" {
		t.Fatalf("browser extra_args = %v", c.Browser.ExtraArgs)
	}
	if c.Tray.Labels["open"] != "Open browser" {
		t.Fatalf("overridden label = %q", c.Tray.Labels["open"])
	}
	if c.Tray.Labels["exit"] == "" {
		t.Fatal("labels the file did not mention must keep their default")
	}
	if c.Launch.Env["DSH_TELEMETRY"] != "0" {
		t.Fatalf("launch env = %v", c.Launch.Env)
	}
}

// TestLoadConfigMissingFileFallsBackToDefaults: the program must still run with no config file.
func TestLoadConfigMissingFileFallsBackToDefaults(t *testing.T) {
	c, rep := LoadConfig(filepath.Join(t.TempDir(), "absent.toml"))
	if rep.FileErr != nil {
		t.Fatalf("missing file must not be fatal: %v", rep.FileErr)
	}
	if len(rep.Repairs) != 0 {
		t.Fatalf("没有配置文件就没有回退项：%v", rep.Repairs)
	}
	if c.Ports.Production != 3090 {
		t.Fatalf("production port = %d", c.Ports.Production)
	}
}

// TestLoadConfigAcceptsUTF8BOM: the installer rewrites dsh-ab.toml with Inno's
// LoadStringsFromFile/SaveStringsToFile, which only round-trips UTF-8 when the file carries a
// BOM. The program therefore has to accept a BOM-prefixed config, comments included.
func TestLoadConfigAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh-ab.toml")
	body := "\ufeff[ports]\nproduction = 4190\nhost = \"127.0.0.1\"   # 中文注释必须原样保留\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, rep := LoadConfig(path)
	if rep.FileErr != nil {
		t.Fatalf("a BOM-prefixed config must load: %v", rep.FileErr)
	}
	if c.Ports.Production != 4190 {
		t.Fatalf("production port = %d, want 4190", c.Ports.Production)
	}
}

// TestLoadConfigReportsAFileLevelFailure (O1): one bad key used to discard the whole
// file, run the program on defaults and leave a single WARN line deep in the log.
// The report carries the failure out - and "全部默认值" has to be literally true,
// so neither flavour of failure may leave a half-decoded struct behind.
func TestLoadConfigReportsAFileLevelFailure(t *testing.T) {
	cases := []struct{ name, body string }{
		{
			// The mistake a Windows user makes: one backslash in a TOML basic string.
			"invalid escape",
			"[ports]\nproduction = 4190\n\n[logs]\ndir = \"logs2\"\n\n[launch.env]\nDSH_HOME = \"D:\\dshab\\hijack\"\n",
		},
		{
			// A type error is caught while the file is being copied into the struct, so
			// the struct can already hold an arbitrary part of it (measured: logs.dir
			// kept "logs2" in 3 of 5 runs). "全部默认值" has to be true anyway.
			"incompatible type",
			"[logs]\ndir = \"logs2\"\nmax_files = 3\n\n[browser]\nopen_on_start = false\n\n[tray]\ntooltip = \"custom\"\n\n[health]\npoll_interval_s = 7\n\n[ports]\nproduction = \"abc\"\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dsh-ab.toml")
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			checkAFileLevelFailure(t, path)
		})
	}
}

// checkAFileLevelFailure pins the whole contract of a discarded config file: the
// failure is reported, every value in force is the documented default, and the
// status line and the notice say so.
func checkAFileLevelFailure(t *testing.T, path string) {
	t.Helper()
	c, rep := LoadConfig(path)
	if rep.FileErr == nil {
		t.Fatal("读不了的配置必须报成文件级失败")
	}
	if c.Ports.Production != 3090 || c.Logs.Dir != "logs" || c.Logs.MaxFiles != 20 ||
		c.Browser.OpenOnStart != true || c.Tray.Icon != "embedded" || c.Health.PollIntervalS != 30 {
		t.Fatalf("文件级失败后必须整份回退默认值，实际拿到：ports=%+v logs=%+v browser=%+v tray=%+v health=%+v",
			c.Ports, c.Logs, c.Browser, c.Tray, c.Health)
	}
	if got := rep.StatusLine(); got != "配置：读取失败，使用默认值" {
		t.Errorf("状态行 = %q", got)
	}
	title, text, ok := rep.Popup("C:\\log\\dsh-ab.log")
	if !ok {
		t.Fatal("文件级失败必须有一个提示框")
	}
	if !strings.Contains(title, "配置读取失败") {
		t.Errorf("提示框标题 = %q", title)
	}
	if !strings.Contains(text, "配置文件读取失败，本次使用全部默认值") {
		t.Errorf("提示框没有说明整份回退：\n%s", text)
	}
	if !strings.Contains(text, "C:\\log\\dsh-ab.log") {
		t.Errorf("提示框没有给出日志路径：\n%s", text)
	}
}

// TestConfigPopupDoesNotDoubleTheLogLabel (O1-1): the caller's note already names
// the log ("完整日志：<path>"), and Popup used to put a "日志：" label of its own in
// front of it, so both config dialogs read "日志：完整日志：D:...dsh-ab.log".
func TestConfigPopupDoesNotDoubleTheLogLabel(t *testing.T) {
	const note = "完整日志：C:\\x\\logs\\dsh-ab.log"
	cases := []struct {
		name string
		rep  ConfigReport
	}{
		{"文件级失败", ConfigReport{FileErr: errors.New("toml: line 5: invalid escape in string '\\d'")}},
		{"逐项回退", ConfigReport{Repairs: []string{"ports.production 无效，已回退为 3090"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, text, ok := c.rep.Popup(note)
			if !ok {
				t.Fatal("这一份报告必须有一个提示框")
			}
			if !strings.Contains(text, note) {
				t.Errorf("提示框没有把调用方的日志行原样带出来：\n%s", text)
			}
			if strings.Contains(text, "日志：完整日志：") {
				t.Errorf("提示框把日志前缀写了两遍：\n%s", text)
			}
		})
	}
	// A two-line note comes out verbatim too - the off-level one now names the path
	// as well, because off keeps errors.
	const offNote = "日志级别为 off：出错才写。\n日志路径：C:\\x\\logs\\dsh-ab.log"
	_, text, ok := ConfigReport{FileErr: errors.New("boom")}.Popup(offNote)
	if !ok || !strings.HasSuffix(text, offNote) {
		t.Errorf("off 档的日志说明没有原样带出：\n%s", text)
	}
}

// TestLoadConfigReportsPerItemRepairs (O1): the per-item fallbacks are counted and
// named, so "2 项已回退" in the status popup is not a number the user has to take
// on faith.
func TestLoadConfigReportsPerItemRepairs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh-ab.toml")
	body := "[ports]\nproduction = 99999\n\n[logs]\nmax_files = 0\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, rep := LoadConfig(path)
	if rep.FileErr != nil {
		t.Fatalf("这份文件解析得通：%v", rep.FileErr)
	}
	if len(rep.Repairs) != 2 {
		t.Fatalf("回退项计数 = %d，期望 2（%v）", len(rep.Repairs), rep.Repairs)
	}
	if c.Ports.Production != 3090 || c.Logs.MaxFiles != 20 {
		t.Fatalf("没有逐项回退：%+v / %+v", c.Ports, c.Logs)
	}
	if got := rep.StatusLine(); got != "配置：2 项已回退" {
		t.Errorf("状态行 = %q", got)
	}
	title, text, ok := rep.Popup("log")
	if !ok {
		t.Fatal("有回退项时必须提示")
	}
	if !strings.Contains(title+text, "2 项") {
		t.Errorf("提示框没有说清有几项无效：%s / %s", title, text)
	}
	for _, w := range rep.Repairs {
		if !strings.Contains(text, w) {
			t.Errorf("提示框里少了回退项 %q：\n%s", w, text)
		}
	}
}

// TestLoadConfigIgnoresKeysAnOlderVersionWrote: the four knobs the optimization round deleted
// (browser.url_path, health.auto_restart, [snapshot], [ui]) are still in the dsh-ab.toml of every
// installation made before it. An unknown key has to stay inert - not a parse error (that would show
// "配置读取失败" and throw the whole file away) and not a repair item either.
func TestLoadConfigIgnoresKeysAnOlderVersionWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh-ab.toml")
	body := `[ports]
	production = 4190

	[browser]
	open_on_start = false
	url_path = "/chat"

	[health]
	poll_interval_s = 45
	auto_restart = true

	[snapshot]
	git_exe = "runtime/git/cmd/git.exe"
	git_missing_reminder_min = 30

	[ui]
	language = "zh-CN"
	`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, rep := LoadConfig(path)
	if rep.FileErr != nil {
		t.Fatalf("一份老版本写的文件被当成解析失败：%v", rep.FileErr)
	}
	if len(rep.Repairs) != 0 {
		t.Fatalf("删掉的键不该产生任何回退项：%v", rep.Repairs)
	}
	if c.Ports.Production != 4190 || c.Browser.OpenOnStart || c.Health.PollIntervalS != 45 {
		t.Fatalf("认识的键没有生效：ports=%+v browser=%+v health=%+v", c.Ports, c.Browser, c.Health)
	}
}

// TestValidateRejectsBadValues: nonsense values fall back to the documented default, never to zero.
func TestValidateRejectsBadValues(t *testing.T) {
	c := DefaultConfig()
	c.Ports.Production = 0
	c.Ports.Host = ""
	c.Logs.Level = "loud"
	c.Logs.MaxFiles = 0
	c.Logs.MaxSizeMB = -1
	c.Health.PollIntervalS = 0
	c.Launch.StartTimeoutS = 0
	c.Launch.ProbeIntervalMs = 0
	warnings := c.Validate()
	if len(warnings) == 0 {
		t.Fatal("bad values must produce warnings")
	}
	if c.Ports.Production != 3090 {
		t.Fatalf("ports not repaired: %+v", c.Ports)
	}
	if c.Ports.Host == "" {
		t.Fatal("host not repaired")
	}
	if c.Logs.Level != "auto" || c.Logs.MaxFiles != 20 || c.Logs.MaxSizeMB != 5 {
		t.Fatalf("logs not repaired: %+v", c.Logs)
	}
	if c.Launch.StartTimeoutS != 60 || c.Launch.ProbeIntervalMs != 500 {
		t.Fatalf("launch timing not repaired: %+v", c.Launch)
	}
}

// TestLogLevelCyclesAndWrites: item 3 of the menu cycles off -> auto -> full -> off.
// What each of the three levels keeps is pinned by TestLogLevelThresholds.
func TestLogLevelCyclesAndWrites(t *testing.T) {
	dir := t.TempDir()
	lg := NewLogger(dir, "off", 20, 5)
	t.Cleanup(lg.Close) // Windows will not delete the directory while the log handle is open
	if lg.Level() != "off" {
		t.Fatalf("level = %q", lg.Level())
	}
	if got := lg.NextLevel(); got != "auto" {
		t.Fatalf("off -> %q, want auto", got)
	}
	lg.SetLevel(lg.NextLevel())
	if got := lg.NextLevel(); got != "full" {
		t.Fatalf("auto -> %q, want full", got)
	}
	lg.SetLevel(lg.NextLevel())
	if got := lg.NextLevel(); got != "off" {
		t.Fatalf("full -> %q, want off", got)
	}
	lg.Info("hello from full")
	if !lg.Wrote() {
		t.Fatal("full level must write a log file")
	}
	lg.SetLevel("off")
	lg.Info("must not land on disk")
	data, err := os.ReadFile(lg.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "must not land on disk") {
		t.Fatal("off level wrote to the log file")
	}
}

// TestLogNoteTellsTheTruthAboutAnUnwritableLogDirectory (A5): every failure popup
// used to close with "日志级别为 off，本次没有写日志文件。" whenever nothing had
// been written, so a level that means to write but cannot (no directory, no
// permission, a full disk) reported a setting the user never chose - and hid the
// real problem. The note now follows the level and the last write failure.
func TestLogNoteTellsTheTruthAboutAnUnwritableLogDirectory(t *testing.T) {
	// A regular file where the log *directory* has to be created: MkdirAll then
	// fails on every platform, without depending on ACLs a test cannot set.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	lg := NewLogger(filepath.Join(blocker, "logs"), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)

	a, _ := newTestApp(t, DefaultConfig(), nil)
	a.lg = lg
	// auto 档的写入门槛是 warn/debug（Info 已归 full 档），用一条 auto 真会写的行。
	lg.Warnf("这一行写不进去")

	if lg.WriteError() == nil {
		t.Fatal("日志目录不可写，WriteError() 却是 nil")
	}
	if lg.Wrote() {
		t.Fatal("日志目录不可写，Wrote() 却是 true")
	}
	note := a.logNote()
	if !strings.Contains(note, "日志目录不可写") || !strings.Contains(note, lg.Path()) {
		t.Fatalf("日志写不进去时没有说实话：%q", note)
	}
	if strings.Contains(note, "级别为 off") {
		t.Fatalf("日志级别不是 off，却说成了 off：%q", note)
	}

	// off 不再等于「不写日志」：它照样记 error，所以那句
	// 「本次没有写日志文件」已经不成立——说明只能讲清档位，绝不能声称没有日志。
	writable := NewLogger(t.TempDir(), LevelOff, 20, 5)
	t.Cleanup(writable.Close)
	a.lg = writable
	note = a.logNote()
	if !strings.Contains(note, LevelOff) || !strings.Contains(note, writable.Path()) {
		t.Fatalf("off 档的说明没有给出档位与日志路径：%q", note)
	}
	if strings.Contains(note, "没有写日志") {
		t.Fatalf("off 档也会在出错时写日志，说明却说没有日志：%q", note)
	}
}

// TestLogLevelThresholds pins the three level gates with one
// batch of lines: off keeps errors only, auto adds warnings and debug, full keeps
// everything - info included, and the child's output, which is tagged with the
// slot it came from because one log file accumulates both slots across restarts.
func TestLogLevelThresholds(t *testing.T) {
	cases := []struct {
		level string
		want  []string
		deny  []string
	}{
		{LevelOff, []string{"markErr"}, []string{"markWarn", "markDebug", "markInfo", "markChild"}},
		{LevelAuto, []string{"markErr", "markWarn", "markDebug"}, []string{"markInfo", "markChild"}},
		{LevelFull, []string{"markErr", "markWarn", "markDebug", "markInfo", "child-slot-a markChildA", "child-slot-b markChildB"}, nil},
	}
	for _, c := range cases {
		lg := NewLogger(t.TempDir(), c.level, 20, 5)
		lg.Errorf("markErr")
		lg.Warnf("markWarn")
		lg.Debugf("markDebug")
		lg.Info("markInfo")
		lg.Child(slotA, "markChildA")
		lg.Child(slotB, "markChildB")
		lg.Close() // Windows 要关掉句柄才能读，也能证明每一行真的落了盘
		data, err := os.ReadFile(lg.Path())
		if err != nil {
			t.Fatalf("级别 %s：读日志：%v", c.level, err)
		}
		for _, want := range c.want {
			if !strings.Contains(string(data), want) {
				t.Errorf("级别 %s：日志里缺少 %q：\n%s", c.level, want, data)
			}
		}
		for _, deny := range c.deny {
			if strings.Contains(string(data), deny) {
				t.Errorf("级别 %s：日志里不该有 %q：\n%s", c.level, deny, data)
			}
		}
	}
}

// TestLogRotatesBySize: full logging must not grow one unbounded file.
func TestLogRotatesBySize(t *testing.T) {
	dir := t.TempDir()
	lg := NewLogger(dir, "full", 3, 1) // 1 MB per file
	t.Cleanup(lg.Close)
	chunk := strings.Repeat("x", 64*1024)
	for i := 0; i < 40; i++ { // ~2.5 MB
		lg.Info(chunk)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected a rotated file, got %d file(s)", len(entries))
	}
	if len(entries) > 3 {
		t.Fatalf("max_files=3 exceeded: %d files", len(entries))
	}
}

// TestLogRotationRespectsMaxFilesAtSteadyState: max_files counts every file kept, the live
// log included, so many rotations must not creep one file above the configured limit.
func TestLogRotationRespectsMaxFilesAtSteadyState(t *testing.T) {
	dir := t.TempDir()
	const maxFiles = 3
	lg := NewLogger(dir, "full", maxFiles, 1) // 1 MB per file
	t.Cleanup(lg.Close)
	chunk := strings.Repeat("x", 64*1024)
	for i := 0; i < 130; i++ { // ~8 MB, well past steady state
		lg.Info(chunk)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > maxFiles {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("max_files=%d exceeded: %d files kept (%v)", maxFiles, len(entries), names)
	}
}
