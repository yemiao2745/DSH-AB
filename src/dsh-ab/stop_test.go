package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The two routes Stop can take, in the exact wording RUNTIME_CONTRACT §3.9 fixes for the log.
// Every test here pins the line, because "which route ran" is the whole difference the user can
// see in the log; the reasons come from dsh.go verbatim.
const (
	stopLineStopped    = "已停止 dsh（PID %d）"
	stopLineForcedTree = "已强制结束 dsh（PID %d，整个进程树由作业对象结束，未等待 stop_grace_s）"
	stopLineForcedOne  = "已强制结束 dsh（PID %d，等待 stop_grace_s 后仍在运行，只结束了子进程）"
)

// treeChild is one real dsh child - this test binary, re-executed as the tree helper - plus the
// grandchild it starts. Both start through the real Start path, so the job assignment Start
// performs is the one Stop is tested against.
type treeChild struct {
	run   *Runner
	child int
	grand int
}

// startTreeChild builds a slot whose node.exe is the test binary and starts it through Start.
// The go-ahead file is written only after Start returned: the child is in the job by then, so the
// grandchild it starts next is born inside it.
func startTreeChild(t *testing.T, killTree bool) *treeChild {
	t.Helper()
	root := t.TempDir()
	slotRoot := filepath.Join(root, slotA)
	for _, dir := range []string{filepath.Join(slotRoot, "node"), filepath.Join(slotRoot, "app")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("建槽目录 %s：%v", dir, err)
		}
	}
	exe, err := helperBinary()
	if err != nil {
		t.Fatalf("找不到测试二进制：%v", err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("读取测试二进制：%v", err)
	}
	if err := os.WriteFile(filepath.Join(slotRoot, "node", "node.exe"), binary, 0o755); err != nil {
		t.Fatalf("写入替身 node：%v", err)
	}
	if err := os.WriteFile(filepath.Join(slotRoot, "app", "dsh.js"), []byte("// stand-in entry\n"), 0o644); err != nil {
		t.Fatalf("写入替身入口：%v", err)
	}

	trigger := filepath.Join(root, "go-ahead")
	pidFile := filepath.Join(root, "grandchild.pid")
	cfg := DefaultConfig()
	cfg.Launch.DshEntry = "app/dsh.js"
	cfg.Launch.KillTree = killTree
	cfg.Launch.StopGraceS = 2
	cfg.Launch.Env = map[string]string{
		testHelperEnv:     helperTree,
		testHelperTrigger: trigger,
		testHelperPIDFile: pidFile,
	}
	lg := NewLogger(t.TempDir(), LevelFull, 20, 5)
	t.Cleanup(lg.Close)
	run := NewRunner(root, cfg, lg)
	if err := run.Start(slotA, cfg.Ports.Production); err != nil {
		t.Fatalf("Start：%v", err)
	}
	t.Cleanup(run.Stop)
	c := &treeChild{run: run, child: run.cmd.Process.Pid}
	mustWrite(t, trigger, "go")
	c.grand = readPIDFile(t, pidFile)
	return c
}

// TestKillTreeEndsTheWholeTreeThroughTheJob is the D5 route itself: with kill_tree the child and
// its own child are ended by one TerminateJobObject call on the job the Runner already owns, so
// the log says that route ran and stop_grace_s was not waited out (it cannot be: nothing else can
// end the tree of a CREATE_NO_WINDOW console child).
func TestKillTreeEndsTheWholeTreeThroughTheJob(t *testing.T) {
	c := startTreeChild(t, true)
	// Both handles are opened before the stop: a terminated PID can be reused, a handle cannot.
	childH := openProcess(t, c.child, true)
	grandH := openProcess(t, c.grand, true)

	start := time.Now()
	log := writeAndReadLog(t, func(lg *Logger) { c.run.lg = lg; c.run.Stop() })
	waited := time.Since(start)

	if want := fmt.Sprintf(stopLineForcedTree, c.child); !strings.Contains(log, want) {
		t.Errorf("日志里没有作业对象路线的 INFO 行 %q：\n%s", want, log)
	}
	if strings.Contains(log, "已停止 dsh") {
		t.Errorf("走了强制路线，却还写了「已停止 dsh」：\n%s", log)
	}
	if waited >= 2*time.Second {
		t.Errorf("作业对象路线花了 %v：它不该等 stop_grace_s（配置为 2s）", waited)
	}
	if !waitGone(t, childH, 15*time.Second) {
		t.Error("dsh 子进程还活着")
	}
	if !waitGone(t, grandH, 15*time.Second) {
		t.Error("孙子进程还活着：作业对象没有带走整棵树")
	}
	if c.run.Slot() != "" {
		t.Errorf("Stop 之后 Slot() = %q，应为空", c.run.Slot())
	}
}

