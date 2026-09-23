package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// closedPort returns a TCP port that nothing is listening on: it is opened and
// immediately released, so a probe against it fails fast the way a not-yet-ready
// dsh port does.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// TestLineTailKeepsOnlyTheLastLines: the always-on tail must be bounded, and it
// must hand the lines back oldest first so a failure reads in real order.
func TestLineTailKeepsOnlyTheLastLines(t *testing.T) {
	r := newLineTail(3)
	for _, s := range []string{"one", "two", "three", "four", "five"} {
		r.push(s)
	}
	got := r.lines()
	want := []string{"three", "four", "five"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines() = %v, want %v", got, want)
	}
	if (*lineTail)(nil).lines() != nil {
		t.Fatal("a nil tail must report no lines instead of panicking")
	}
}

// TestRunnerKeepsChildOutputRegardlessOfLogLevel is the diagnostic floor:
// logs.level = auto discards the child's output from the log file, but the
// Runner must still hold it, otherwise a startup failure has nothing to show.
func TestRunnerKeepsChildOutputRegardlessOfLogLevel(t *testing.T) {
	r, pw := newPipeRunner(t)
	if _, err := pw.WriteString("child says: profile bootstrap failed\r\nother line\n"); err != nil {
		t.Fatalf("write to child pipe: %v", err)
	}
	pw.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if lines, _ := r.LastStartOutput(); len(lines) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := r.LastStartOutput()
	want := []string{"child says: profile bootstrap failed", "other line"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LastStartOutput() = %v, want %v", got, want)
	}
}

// TestWaitPortLiveStopsWhenTheChildDies: the observed shape is a child
// that dies seconds after it starts. The launcher must stop waiting then, not
// sit out the whole 60 s startup budget and report a timeout as well.
func TestWaitPortLiveStopsWhenTheChildDies(t *testing.T) {
	alive := true
	calls := 0
	start := time.Now()
	ready, exited := waitPortLive("127.0.0.1", closedPort(t), 60*time.Second, 100*time.Millisecond, func() bool {
		calls++
		if calls > 2 {
			alive = false
		}
		return alive
	})
	if ready || !exited {
		t.Fatalf("ready=%v exited=%v, want ready=false exited=true", ready, exited)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("waited %v for a child that had already exited", d)
	}
}

// TestWaitPortLiveReportsReady: a listening port is readiness, whatever the
// child's liveness says afterwards.
func TestWaitPortLiveReportsReady(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	ready, exited := waitPortLive("127.0.0.1", port, 2*time.Second, 100*time.Millisecond, func() bool { return true })
	if !ready || exited {
		t.Fatalf("ready=%v exited=%v, want ready=true exited=false", ready, exited)
	}
}

// TestWaitPortLiveReportsTimeoutWhileTheChildLives: a child that stays alive and
// never serves is a timeout, not a death.
func TestWaitPortLiveReportsTimeoutWhileTheChildLives(t *testing.T) {
	ready, exited := waitPortLive("127.0.0.1", closedPort(t), 400*time.Millisecond, 100*time.Millisecond, func() bool { return true })
	if ready || exited {
		t.Fatalf("ready=%v exited=%v, want ready=false exited=false", ready, exited)
	}
}

// TestHealthFaultIsAtMostOneFaultPerSample: one sample may name at most one
// fault, and a dead process is never reported as a port fault as well. This is
// the code-level form of the single popup mechanism.
func TestHealthFaultIsAtMostOneFaultPerSample(t *testing.T) {
	cases := []struct {
		running, portOpen, onDead, onPortDown bool
		want                                  string
	}{
		{true, true, true, true, ""},
		{false, false, true, true, faultProcessDead},
		{false, false, false, false, ""},
		{false, false, false, true, ""},
		{true, false, true, true, faultPortDown},
		{true, false, true, false, ""},
	}
	for _, c := range cases {
		got := healthFault(c.running, c.portOpen, c.onDead, c.onPortDown)
		if got != c.want {
			t.Fatalf("healthFault(%v,%v,%v,%v) = %q, want %q",
				c.running, c.portOpen, c.onDead, c.onPortDown, got, c.want)
		}
	}
}

