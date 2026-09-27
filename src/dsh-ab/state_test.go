package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestState(t *testing.T) (*State, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	st, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return st, dir
}

// TestFreshInstallHasNoRegistration: a clean install must show 'not registered' and write an active pointer.
func TestFreshInstallHasNoRegistration(t *testing.T) {
	st, dir := newTestState(t)
	if st.ActiveSlot() != "slot-a" {
		t.Fatalf("fresh active slot = %q, want slot-a", st.ActiveSlot())
	}
	if st.RollbackPhase() != phaseNone {
		t.Fatalf("fresh rollback phase = %q, want none", st.RollbackPhase())
	}
	if _, err := os.Stat(filepath.Join(dir, "active.json")); err != nil {
		t.Fatalf("active.json must exist on a fresh install: %v", err)
	}
}

// TestReloadAdoptsARegistrationWrittenBySomebodyElse: the maintenance agent writes
// state\pending.json directly, and the tray has to notice it. Without Reload the menu
// kept saying 「槽位切换」and 重启 read a Pending from startup time and silently did not
// switch at all (2026-09-24，真机报告).
func TestReloadAdoptsARegistrationWrittenBySomebodyElse(t *testing.T) {
	st, dir := newTestState(t)

	// 外部写入：绕开 State.Register，直接落盘，就像维护代理做的那样。
	mustWrite(t, filepath.Join(dir, "pending.json"), `{"target_slot":"slot-b"}`)

	if !st.Reload() {
		t.Fatal("外部写进 pending.json 的登记没有被察觉")
	}
	if st.RollbackPhase() != phasePending {
		t.Fatalf("Reload 后 phase = %q，应为 pending", st.RollbackPhase())
	}
	if st.Pending == nil || st.Pending.TargetSlot != "slot-b" {
		t.Fatalf("Reload 后登记 = %+v，应为 slot-b", st.Pending)
	}
	// 刷新绝不回写盘：回写就是亲手抹掉别人刚写下的登记。
	if _, err := os.Stat(filepath.Join(dir, "pending.json")); err != nil {
		t.Fatalf("Reload 之后 pending.json 必须还在盘上：%v", err)
	}
	if st.Reload() {
		t.Fatal("同一个登记重复读一次不该再报变化")
	}
}

// TestReloadKeepsTheLastGoodValueForABrokenFile: a corrupt file must not clear the
// in-memory state - that would send the menu back to its default and hide a
// registration that really is on disk - and it must not grow one warning per tick.
// A missing pending.json is the other thing entirely: that is "nothing registered".
func TestReloadKeepsTheLastGoodValueForABrokenFile(t *testing.T) {
	st, dir := newTestState(t)
	if err := st.Register("slot-b"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "pending.json"), "{ 这不是 JSON")

	if st.Reload() {
		t.Fatal("坏文件不该被算成一次状态变化")
	}
	if st.RollbackPhase() != phasePending {
		t.Fatalf("坏文件把内存里的登记清掉了：phase = %q", st.RollbackPhase())
	}
	if len(st.Warnings) == 0 {
		t.Fatal("坏文件必须留下一条警告")
	}
	before := len(st.Warnings)
	st.Reload()
	st.Reload()
	if len(st.Warnings) != before {
		t.Fatalf("同样的坏文件每 2 秒刷一次就多一条警告：%d -> %d", before, len(st.Warnings))
	}

	if err := os.Remove(filepath.Join(dir, "pending.json")); err != nil {
		t.Fatal(err)
	}
	if !st.Reload() {
		t.Fatal("pending.json 消失后必须回到默认态")
	}
	if st.RollbackPhase() != phaseNone {
		t.Fatalf("登记文件消失后 phase = %q，应为 none", st.RollbackPhase())
	}
}

