package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

// slotRootPath is the installation directory of one slot.
func slotRootPath(root, slot string) string { return filepath.Join(root, slot) }

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// slotInstalled reports whether a slot holds a complete dsh instance. It only
// reads: DSH-AB never parses or modifies anything inside dsh.
func slotInstalled(root string, cfg *Config, slot string) bool {
	if !validSlot(slot) {
		return false
	}
	sr := slotRootPath(root, slot)
	return fileExists(cfg.Abs(sr, cfg.Launch.NodeExe)) &&
		fileExists(cfg.Abs(sr, cfg.Launch.DshEntry))
}

// decideSlot answers the one question the launcher asks: which slot does this
// start run? apply is true for the tray's 重启 and for nothing else: the start the
// app performs when it opens reads the registration, keeps it and runs the active
// slot (user 2026-09-19). A registration whose target slot is missing is reported
// and kept, never silently dropped.
func decideSlot(root string, cfg *Config, st *State, apply bool) (slot string, consumed bool, warning string) {
	active := st.ActiveSlot()
	if st.Pending == nil || !apply {
		return active, false, ""
	}
	target := st.Pending.TargetSlot
	if !validSlot(target) {
		return active, false, fmt.Sprintf("槽位切换的目标 %q 不是有效槽位，本次仍启动活动槽 %s。", target, active)
	}
	if !slotInstalled(root, cfg, target) {
		return active, false, fmt.Sprintf("目标槽 %s 未安装 dsh，本次仍启动活动槽 %s；这条槽位切换继续保留。", target, active)
	}
	got, ok := st.ConsumePending()
	if !ok {
		return active, false, "登记写入失败，本次仍启动活动槽 " + active + "。"
	}
	return got, true, ""
}

// PortOpen is the liveness probe used before opening the browser.
func PortOpen(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitPortLive polls until the port accepts a connection, the child dies, or the
// budget runs out. A child that is gone cannot make the port ready later, so the
// wait ends there instead of burning the whole budget and reporting a timeout on
// top of the death (docs/DEFECTS.md, DEF-002: one fault, one popup).
func waitPortLive(host string, port int, timeout, interval time.Duration, alive func() bool) (ready, exited bool) {
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		if PortOpen(host, port, interval) {
			return true, false
		}
		if alive != nil && !alive() {
			return false, true
		}
		if time.Now().After(deadline) {
			return false, false
		}
		time.Sleep(interval)
	}
}

// childTailLines is how many lines of the child's output are held in memory at
// all times. The stream is always read, because dsh announces its authenticated
// page address there; keeping the tail means a startup failure still has
// something to show when logs.level discards child output (DEF-002).
const childTailLines = 40

// lineTail keeps the most recent lines written to it: a slice that drops its oldest
// entry once it is longer than keep. Every method tolerates a nil receiver, so a
// Runner built without one is simply a Runner with no tail.
type lineTail struct {
	mu   sync.Mutex
	keep int
	buf  []string
}

func newLineTail(keep int) *lineTail {
	if keep < 1 {
		keep = 1
	}
	return &lineTail{keep: keep}
}

func (t *lineTail) push(line string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, line)
	if len(t.buf) > t.keep {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.keep:]...)
	}
}

// lines returns a copy of the kept lines, oldest first, so a failure reads in real
// order and a reader can never race the writer.
func (t *lineTail) lines() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.buf...)
}

// webURLLine matches the page address dsh prints once its web runtime settles:
//
//	dsh web: http://127.0.0.1:3190/?token=<one-time launch token> (LAN: ...)
//
// Only an http(s) form matches, so the neighbouring "dsh web: opening the
// default browser" line can never be mistaken for an address.
var webURLLine = regexp.MustCompile(`dsh web:\s*(https?://\S+)`)