// TestStartupFailureTextCarriesTheChildsOwnWords: the popup of a failed start
// must say what failed, show the tail of the child's output, and name the log.
func TestStartupFailureTextCarriesTheChildsOwnWords(t *testing.T) {
	tail := []string{"first", "second", "third"}
	got := startupFailureText(startFailure{cause: "dsh 进程在端口 3190 就绪前就退出了（exit status 1）。", tail: tail, launched: true, logNote: "日志：C:\\x\\logs\\dsh-ab.log", maxLines: 12})

	for _, want := range []string{"exit status 1", "first", "second", "third", "C:\\x\\logs\\dsh-ab.log"} {
		if !strings.Contains(got, want) {
			t.Fatalf("startup failure text is missing %q:\n%s", want, got)
		}
	}
}

// TestStartupFailureTextKeepsTheLastLinesAndSaysSo: a long tail is cut from the
// front — the newest lines are the ones that explain a death — and the text says
// how much was dropped instead of pretending it showed everything.
func TestStartupFailureTextKeepsTheLastLinesAndSaysSo(t *testing.T) {
	var tail []string
	for i := 1; i <= 30; i++ {
		tail = append(tail, "line-"+strconv.Itoa(i))
	}
	got := startupFailureText(startFailure{cause: "原因。", tail: tail, launched: true, logNote: "日志：x", maxLines: 5})

	if strings.Contains(got, "line-25") || strings.Contains(got, "line-1\n") {
		t.Fatalf("kept lines older than the last 5:\n%s", got)
	}
	for i := 26; i <= 30; i++ {
		if !strings.Contains(got, "line-"+strconv.Itoa(i)) {
			t.Fatalf("dropped the newest line line-%d:\n%s", i, got)
		}
	}
	if !strings.Contains(got, "30") {
		t.Fatalf("must say how many lines there were:\n%s", got)
	}
}

// TestStartupFailureTextSaysWhenThereWasNoOutput: silence is a finding too — the
// text must not look like it simply forgot to include the output.
func TestStartupFailureTextSaysWhenThereWasNoOutput(t *testing.T) {
	got := startupFailureText(startFailure{cause: "原因。", launched: true, logNote: "日志：x", maxLines: 12})
	if !strings.Contains(got, "没有产生任何输出") {
		t.Fatalf("a child that printed nothing must be reported as such:\n%s", got)
	}
	if !strings.Contains(got, "日志：x") {
		t.Fatalf("the log pointer is missing:\n%s", got)
	}
}

// TestRunnerReportsWhyTheChildStopped keeps the exit reason available to the
// failure popup: "the process is gone" alone used to be the whole story.
func TestRunnerReportsWhyTheChildStopped(t *testing.T) {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("cmd.exe"); err == nil {
		cmd = exec.Command("cmd.exe", "/c", "exit 3")
	} else {
		cmd = exec.Command("does-not-exist.exe")
	}
	r := &Runner{tail: newLineTail(childTailLines)}
	exited := make(chan struct{})
	r.cmd, r.exited = cmd, exited
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a failing child here: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		r.setExitNote(err)
	}
	close(exited)

	if note := r.ExitNote(); note == "" {
		t.Fatal("ExitNote() is empty for a child that ended with an error")
	}
}

// --- a failed start is always told, and told once ------------------------------

// newTestApp builds the smallest App a startup-failure report needs, with the
// popup captured instead of shown.
func newTestApp(t *testing.T, cfg *Config, tail []string) (*App, *[]string) {
	t.Helper()
	// These callers stand in for a child that was launched and did say something.
	run := &Runner{tail: newLineTail(childTailLines), launched: true}
	for _, line := range tail {
		run.tail.push(line)
	}
	return newTestAppWithRunner(t, cfg, run)
}

// newTestAppWithRunner is newTestApp for a test that needs the Runner itself,
// e.g. to run a real Start against it.
func newTestAppWithRunner(t *testing.T, cfg *Config, run *Runner) (*App, *[]string) {
	t.Helper()
	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	t.Cleanup(lg.Close)
	run.lg = lg
	pops := &[]string{}
	a := &App{
		cfg:    cfg,
		run:    run,
		lg:     lg,
		faults: newThrottle(time.Duration(cfg.Health.PopupThrottleMin) * time.Minute),
		popup:  func(title, text string, isErr bool) { *pops = append(*pops, title+"\n"+text) },
		// No tray icon exists outside systray.Run, so the tooltip has no sink here.
		tooltip: func(string) {},
	}
	return a, pops
}