// TestKillTreeOffEndsOnlyTheDirectChild is the other operand: with kill_tree=false the direct
// child is ended and the grandchild is deliberately left alone, and the log says the grace period
// was waited out before the kill. Without this test the two routes would not be told apart.
func TestKillTreeOffEndsOnlyTheDirectChild(t *testing.T) {
	c := startTreeChild(t, false)
	childH := openProcess(t, c.child, true)
	grandH := openProcess(t, c.grand, true)

	start := time.Now()
	log := writeAndReadLog(t, func(lg *Logger) { c.run.lg = lg; c.run.Stop() })
	waited := time.Since(start)

	if want := fmt.Sprintf(stopLineForcedOne, c.child); !strings.Contains(log, want) {
		t.Errorf("日志里没有单进程路线的 INFO 行 %q：\n%s", want, log)
	}
	if strings.Contains(log, "整个进程树由作业对象结束") {
		t.Errorf("kill_tree=false 却报告走了作业对象路线：\n%s", log)
	}
	if waited < time.Second {
		t.Errorf("kill_tree=false 只花了 %v：它必须先等满 stop_grace_s（配置为 2s）", waited)
	}
	if !waitGone(t, childH, 15*time.Second) {
		t.Error("直接子进程还活着")
	}
	if waitGone(t, grandH, 2*time.Second) {
		t.Error("孙子进程也被结束了：kill_tree=false 只该结束直接子进程")
	}
	// This route leaves the grandchild to nobody on purpose, so the test ends it - no test may
	// leave a five-minute sleeper behind.
	if err := windows.TerminateProcess(grandH, 1); err != nil {
		t.Errorf("清理孙子进程：%v", err)
	}
}

// unownedRunner starts one stand-in child outside every job object and wires a Runner onto it the
// way Start would, minus the assignment: that is the shape Stop has to cope with when the job
// never took the child (job.go's owns()).
func unownedRunner(t *testing.T, cfg *Config, mode string) (*Runner, windows.Handle) {
	t.Helper()
	cmd := startHelper(t, mode)
	r := &Runner{cfg: cfg, tail: newLineTail(childTailLines)}
	exited := make(chan struct{})
	r.cmd, r.exited = cmd, exited
	go func() { _ = cmd.Wait(); close(exited) }()
	return r, openProcess(t, cmd.Process.Pid, true)
}

// TestStopWithoutTheJobTakesTheSingleProcessRoute: kill_tree=true alone is not the tree route.
// An empty job object accepts TerminateJobObject and ends nothing, so a Stop that trusted the flag
// would log a killed tree while dsh kept the production port - owns() is what stops that, and this
// is the test that fails if it is removed.
func TestStopWithoutTheJobTakesTheSingleProcessRoute(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Launch.KillTree = true
	cfg.Launch.StopGraceS = 2
	r, h := unownedRunner(t, cfg, helperSleep)
	pid := r.cmd.Process.Pid

	log := writeAndReadLog(t, func(lg *Logger) { r.lg = lg; r.Stop() })

	if want := fmt.Sprintf(stopLineForcedOne, pid); !strings.Contains(log, want) {
		t.Errorf("作业对象没接住子进程，日志却不是单进程路线 %q：\n%s", want, log)
	}
	if strings.Contains(log, "整个进程树由作业对象结束") {
		t.Errorf("空的作业对象被当成了已结束的整棵树：\n%s", log)
	}
	if !waitGone(t, h, 15*time.Second) {
		t.Error("子进程还活着")
	}
}

// TestStopReportsAChildThatLeftOnItsOwn pins the third INFO line: a child that is gone before
// stop_grace_s runs out was not forced, and the log must not claim it was.
func TestStopReportsAChildThatLeftOnItsOwn(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Launch.StopGraceS = 5
	r, h := unownedRunner(t, cfg, helperExit1s)
	pid := r.cmd.Process.Pid

	log := writeAndReadLog(t, func(lg *Logger) { r.lg = lg; r.Stop() })

	if want := fmt.Sprintf(stopLineStopped, pid); !strings.Contains(log, want) {
		t.Errorf("自行退出的子进程没有写出 %q：\n%s", want, log)
	}
	if strings.Contains(log, "已强制结束") {
		t.Errorf("子进程自己在宽限期内退了，日志却说被强制结束：\n%s", log)
	}
	if !waitGone(t, h, 5*time.Second) {
		t.Error("子进程还活着")
	}
}