// TestRollbackTwoStates: not registered -> pending -> not registered again, with
// nothing archived in between (user 2026-09-19).
func TestRollbackTwoStates(t *testing.T) {
	st, dir := newTestState(t)
	if err := st.Register("slot-b"); err != nil {
		t.Fatal(err)
	}
	if st.RollbackPhase() != phasePending {
		t.Fatalf("phase = %q, want pending", st.RollbackPhase())
	}
	if _, err := os.Stat(filepath.Join(dir, "pending.json")); err != nil {
		t.Fatalf("a registration must be on disk: %v", err)
	}
	if err := st.Undo(); err != nil {
		t.Fatal(err)
	}
	if st.RollbackPhase() != phaseNone {
		t.Fatalf("phase after undo = %q, want none", st.RollbackPhase())
	}
	if _, err := os.Stat(filepath.Join(dir, "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("undo must clear pending.json, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "undone.json")); !os.IsNotExist(err) {
		t.Fatalf("there is no third state: undone.json must not exist, err=%v", err)
	}
}

// TestLoadStateDropsTheOldArchive: a state/ written by the older three-state
// build must not keep an archive the current program cannot read (user 2026-09-19).
func TestLoadStateDropsTheOldArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "undone.json"),
		`[{"kind":"rollback","target_slot":"slot-b","time":"2026-09-19T00:00:00+08:00"}]`)
	if _, err := LoadState(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "undone.json")); !os.IsNotExist(err) {
		t.Fatalf("旧的归档文件必须被清掉，err=%v", err)
	}
}

// TestPendingSurvivesRestart: launch reads pending to pick the slot, then writes active and clears the registration.
func TestPendingSurvivesRestart(t *testing.T) {
	st, dir := newTestState(t)
	if err := st.Register("slot-b"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Pending == nil || reloaded.Pending.TargetSlot != "slot-b" {
		t.Fatalf("pending lost across restart: %+v", reloaded.Pending)
	}
	target, ok := reloaded.ConsumePending()
	if !ok || target != "slot-b" {
		t.Fatalf("ConsumePending = (%q,%v)", target, ok)
	}
	if reloaded.ActiveSlot() != "slot-b" {
		t.Fatalf("active after consume = %q", reloaded.ActiveSlot())
	}
	if reloaded.Pending != nil {
		t.Fatal("registration must be cleared after it takes effect")
	}
	again, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.ActiveSlot() != "slot-b" || again.Pending != nil {
		t.Fatalf("reloaded state = %q / %+v", again.ActiveSlot(), again.Pending)
	}
}

// TestStateFilesArePlainAndAtomic: the files are the single source of truth and must be valid JSON with no temp leftovers.
func TestStateFilesArePlainAndAtomic(t *testing.T) {
	st, dir := newTestState(t)
	if err := st.Register("slot-b"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			t.Fatalf("unexpected leftover file %q", e.Name())
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s is not valid JSON: %v", e.Name(), err)
		}
	}
}