// TestStartFailurePopupIsUnconditional: health.popup_on_process_dead
// governs the health loop, not the report of a start that failed. It is also the
// one popup that must carry the child's own words, because "详见日志" was a
// promise the old code could not keep.
func TestStartFailurePopupIsUnconditional(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Health.PopupOnProcessDead = false
	cfg.Health.PopupOnPortDown = false

	a, pops := newTestApp(t, cfg, []string{"dsh: cannot read plugin cordis-plugin-timer", "dsh: exited"})
	a.run.setExitNote(errExitStatus{})
	a.reportStartFailure(startFailure{cause: a.portFailureCause("slot-a", cfg.Ports.Production, true, false), exited: true})
	a.flushPopups() // the report only queues; the lock owner shows it

	if len(*pops) != 1 {
		t.Fatalf("startup failure produced %d popups, want exactly 1", len(*pops))
	}
	text := (*pops)[0]
	for _, want := range []string{"cordis-plugin-timer", "exit status"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the startup popup does not carry the child's own output (%q missing):\n%s", want, text)
		}
	}
}

// TestPortFailureCauseNamesThePortThatWasAlreadyTaken: a port that already
// answered before this start is the one port failure whose cause is known -
// another DSH-AB (every installation defaults to the same production port) or a
// dsh that never went away - so the popup says that instead of a bare timeout. A
// start whose port was free keeps the old wording.
func TestPortFailureCauseNamesThePortThatWasAlreadyTaken(t *testing.T) {
	a, _ := newTestApp(t, DefaultConfig(), nil)

	got := a.portFailureCause("slot-a", 3190, false, true)
	for _, want := range []string{"3190", "启动前就已经被占用", "同一个生产端口", "没退干净"} {
		if !strings.Contains(got, want) {
			t.Fatalf("端口启动前已被占用，原因里却没有 %q：\n%s", want, got)
		}
	}

	free := a.portFailureCause("slot-a", 3190, false, false)
	if strings.Contains(free, "被占用") {
		t.Fatalf("启动前端口是空的，却报告被占用：%s", free)
	}
	if !strings.Contains(free, "还活着") {
		t.Fatalf("进程还活着时的原因变了：%s", free)
	}
}

// TestABusyPortNeverStartsDsh：端口在启动前就被别人占着时**根本不去启动
// dsh**，直接报一次失败。起也起不来，而那个端口上的应答是别人的——照旧往下走只会把别人的页面当成
// 自己的（2026-09-22 真机实测：日志写「端口 N 已就绪」、托盘显示运行中、浏览器开到别人页面上）。
// 端口空着时这条捷径不许走：那会变成「能启动的也被拒了」。
func TestABusyPortNeverStartsDsh(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	cfg := DefaultConfig()
	cfg.Ports.Host = "127.0.0.1"
	cfg.Ports.Production = busy.Addr().(*net.TCPAddr).Port

	// 这个 Runner 必须带 launched=false：本用例要断言的正是「没有启动过子进程」，而 newTestApp 的
	// Runner 是「已经启动过」的替身。
	a, pops := newTestAppWithRunner(t, cfg, &Runner{tail: newLineTail(childTailLines), cfg: cfg})
	a.root = t.TempDir()
	a.st = &State{active: "slot-a"}

	a.launchLocked(true)
	a.flushPopups()

	if _, launched := a.run.LastStartOutput(); launched {
		t.Fatal("端口被别人占着，却还是启动了 dsh 子进程（这种情况只该直接报错）")
	}
	if len(*pops) != 1 || !strings.Contains((*pops)[0], "启动前就已经被占用") {
		t.Fatalf("没有给出恰好一条「端口已被占用」的弹窗：%v", *pops)
	}

	// 端口空着：必须走到真正的启动路径（这里槽是空的，于是它以「槽内没有 node」失败），
	// 弹窗里因此不该出现「被占用」。
	cfg2 := DefaultConfig()
	cfg2.Ports.Host = "127.0.0.1"
	cfg2.Ports.Production = closedPort(t)
	a2, pops2 := newTestAppWithRunner(t, cfg2, &Runner{tail: newLineTail(childTailLines), cfg: cfg2})
	a2.root = t.TempDir()
	a2.st = &State{active: "slot-a"}
	a2.launchLocked(true)
	a2.flushPopups()
	if len(*pops2) != 1 || strings.Contains((*pops2)[0], "被占用") {
		t.Fatalf("端口是空的，却按「端口被占用」处理了：%v", *pops2)
	}
}