// parseWebURL returns the authenticated page URL printed on one line of dsh
// output, or "" when that line carries no address.
func parseWebURL(line string) string {
	m := webURLLine.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// Runner owns the single dsh child process of this installation.
type Runner struct {
	mu     sync.Mutex
	root   string
	cfg    *Config
	lg     *Logger
	cmd    *exec.Cmd
	exited chan struct{}
	slot   string

	// authURL is the token-carrying page address printed by the child that is
	// running right now, and urlReady closes as soon as it has been captured.
	// dsh mints the token per process, so both are reset on every start.
	authURL  string
	urlReady chan struct{}

	// tail is the output of the child the latest Start attempt launched, and
	// exitNote is why that child stopped, if it stopped with an error. Both exist
	// so a start that fails can explain itself (DEF-002).
	//
	// launched says whether the latest attempt got as far as creating a process.
	// An attempt the preflight check rejects has no output of its own, and its tail
	// is deliberately empty so it cannot be mistaken for one (DEF-009).
	tail     *lineTail
	exitNote string
	launched bool
}

func NewRunner(root string, cfg *Config, lg *Logger) *Runner {
	return &Runner{root: root, cfg: cfg, lg: lg, tail: newLineTail(childTailLines)}
}

// commandLine resolves the node executable and the dsh entry inside one slot.
func (r *Runner) commandLine(slotRoot string, port int) (string, []string, error) {
	exe := r.cfg.Abs(slotRoot, r.cfg.Launch.NodeExe)
	entry := r.cfg.Abs(slotRoot, r.cfg.Launch.DshEntry)
	if !fileExists(exe) {
		return "", nil, fmt.Errorf("槽内没有 node：%s", exe)
	}
	if !fileExists(entry) {
		return "", nil, fmt.Errorf("槽内没有 dsh 入口：%s", entry)
	}
	// --no-open: dsh must not open a browser itself. DSH-AB opens it, and only after the
	// port probe says the instance is really answering (PLAN section 6 item 1).
	args := []string{entry, "web", "--port", strconv.Itoa(port), "--no-open"}
	args = append(args, r.cfg.Launch.ExtraArgs...)
	return exe, args, nil
}

// childEnv is the parent environment plus the slot's DSH_HOME and the extra
// variables from the config. DSH_HOME is owned by the program and cannot be
// overridden, otherwise a slot would read another slot's data.
func (r *Runner) childEnv(slotRoot string) []string {
	env := make([]string, 0, len(os.Environ())+len(r.cfg.Launch.Env)+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "DSH_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "DSH_HOME="+filepath.Join(slotRoot, "data"))
	for k, v := range r.cfg.Launch.Env {
		if strings.EqualFold(k, "DSH_HOME") {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

// Start launches dsh for one slot without a console window. It returns as soon
// as the process exists; readiness is a separate question (see waitPortLive).
func (r *Runner) Start(slot string, port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return errors.New("dsh 已在运行")
	}
	// A new attempt starts from nothing: the lines still held belong to the child
	// that was just stopped, and reporting them as this start's output sent the
	// user chasing the wrong error (DEF-009).
	r.tail, r.exitNote, r.launched = newLineTail(childTailLines), "", false
	slotRoot := slotRootPath(r.root, slot)
	exe, args, err := r.commandLine(slotRoot, port)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = slotRoot
	cmd.Env = r.childEnv(slotRoot)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
	}
	// The child's output is always read, because the authenticated page address
	// is only ever announced there. The log level decides how much of it is kept.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	pw.Close() // the child holds the only remaining write handle

	// Publish this child before anything can observe it: the pump and the wait
	// goroutine both report into the fields set here. Start already holds r.mu for
	// its whole body, so this block must NOT take it again - sync.Mutex is not
	// reentrant, and a second Lock here hangs the launcher with dsh alive behind a
	// full stdout pipe (the pipe stops being drained, so dsh never serves).
	exited := make(chan struct{})
	tail := newLineTail(childTailLines)
	r.cmd, r.exited, r.slot = cmd, exited, slot
	r.authURL, r.urlReady = "", make(chan struct{})
	r.tail, r.exitNote, r.launched = tail, "", true

	go func() {
		if err := cmd.Wait(); err != nil {
			r.setExitNote(err)
		}
		close(exited)
	}()
	go r.pump(pr, cmd, tail, slot)
	if r.lg != nil {
		r.lg.Info("已启动 dsh：槽 %s，端口 %d，PID %d", slot, port, cmd.Process.Pid)
	}
	return nil
}

// pump keeps the child's output flowing into the log and watches it for the
// authenticated page address, which only this child can announce. The last lines
// are always kept in tail, whichever log level is active. slot is the slot this
// child was started from, taken as a parameter because the log outlives the run:
// after a slot switch both slots' output shares one file and every line has to
// name its own slot (the running slot field is cleared by Stop).
func (r *Runner) pump(pr *os.File, cmd *exec.Cmd, tail *lineTail, slot string) {
	defer pr.Close()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		tail.push(line)
		if r.lg != nil {
			r.lg.Child(slot, line)
		}
		if u := parseWebURL(line); u != "" {
			r.capture(cmd, u)
		}
	}
}

// LastStartOutput is the recent output of the child the latest Start attempt
// launched, plus whether that attempt launched a child at all. An attempt the
// preflight check rejected reports no lines — never the previous child's
// (DEF-009).
func (r *Runner) LastStartOutput() ([]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tail.lines(), r.launched
}

// ExitNote is why the running child stopped, e.g. "exit status 1", or "" when it
// has not stopped or stopped cleanly.
func (r *Runner) ExitNote() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exitNote
}

func (r *Runner) setExitNote(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exitNote == "" {
		r.exitNote = err.Error()
	}
}

// capture stores the address of one child. Output that arrives after that child
// was replaced or stopped is dropped, so a stale token can never be reused.
func (r *Runner) capture(cmd *exec.Cmd, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != cmd || r.authURL != "" {
		return
	}
	r.authURL = url
	if r.urlReady != nil {
		close(r.urlReady)
	}
	if r.lg != nil {
		r.lg.Info("已取得 dsh 认证地址：%s", url)
	}
}

// URL is the authenticated page address of the running child, or "" when the
// child has not announced one yet.
func (r *Runner) URL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authURL
}