func writeSlot(t *testing.T, root, slot string, cfg *Config) {
	t.Helper()
	slotRoot := filepath.Join(root, slot)
	mustWrite(t, filepath.Join(slotRoot, filepath.FromSlash(cfg.Launch.NodeExe)), "node")
	mustWrite(t, filepath.Join(slotRoot, filepath.FromSlash(cfg.Launch.DshEntry)), "dsh")
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDecideSlotNoPendingRunsActive: a normal restart must not switch slots.
func TestDecideSlotNoPendingRunsActive(t *testing.T) {
	cfg := DefaultConfig()
	root := t.TempDir()
	writeSlot(t, root, "slot-a", cfg)
	st, _ := newTestState(t)
	slot, consumed, warn := decideSlot(root, cfg, st, true)
	if slot != "slot-a" || consumed || warn != "" {
		t.Fatalf("decideSlot = (%q,%v,%q)", slot, consumed, warn)
	}
}

// TestDecideSlotConsumesPending: the tray's 重启 applies a registered switch, and it sticks.
func TestDecideSlotConsumesPending(t *testing.T) {
	cfg := DefaultConfig()
	root := t.TempDir()
	writeSlot(t, root, "slot-a", cfg)
	writeSlot(t, root, "slot-b", cfg)
	st, _ := newTestState(t)
	if err := st.Register("slot-b"); err != nil {
		t.Fatal(err)
	}
	slot, consumed, warn := decideSlot(root, cfg, st, true)
	if slot != "slot-b" || !consumed || warn != "" {
		t.Fatalf("decideSlot = (%q,%v,%q)", slot, consumed, warn)
	}
	if st.ActiveSlot() != "slot-b" {
		t.Fatalf("active = %q, want slot-b", st.ActiveSlot())
	}
}

// TestDecideSlotKeepsPendingWhenTargetMissing: slot-b is absent right after install, so the run must stay on slot-a and the registration must survive.
func TestDecideSlotKeepsPendingWhenTargetMissing(t *testing.T) {
	cfg := DefaultConfig()
	root := t.TempDir()
	writeSlot(t, root, "slot-a", cfg)
	st, _ := newTestState(t)
	if err := st.Register("slot-b"); err != nil {
		t.Fatal(err)
	}
	slot, consumed, warn := decideSlot(root, cfg, st, true)
	if slot != "slot-a" || consumed {
		t.Fatalf("decideSlot = (%q,%v), want slot-a and not consumed", slot, consumed)
	}
	if warn == "" {
		t.Fatal("a missing target slot must be reported")
	}
	if st.Pending == nil {
		t.Fatal("the registration must not be dropped")
	}
}

// TestSlotInstalledRequiresBothNodeAndEntry: a half-copied slot is not installed.
func TestSlotInstalledRequiresBothNodeAndEntry(t *testing.T) {
	cfg := DefaultConfig()
	root := t.TempDir()
	if slotInstalled(root, cfg, "slot-b") {
		t.Fatal("absent slot reported as installed")
	}
	mustWrite(t, filepath.Join(root, "slot-b", filepath.FromSlash(cfg.Launch.NodeExe)), "node")
	if slotInstalled(root, cfg, "slot-b") {
		t.Fatal("slot without the dsh entry reported as installed")
	}
	mustWrite(t, filepath.Join(root, "slot-b", filepath.FromSlash(cfg.Launch.DshEntry)), "dsh")
	if !slotInstalled(root, cfg, "slot-b") {
		t.Fatal("complete slot reported as missing")
	}
}

// TestChildEnvPinsDshHomeToTheSlot: DSH_HOME is derived from the slot and cannot be overridden from config.
func TestChildEnvPinsDshHomeToTheSlot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Launch.Env = map[string]string{"DSH_HOME": "/somewhere/else", "DSH_TELEMETRY": "0"}
	r := NewRunner(`C:\root`, cfg, nil)
	slotRoot := filepath.Join(`C:\root`, "slot-b")
	env := r.childEnv(slotRoot)
	var homes []string
	for _, kv := range env {
		if len(kv) >= 9 && kv[:9] == "DSH_HOME=" {
			homes = append(homes, kv)
		}
		if len(kv) >= 15 && kv[:15] == "DSH_TELEMETRY=" {
			if kv != "DSH_TELEMETRY=0" {
				t.Fatalf("extra env lost: %q", kv)
			}
		}
	}
	if len(homes) != 1 {
		t.Fatalf("expected exactly one DSH_HOME in env, got %v (inherited %d entries)", homes, len(env))
	}
	if homes[0] != "DSH_HOME="+filepath.Join(slotRoot, "data") {
		t.Fatalf("DSH_HOME = %q, want the slot's data dir", homes[0])
	}
}