// TestStartFailureIsLoggedEvenWhenTheThrottleSilencesThePopup: the popup is
// throttled like every other fault, but the diagnostic never is — a user who
// clicked restart twice must still be able to read why.
func TestStartFailureIsLoggedEvenWhenTheThrottleSilencesThePopup(t *testing.T) {
	cfg := DefaultConfig()
	a, pops := newTestApp(t, cfg, []string{"first failure reason"})

	a.reportStartFailure(startFailure{cause: "原因一。"})
	a.reportStartFailure(startFailure{cause: "原因二。"})
	a.flushPopups()

	if len(*pops) != 1 {
		t.Fatalf("two failures inside the throttle window produced %d popups, want 1", len(*pops))
	}
	logged, err := os.ReadFile(a.lg.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{"原因一。", "原因二。", "first failure reason"} {
		if !strings.Contains(string(logged), want) {
			t.Fatalf("the log is missing %q after a silenced popup:\n%s", want, logged)
		}
	}
}

// TestStartFailureDoesNotShowThePreviousChildsOutput: the preflight
// check runs before a process exists, so an attempt it rejects has no output of
// its own: the lines still held belong to the child the restart just stopped, and
// showing them under "dsh 子进程输出" sent the user after the wrong error.
func TestStartFailureDoesNotShowThePreviousChildsOutput(t *testing.T) {
	root := t.TempDir() // slot-a exists but holds neither node nor a dsh entry
	cfg := DefaultConfig()
	run := &Runner{root: root, cfg: cfg, tail: newLineTail(childTailLines)}
	// Exactly what the restart leaves behind: Stop does not touch the tail.
	run.tail.push("dsh web: http://127.0.0.1:3190/?token=stale-from-the-previous-child")
	a, pops := newTestAppWithRunner(t, cfg, run)

	if err := run.Start(slotA, cfg.Ports.Production); err == nil {
		t.Fatal("Start must fail when the slot has no dsh entry")
	}
	if tail, launched := run.LastStartOutput(); launched || len(tail) != 0 {
		t.Fatalf("a start rejected by the preflight reported launched=%v tail=%v, want false and no lines", launched, tail)
	}

	a.reportStartFailure(startFailure{cause: "无法启动槽 slot-a：槽内没有 dsh 入口：x"})
	a.flushPopups()
	if len(*pops) != 1 {
		t.Fatalf("start failure produced %d popups, want exactly 1", len(*pops))
	}
	text := (*pops)[0]
	if strings.Contains(text, "stale-from-the-previous-child") {
		t.Fatalf("the popup shows the previous child's dying words: %s", text)
	}
	if !strings.Contains(text, "没有启动 dsh 子进程") {
		t.Fatalf("the popup must say this attempt never launched a child: %s", text)
	}
}

// TestStartupFailureTextSaysWhatToDoNext: the popup used to end at the log
// path, which tells the user what happened but not what to do. It now ends with
// one line that fits the case: a live child that never served, a dead child, a
// start the preflight refused, and a port that was already taken before the start
// (no restart can free that one - only another port can).
func TestStartupFailureTextSaysWhatToDoNext(t *testing.T) {
	cases := []struct {
		name string
		f    startFailure
		want []string
	}{
		{
			"进程还活着但端口没通",
			startFailure{cause: "原因。", launched: true, logNote: "日志：x", root: `C:\DSH-AB`},
			[]string{"重启", "ports.production"},
		},
		{
			"进程已退出",
			startFailure{cause: "原因。", launched: true, exited: true, logNote: "日志：x", root: `C:\DSH-AB`},
			[]string{"最后几行", "槽位切换"},
		},
		{
			"没起子进程（启动前检查拒绝）",
			startFailure{cause: "原因。", logNote: "日志：x", root: `C:\DSH-AB`},
			[]string{"槽位切换"},
		},
		{
			"启动前端口就被占用",
			startFailure{cause: "原因。", launched: true, busyBefore: true, logNote: "日志：x", root: `C:\DSH-AB`},
			[]string{`C:\DSH-AB\dsh-ab.toml`, "ports.production"},
		},
	}
	for _, c := range cases {
		got := startupFailureText(c.f)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s：弹窗里没有 %q：\n%s", c.name, want, got)
			}
		}
		// 顺序固定：日志行之后才是下一步。
		if strings.Index(got, "下一步：") < strings.LastIndex(got, "日志：x") {
			t.Errorf("%s：下一步没有排在日志行之后：\n%s", c.name, got)
		}
	}
}