// AwaitURL waits up to timeout for this child to announce its address. An
// already-announced address satisfies the wait at once: capture closes urlReady
// in the same critical section it stores the address in.
func (r *Runner) AwaitURL(timeout time.Duration) (string, bool) {
	r.mu.Lock()
	ready := r.urlReady
	r.mu.Unlock()
	if ready == nil {
		return "", false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
		return r.URL(), true
	case <-timer.C:
		return "", false
	}
}

// Running reports whether the child we started is still alive.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.aliveLocked()
}

// Slot is the slot the running child was started from.
func (r *Runner) Slot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.slot
}

func (r *Runner) aliveLocked() bool {
	if r.cmd == nil || r.exited == nil {
		return false
	}
	select {
	case <-r.exited:
		return false
	default:
		return true
	}
}

// Stop ends the child process and, when kill_tree is set, its whole tree.
// It tries a graceful stop first and only escalates after stop_grace_s.
func (r *Runner) Stop() {
	r.mu.Lock()
	cmd, exited := r.cmd, r.exited
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	alive := func() bool {
		select {
		case <-exited:
			return false
		default:
			return true
		}
	}
	if !alive() {
		r.clear()
		return
	}
	pid := cmd.Process.Pid
	grace := time.Duration(r.cfg.Launch.StopGraceS) * time.Second

	gracefulOK := true
	if r.cfg.Launch.KillTree {
		gracefulOK = r.taskkill(pid, false) == nil
	}
	if !gracefulOK {
		if r.lg != nil {
			r.lg.Debugf("优雅停止不可用，直接结束 PID %d", pid)
		}
		r.forceKill(cmd, pid)
	} else {
		deadline := time.Now().Add(grace)
		for alive() && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		if alive() {
			r.forceKill(cmd, pid)
		}
	}
	for i := 0; i < 50 && alive(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive() && r.lg != nil {
		r.lg.Errorf("PID %d 未能结束", pid)
	}
	if r.lg != nil {
		r.lg.Info("已停止 dsh（PID %d）", pid)
	}
	r.clear()
}

func (r *Runner) clear() {
	r.mu.Lock()
	r.cmd, r.exited, r.slot = nil, nil, ""
	r.authURL, r.urlReady = "", nil
	r.mu.Unlock()
}

func (r *Runner) forceKill(cmd *exec.Cmd, pid int) {
	if r.cfg.Launch.KillTree {
		if err := r.taskkill(pid, true); err != nil && r.lg != nil {
			r.lg.Warnf("taskkill /F 失败：%v", err)
		}
		return
	}
	if err := cmd.Process.Kill(); err != nil && r.lg != nil {
		r.lg.Warnf("结束进程失败：%v", err)
	}
}

func (r *Runner) taskkill(pid int, force bool) error {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return kill.Run()
}