// TestCommandLineUsesSlotPathsAndPort: the child is started from the slot, on the slot's port, with extra args appended.
func TestCommandLineUsesSlotPathsAndPort(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Launch.ExtraArgs = []string{"--verbose"}
	root := t.TempDir()
	writeSlot(t, root, "slot-a", cfg)
	r := NewRunner(root, cfg, nil)
	slotRoot := filepath.Join(root, "slot-a")
	exe, args, err := r.commandLine(slotRoot, 3190)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	wantExe := filepath.Join(slotRoot, filepath.FromSlash(cfg.Launch.NodeExe))
	if exe != wantExe {
		t.Fatalf("exe = %q, want %q", exe, wantExe)
	}
	// --no-open keeps dsh from opening a browser on its own: DSH-AB opens it, and only after
	// the port probe says the instance is actually answering.
	want := []string{filepath.Join(slotRoot, filepath.FromSlash(cfg.Launch.DshEntry)), "web", "--port", "3190", "--no-open", "--verbose"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestLaunchPathsAbsoluteAreUsedAsWritten: an absolute launch.node_exe /
// launch.dsh_entry used to be spliced behind the slot root ("slot-b\C:\..."), so
// the start failed on a path the user never typed. An absolute value is now used
// exactly as written - both for the command line and for "is this slot installed".
func TestLaunchPathsAbsoluteAreUsedAsWritten(t *testing.T) {
	outside := t.TempDir()
	exe := filepath.Join(outside, "node.exe")
	entry := filepath.Join(outside, "bin.js")
	mustWrite(t, exe, "node")
	mustWrite(t, entry, "dsh")

	cfg := DefaultConfig()
	cfg.Launch.NodeExe = exe
	cfg.Launch.DshEntry = entry

	root := t.TempDir()
	slotRoot := filepath.Join(root, "slot-a")
	if err := os.MkdirAll(slotRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Neither file is inside the slot: a program that still joined them would call
	// this slot uninstalled and refuse to start - exactly the reported behaviour.
	if !slotInstalled(root, cfg, "slot-a") {
		t.Fatal("绝对路径的 node 与 dsh 入口都存在时，槽位应算作已安装")
	}
	r := NewRunner(root, cfg, nil)
	gotExe, args, err := r.commandLine(slotRoot, 3190)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	if gotExe != exe {
		t.Errorf("exe = %q, want %q（绝对路径必须按原样使用）", gotExe, exe)
	}
	if len(args) == 0 || args[0] != entry {
		t.Errorf("入口 = %v, want %q", args, entry)
	}
}

// TestLaunchPathsRelativeStayRelativeToTheSlotRoot: the documented default is
// still "relative to the slot root", so the fix for absolute paths changed nothing
// for the shipped configuration.
func TestLaunchPathsRelativeStayRelativeToTheSlotRoot(t *testing.T) {
	cfg := DefaultConfig()
	root := t.TempDir()
	writeSlot(t, root, "slot-a", cfg)
	slotRoot := filepath.Join(root, "slot-a")
	if !slotInstalled(root, cfg, "slot-a") {
		t.Fatal("完整槽位应算作已安装")
	}
	r := NewRunner(root, cfg, nil)
	exe, args, err := r.commandLine(slotRoot, 3190)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	if want := filepath.Join(slotRoot, filepath.FromSlash(cfg.Launch.NodeExe)); exe != want {
		t.Errorf("exe = %q, want %q", exe, want)
	}
	if want := filepath.Join(slotRoot, filepath.FromSlash(cfg.Launch.DshEntry)); len(args) == 0 || args[0] != want {
		t.Errorf("入口 = %v, want %q", args, want)
	}
}

// TestCommandLineRejectsMissingSlot: a slot without node/dsh must fail loudly instead of starting nothing.
func TestCommandLineRejectsMissingSlot(t *testing.T) {
	cfg := DefaultConfig()
	r := NewRunner(t.TempDir(), cfg, nil)
	if _, _, err := r.commandLine(filepath.Join(t.TempDir(), "slot-b"), 3190); err == nil {
		t.Fatal("expected an error for an uninstalled slot")
	}
}

// TestPortHelpers: the liveness probe the tray uses to decide whether to open the browser.
func TestPortHelpers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !PortOpen("127.0.0.1", port, time.Second) {
		t.Fatal("open port reported as closed")
	}
	if ready, exited := waitPortLive("127.0.0.1", port, 2*time.Second, 100*time.Millisecond, func() bool { return true }); !ready || exited {
		t.Fatalf("waitPortLive ready=%v exited=%v on a listening port, want ready=true", ready, exited)
	}
	ln.Close()
	if PortOpen("127.0.0.1", port, 300*time.Millisecond) {
		t.Fatal("closed port reported as open")
	}
}

// TestPopupThrottle: the same fault must not pop twice inside the configured window.
func TestPopupThrottle(t *testing.T) {
	th := newThrottle(5 * time.Minute)
	if !th.Allow("port_down") {
		t.Fatal("first fault must be shown")
	}
	if th.Allow("port_down") {
		t.Fatal("repeat fault inside the window must be suppressed")
	}
	if !th.Allow("process_dead") {
		t.Fatal("a different fault is a different category")
	}
	th2 := newThrottle(0)
	if !th2.Allow("x") || !th2.Allow("x") {
		t.Fatal("zero window must not throttle")
	}
}