// TestStartupFailureTextSaysWhenNoChildWasLaunched: "没有产生任何输出" describes a
// child that ran and stayed silent, which is not what a refused preflight did.
func TestStartupFailureTextSaysWhenNoChildWasLaunched(t *testing.T) {
	got := startupFailureText(startFailure{cause: "原因。", tail: []string{"stale line"}, logNote: "日志：x", maxLines: 12})
	if strings.Contains(got, "stale line") {
		t.Fatalf("a start that launched nothing must not show any output lines: %s", got)
	}
	if !strings.Contains(got, "没有启动 dsh 子进程") {
		t.Fatalf("the text must report that no child was launched: %s", got)
	}
	if !strings.Contains(got, "日志：x") {
		t.Fatalf("the log pointer is missing: %s", got)
	}
}

// errExitStatus stands in for the *exec.ExitError a dead child yields, without
// spawning one.
type errExitStatus struct{}

func (errExitStatus) Error() string { return "exit status 1" }

// --- the Runner must never deadlock in Start -------------------------------
//
// The probe installer showed the real thing: the launcher spawned dsh, then
// blocked on a lock it had already taken - it never logged, never waited for the
// port and never opened the browser, with dsh alive and stuck behind a full
// stdout pipe. No unit test started a real child through Start(), so the whole
// class went unnoticed.
func TestStartReturnsAndKeepsTheChild(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only entry point")
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		t.Skip("no COMSPEC to stand in for the node executable")
	}

	root := t.TempDir()
	slotRoot := filepath.Join(root, "slot-a")
	if err := os.MkdirAll(filepath.Join(slotRoot, "node"), 0o755); err != nil {
		t.Fatalf("mkdir node: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(slotRoot, "app"), 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	// A real, harmless child that starts, writes to both streams and exits. The
	// command line Start builds is "<exe> <entry> web --port N --no-open"; cmd
	// simply fails to run the entry, which is all this test needs.
	binary, err := os.ReadFile(comspec)
	if err != nil {
		t.Skipf("cannot read COMSPEC: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slotRoot, "node", "node.exe"), binary, 0o755); err != nil {
		t.Fatalf("write stand-in node: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slotRoot, "app", "dsh.js"), []byte("// stand-in entry\n"), 0o644); err != nil {
		t.Fatalf("write stand-in entry: %v", err)
	}

	lg := NewLogger(t.TempDir(), LevelFull, 20, 5)
	defer lg.Close()
	cfg := DefaultConfig()
	cfg.Launch.DshEntry = "app/dsh.js"
	r := NewRunner(root, cfg, lg)

	done := make(chan error, 1)
	go func() { done <- r.Start("slot-a", 3190) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Start did not return: the Runner mutex is taken twice (self-deadlock), so the child would sit behind a full stdout pipe")
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && r.Running() {
		time.Sleep(50 * time.Millisecond)
	}
	if r.Running() {
		t.Fatal("the stand-in child is still reported as running")
	}
	r.Stop()
	if r.Slot() != "" {
		t.Fatalf("Slot() = %q after Stop, want empty", r.Slot())
	}
}
