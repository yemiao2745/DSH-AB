package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getlantern/systray"
)

// App wires the tray, the state files and the dsh child together.
type App struct {
	root string
	cfg  *Config
	// name is what this installation is called on screen: "DSH-AB" while it is
	// the only one on this machine, "DSH-AB (<port>)" when another one coexists, and
	// "DSH-AB (<tag>)" when its port is unusable or collides with another copy. main sets
	// it from alignInstallIdentity; a zero value falls back to the single-install rule.
	name string
	// nameNotice is the popup that name carries when it had to fall back to the tag:
	// which port is in the way and which file to fix. '' when there is nothing to say.
	// main sets it next to name, and onReady shows it off the startup path.
	nameNotice string
	// cfgReport is what loading dsh-ab.toml had to say: a whole file discarded, or
	// the per-item fallbacks. The one startup notice and the status popup read it,
	// so a config the user cannot see is no longer silently ignored.
	cfgReport ConfigReport
	st        *State
	run       *Runner
	lg        *Logger

	// opMu serializes start/stop so a restart click cannot race the launcher.
	opMu   sync.Mutex
	faults *throttle
	ready  atomic.Bool

	// popup is the one popup entry: every message this program shows goes through
	// it, and isErr picks the icon. NewApp sets the system MessageBox (with the owner
	// window that keeps it off the taskbar) and tests replace it, which is the only way to pin the "one fault,
	// one popup" contract without a desktop.
	popup func(title, text string, isErr bool)

	// tooltip is the sink that sets the tray icon's tooltip, and production puts
	// systray.SetTooltip here. Left nil (or replaced by a recorder) in a test:
	// systray's own state is a nil pointer until systray.Run, so calling into it
	// from a test panics.
	tooltip func(text string)

	// deferred holds the messages the operation running under opMu wants to show.
	// A modal dialog blocks the goroutine that shows it, so showing one while the
	// lock is held freezes every other tray action behind it (the recorded shape: "重启"
	// confirmed at 16:35:04 only ran at 16:38:30, when the dialog was dismissed).
	// The lock holder only ever queues; the same goroutine drains the queue right
	// after unlocking. Only that goroutine touches this field.
	deferred []deferredPopup

	openItem     *systray.MenuItem
	restartItem  *systray.MenuItem
	rollbackItem *systray.MenuItem
	statusItem   *systray.MenuItem
	logItem      *systray.MenuItem
	exitItem     *systray.MenuItem
}

func NewApp(root string, cfg *Config, st *State, run *Runner, lg *Logger, cfgReport ConfigReport) *App {
	return &App{
		root:      root,
		cfg:       cfg,
		cfgReport: cfgReport,
		st:        st,
		run:       run,
		lg:        lg,
		faults:    newThrottle(time.Duration(cfg.Health.PopupThrottleMin) * time.Minute),
		tooltip:   systray.SetTooltip,
		// The production sink is the one MessageBox mechanism, with the error icon for
		// messages that report a failure and the information icon for the rest (a
		// warning about a slot switch is not an error).
		popup: func(title, text string, isErr bool) {
			if isErr {
				alertErr(title, text)
				return
			}
			alert(title, text)
		},
	}
}

// onReady builds the six fixed menu items. A click on the icon opens the same
// menu for both mouse buttons.
func (a *App) onReady() {
	systray.SetIcon(a.iconBytes())
	a.refreshTooltip()

	a.addMenuItems(systray.AddMenuItem)

	a.refreshLabels()

	// The three workers start before anything else on this path: nothing that
	// follows may be a dialog the user has to dismiss first.
	go a.serve()
	go a.launch(true)
	go a.healthLoop()
	// onReady only asks for the config notice; the dialog waits on a goroutine of
	// its own, so nothing here can hold the entry point hostage.
	a.configNotice()
	// Same shape for the name: a degraded name is worth one popup, and that popup must
	// not be a dialog the start path has to wait for either.
	a.nameNoticeShow()
}

// addMenuItems registers the six fixed items through add, in the one fixed order
// (2026-09-19, "information first": what can be looked at, then what
// acts, then exit). Taking add as a parameter is what lets a test pin that order
// without a desktop.
func (a *App) addMenuItems(add func(title, tooltip string) *systray.MenuItem) {
	a.openItem = add(a.cfg.Tray.Labels["open"], "打开浏览器")
	a.statusItem = add(a.cfg.Tray.Labels["status"], "活动槽、端口、两槽状态")
	a.logItem = add(a.cfg.LogLabel(a.lg.Level()), "单击在 off / auto / full 之间循环")
	a.rollbackItem = add(a.cfg.Tray.Labels["rollback"], "槽位切换：只登记，不结束进程")
	a.restartItem = add(a.cfg.Tray.Labels["restart"], "停止并重新启动 dsh")
	a.exitItem = add(a.cfg.Tray.Labels["exit"], "停止 dsh 并退出")
}

// onExit runs inside the systray event loop just before the process ends.
func (a *App) onExit() {
	a.lg.Info("DSH-AB 退出")
	a.lg.Close()
}

func (a *App) serve() {
	for {
		select {
		case <-a.openItem.ClickedCh:
			go a.onOpenBrowser()
		case <-a.restartItem.ClickedCh:
			go a.onRestart()
		case <-a.rollbackItem.ClickedCh:
			go a.onRollback()
		case <-a.statusItem.ClickedCh:
			go a.onStatus()
		case <-a.logItem.ClickedCh:
			go a.onLog()
		case <-a.exitItem.ClickedCh:
			go a.onExitClicked()
		}
	}
}

// rollbackMenuTitle is the text of menu item 4 in each of its two states (user
// 2026-09-19, final wording): a registered switch reads "槽位切换：待生效" and
// everything else reads "槽位切换". The count and the order of the six items stay
// fixed by the menu contract. The status popup keeps its own lines, so the
// menu label is what changes with the state.
func rollbackMenuTitle(cfg *Config, phase string) string {
	if phase == phasePending {
		return "槽位切换：待生效"
	}
	return cfg.Tray.Labels["rollback"]
}

// rollbackConfirm is the one confirmation menu item 4 asks for, in each of its two
// states. Its title is that state's menu text and nothing else: the button carries
// only two texts, so its dialogs do too (user 2026-09-19, "都改成").
func rollbackConfirm(phase, target string) (title, text string) {
	if phase == phasePending {
		return "槽位切换：待生效", "撤销这条槽位切换？\n\n只做登记，不会结束进程；撤销后回到默认状态。"
	}
	return "槽位切换", fmt.Sprintf("槽位切换：切换到 %s？\n\n只做登记，不结束当前进程；点托盘「重启」后才生效。", target)
}

// refreshLabels reflects the current rollback phase and log level on the two
// menu items whose text is not fixed.
func (a *App) refreshLabels() {
	if a.rollbackItem == nil {
		return
	}
	a.rollbackItem.SetTitle(rollbackMenuTitle(a.cfg, a.st.RollbackPhase()))
	a.logItem.SetTitle(a.cfg.LogLabel(a.lg.Level()))
}

func (a *App) iconBytes() []byte {
	icon := strings.TrimSpace(a.cfg.Tray.Icon)
	if icon != "" && icon != "embedded" {
		path := a.cfg.Abs(a.root, icon)
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b
		}
		a.lg.Warnf("托盘图标 %s 不可用，改用内置图标", path)
	}
	return embeddedIcon
}

// launch is the single start path: it decides the slot, stops whatever runs,
// starts the chosen slot and waits for the port.
func (a *App) launch(initial bool) {
	a.opMu.Lock()
	a.launchLocked(initial)
	a.opMu.Unlock()
	// Never show a dialog under the lock: everything this start wants to
	// say is shown here, once the tray is free to act again.
	a.flushPopups()
}

func (a *App) launchLocked(initial bool) {
	// Only the tray's 重启 applies a waiting registration. initial marks the start
	// the app performs when it opens, and that one keeps it.
	slot, _, warning := decideSlot(a.root, a.cfg, a.st, !initial)
	a.refreshLabels()
	if warning != "" {
		a.lg.Warnf("%s", warning)
		a.notifyInfo("槽位切换", warning)
	}
	// One fault gets exactly one popup, so fault reporting belongs to exactly one
	// reporter: while a start is in flight the start path below owns it, and the
	// health loop only ever reports on a start that completed.
	a.ready.Store(false)
	a.run.Stop()

	port := a.cfg.Ports.Production
	// Whether the port already answered *before* this start is what turns a bare
	// "timeout" into something the user can act on (another DSH-AB - every installation
	// defaults to the same production port - or a dsh that never went away). The probe
	// decides the start itself: a taken port ends the attempt right here, with exactly one
	// popup, queued and shown after the lock.
	busyBefore := PortOpen(a.cfg.Ports.Host, port, portBusyProbe)
	if busyBefore {
		// 端口在启动前就被别人占着时，**不去启动 dsh**。起也起不来（绑不上端口
		// dsh 自己就退了），而那个端口上的应答是别人的——照旧往下走只会把别人的页面当成自己的
		// （日志写「端口 N 已就绪」、托盘显示运行中、浏览器开到别人的页面上；2026-09-22 真机实测过）。
		// 直接报一次失败，是谁占着交给用户自己查。
		a.reportStartFailure(startFailure{
			cause:      a.portFailureCause(slot, port, false, true),
			busyBefore: true,
		})
		return
	}
	if err := a.run.Start(slot, port); err != nil {
		// A start the preflight refused has no child and no port story.
		a.reportStartFailure(startFailure{cause: fmt.Sprintf("无法启动槽 %s：%v", slot, err)})
		return
	}

	timeout := time.Duration(a.cfg.Launch.StartTimeoutS) * time.Second
	interval := time.Duration(a.cfg.Launch.ProbeIntervalMs) * time.Millisecond
	deadline := time.Now().Add(timeout)
	ready, exited := waitPortLive(a.cfg.Ports.Host, port, timeout, interval, a.run.Running)
	if !ready {
		a.reportStartFailure(startFailure{
			cause:      a.portFailureCause(slot, port, exited, busyBefore),
			exited:     exited,
			busyBefore: busyBefore,
		})
		return
	}
	a.lg.Info("端口 %d 已就绪", port)

	if initial && a.cfg.Browser.OpenOnStart {
		if !a.openBrowser(budgetLeft(deadline)) && !a.run.Running() {
			// The child died between the port probe and the page address; that is
			// still one failed start and must stay one popup.
			a.reportStartFailure(startFailure{
				cause:      a.portFailureCause(slot, port, true, busyBefore),
				exited:     true,
				busyBefore: busyBefore,
			})
			return
		}
	}
	a.ready.Store(true)
	a.refreshTooltip()
}

// portBusyProbe is how long the launcher waits before calling a port "already
// taken" when it looks at the port before starting anything.
const portBusyProbe = 300 * time.Millisecond

// portFailureCause describes a start that ended before the page was served: the
// child's own exit reason when it is gone, the unspent budget when it is alive.
// busyBefore says the port was already answering before this start began - the
// one port failure that explains itself. It changes the wording only.
func (a *App) portFailureCause(slot string, port int, exited, busyBefore bool) string {
	if busyBefore {
		return fmt.Sprintf("端口 %d 在启动前就已经被占用——很可能是另一份 DSH-AB（每份安装默认都是同一个生产端口 %d），或上次没退干净的 dsh 进程。", port, defaultProductionPort)
	}
	if !exited {
		return fmt.Sprintf("dsh 进程还活着，但 %d 秒内端口 %d 没有就绪（槽 %s）。",
			a.cfg.Launch.StartTimeoutS, port, slot)
	}
	if note := a.run.ExitNote(); note != "" {
		return fmt.Sprintf("dsh 进程在端口 %d 就绪前就退出了（槽 %s，%s）。", port, slot, note)
	}
	return fmt.Sprintf("dsh 进程在端口 %d 就绪前就退出了（槽 %s）。", port, slot)
}

// startFailure is everything one failed start has to say, and startupFailureText
// renders it. It is a struct rather than a parameter list because one popup needs
// four independent facts about the attempt - was there a child, did it die, was
// the port already taken, which copy is talking - and that many booleans in a
// signature is a bug factory.
type startFailure struct {
	// cause is the first paragraph: portFailureCause, or Start's own refusal.
	cause string
	// tail and launched belong to the child of *this* attempt.
	tail     []string
	launched bool
	// exited says that child is gone; with launched it is still alive.
	exited bool
	// busyBefore: the port already answered before this start began.
	busyBefore bool
	// root and logNote come from the App that reports; maxLines caps the tail.
	root     string
	logNote  string
	maxLines int
}

// startupFailureMaxLines is how much of the child's output the popup shows. The
// whole tail is always in the log; a dialog is not a log viewer.
const startupFailureMaxLines = 12

// reportStartFailure is the only reporter of a start that never reached a
// serving page. It always records the child's own output — so the log pointer it
// shows is never a lie — and it never sets ready, which keeps the
// health loop from describing the same failure a second time.
//
// This popup is unconditional — health.popup_on_process_dead does not
// silence a start that failed — but it stays inside the fault throttle, so a
// start that keeps failing does not keep covering the desktop.
func (a *App) reportStartFailure(f startFailure) {
	tail, launched := a.run.LastStartOutput()
	f.tail, f.launched = tail, launched
	f.root, f.maxLines = a.root, startupFailureMaxLines
	f.logNote = a.logNote()
	a.lg.Errorf("启动失败：%s", f.cause)
	switch {
	case !launched:
		// The preflight refused before any process existed; whatever is still in
		// the tail belongs to the child the restart just stopped.
		a.lg.Errorf("本次未启动 dsh 子进程")
	case len(tail) == 0:
		a.lg.Errorf("dsh 子进程没有产生任何输出")
	default:
		a.lg.Errorf("dsh 子进程输出（最后 %d 行）：", len(tail))
		for _, line := range tail {
			a.lg.Errorf("  %s", line)
		}
	}
	// Whatever the throttle decides about the dialog, the tray icon must stop
	// claiming dsh runs.
	a.refreshTooltip()
	if !a.faults.Allow(faultStartFailed) {
		a.lg.Warnf("同类故障 %d 分钟内已提示过，本次只记日志", a.cfg.Health.PopupThrottleMin)
		return
	}
	a.notifyErr("dsh 启动失败", startupFailureText(f))
}

// nextStep is the closing line of that popup: what the user can do right now
// It follows what the attempt really did, and a port that was already taken
// before the start wins over both process cases - no restart can free it, only
// another port can.
func nextStep(f startFailure) string {
	switch {
	case f.busyBefore:
		return fmt.Sprintf("改 %s 的 ports.production 后重启 DSH_AB.exe。", filepath.Join(f.root, "dsh-ab.toml"))
	case !f.launched:
		return "先看上面的原因；也可以在托盘「槽位切换」登记切回另一个槽再点「重启」。"
	case f.exited:
		return "先看上面最后几行；也可以在托盘「槽位切换」登记切回另一个槽再点「重启」。"
	default:
		return "可以点托盘「重启」重试；反复不通请看 ports.production 是否被别的程序占用。"
	}
}

// tooltipText is the tray tooltip: what dsh is doing right now, and on which port
// — shown even when this is the only installation. The port is what tells two DSH-AB icons apart
// at a glance, so the port is always there and the installation name never is - the
// name is already in 应用和功能 and on the shortcut, and the tray icon has no room
// for a third fact. The tray.tooltip config key that used to prefix this is gone.
func tooltipText(running bool, port int) string {
	if running {
		return fmt.Sprintf("DSH 运行中（端口 %d）", port)
	}
	return fmt.Sprintf("DSH 未运行（端口 %d）", port)
}

// refreshTooltip keeps the tooltip true. SetTooltip is repeatable, so it runs
// again on every event that changes the answer: this start, a start that failed,
// the exit click, and a health check that finds the process gone. An App without
// a sink (a test) simply has no tray icon to keep true.
func (a *App) refreshTooltip() {
	if a.tooltip == nil {
		return
	}
	a.tooltip(tooltipText(a.run.Running(), a.cfg.Ports.Production))
}

// logNote is the closing log line of every popup that points at the log file, and
// it is the truth about this run's logging: a log directory that cannot be
// written (no directory, no permission, a full disk) says exactly that instead of
// claiming a setting the user never chose. That check comes first, because off is
// a level that still writes errors, so a write that never
// reached the disk is the more important fact even there. Either way the user is
// told where the file was supposed to be.
func (a *App) logNote() string {
	if err := a.lg.WriteError(); err != nil {
		return fmt.Sprintf("日志目录不可写：%v\n日志路径：%s", err, a.lg.Path())
	}
	if a.lg.Level() == LevelOff {
		return "日志级别为 off：出错才写。\n日志路径：" + a.lg.Path()
	}
	return "完整日志：" + a.lg.Path()
}

// deferredPopup is one message an operation collected while it held opMu.
type deferredPopup struct {
	title, text string
	isErr       bool
}

// notifyInfo and notifyErr are the only popup calls allowed under opMu: they
// record the message instead of showing it.
func (a *App) notifyInfo(title, text string) {
	a.deferred = append(a.deferred, deferredPopup{title: title, text: text})
}

func (a *App) notifyErr(title, text string) {
	a.deferred = append(a.deferred, deferredPopup{title: title, text: text, isErr: true})
}

// flushPopups shows everything the finished operation collected, in order. It
// must be called by the goroutine that held opMu, immediately after unlocking:
// only then can the user dismiss one dialog without freezing the tray.
func (a *App) flushPopups() {
	deferred := a.deferred
	a.deferred = nil
	for _, p := range deferred {
		a.popup(p.title, p.text, p.isErr)
	}
}

// startupFailureText renders that one popup: what failed, the child's own last
// words, where the whole log lives, and what to do next. f.launched says whether
// this attempt ever created a process; without one there are no last words to
// show, and the old child's must not be passed off as them.
func startupFailureText(f startFailure) string {
	var b strings.Builder
	b.WriteString(f.cause)
	b.WriteString("\n\n")
	switch {
	case !f.launched:
		b.WriteString("本次没有启动 dsh 子进程，原因来自启动前的检查。")
	case len(f.tail) == 0:
		b.WriteString("dsh 子进程没有产生任何输出。")
	default:
		shown, dropped := f.tail, 0
		if f.maxLines > 0 && len(shown) > f.maxLines {
			dropped = len(shown) - f.maxLines
			shown = shown[dropped:]
		}
		if dropped > 0 {
			fmt.Fprintf(&b, "dsh 子进程输出（共 %d 行，只显示最后 %d 行）：\n", len(f.tail), len(shown))
		} else {
			fmt.Fprintf(&b, "dsh 子进程输出（最后 %d 行）：\n", len(shown))
		}
		for _, line := range shown {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	b.WriteString("\n")
	b.WriteString(f.logNote)
	if root := strings.TrimSpace(f.root); root != "" {
		b.WriteString("\n安装根：" + root)
	}
	b.WriteString("\n下一步：")
	b.WriteString(nextStep(f))
	return b.String()
}

// budgetLeft is the unspent part of a startup budget, with a floor so a budget
// spent exactly at its deadline still leaves the child a moment to announce.
func budgetLeft(deadline time.Time) time.Duration {
	if d := time.Until(deadline); d > time.Second {
		return d
	}
	return time.Second
}

func (a *App) onOpenBrowser() {
	a.opMu.Lock()
	a.openBrowserLocked()
	a.opMu.Unlock()
	a.flushPopups()
}

// openBrowserLocked is the whole preflight, under opMu like every other start
// path. It only records what it wants to tell the user; onOpenBrowser shows it
// after releasing the lock.
func (a *App) openBrowserLocked() {
	port := a.cfg.Ports.Production
	if !a.run.Running() {
		a.notifyInfo("dsh 未运行", "当前没有 dsh 进程，未打开浏览器。\n可以从托盘菜单重启。")
		return
	}
	if !PortOpen(a.cfg.Ports.Host, port, 800*time.Millisecond) {
		a.notifyInfo("端口未就绪", fmt.Sprintf("端口 %d 无响应，未打开浏览器。", port))
		return
	}
	a.openBrowser(3 * time.Second)
}

func (a *App) onRestart() {
	if a.cfg.Rollback.ConfirmRestart && !confirm("重启 dsh", "确定要重启 dsh 吗？\n\n已经登记但未生效的槽位切换会在这次重启后生效。") {
		return
	}
	a.lg.Info("用户请求重启")
	a.launch(false)
}

// onRollback only ever writes or clears a registration. It never stops a process.
// The switch has two states: the default one registers, the waiting one takes it
// back, and taking it back lands straight in the default state (user 2026-09-19).
func (a *App) onRollback() {
	switch a.st.RollbackPhase() {
	case phasePending:
		if title, text := rollbackConfirm(phasePending, ""); a.cfg.Rollback.Confirm && !confirm(title, text) {
			return
		}
		if err := a.st.Undo(); err != nil {
			a.lg.Errorf("撤销失败：%v", err)
			alertErr("撤销失败", err.Error())
			return
		}
		a.lg.Info("已撤销登记")
	default:
		target := otherSlot(a.st.ActiveSlot())
		if a.cfg.Rollback.RequireOtherSlot && !slotInstalled(a.root, a.cfg, target) {
			a.lg.Warnf("切换被拒绝：%s 未安装 dsh", target)
			alertErr("无法切换槽位", fmt.Sprintf("槽 %s 未安装 dsh，没有登记任何槽位切换。\n\n请先让 dsh 把活动槽复制到 %s。", target, target))
			return
		}
		if title, text := rollbackConfirm(phaseNone, target); a.cfg.Rollback.Confirm && !confirm(title, text) {
			return
		}
		if err := a.st.Register(target); err != nil {
			a.lg.Errorf("登记失败：%v", err)
			alertErr("登记失败", err.Error())
			return
		}
		a.lg.Info("已登记切换到 %s", target)
	}
	a.refreshLabels()
}

// displayName is the name this installation shows: what alignInstallIdentity
// computed at startup, or the single-install rule when there is no such value (a test).
func (a *App) displayName() string {
	if a.name != "" {
		return a.name
	}
	name, _ := installName(a.root, a.cfg.Ports.Production, true, nil)
	return name
}

// onStatus opens the popup of menu item 2. Its title names the installation first
// (two copies running side by side have popups that look alike), then the item's own
// label, so the menu and the popup still read the same words.
func (a *App) onStatus() {
	a.popup(a.displayName()+" · "+a.cfg.Tray.Labels["status"], a.statusText(), false)
}

// statusText renders the status popup: what is active, what runs, what is
// registered, and whether this run really uses what the config file says - the
// last line is how a user notices a config that was discarded or repaired without
// opening the log. It is pure so those lines can be pinned without a desktop.
func (a *App) statusText() string {
	active := a.st.ActiveSlot()
	var b strings.Builder
	fmt.Fprintf(&b, "活动槽：%s\n生产端口：%d\n\n", active, a.cfg.Ports.Production)
	for _, slot := range []string{slotA, slotB} {
		if !slotInstalled(a.root, a.cfg, slot) {
			fmt.Fprintf(&b, "%s：未安装\n", slot)
			continue
		}
		state := "已安装，未运行"
		if slot == a.run.Slot() && a.run.Running() {
			state = "已安装，运行中"
		}
		fmt.Fprintf(&b, "%s：%s\n", slot, state)
	}
	if a.st.RollbackPhase() == phasePending {
		fmt.Fprintf(&b, "\n槽位切换：待生效 → %s", a.st.Pending.TargetSlot)
	} else {
		b.WriteString("\n槽位切换：未登记")
	}
	fmt.Fprintf(&b, "\n日志级别：%s", a.lg.Level())
	// Two installations run side by side and their popups look alike, so the
	// popup says which copy is talking - the root, the build, and where its log is.
	fmt.Fprintf(&b, "\n安装根：%s", a.root)
	fmt.Fprintf(&b, "\n版本：%s", versionText(effectiveDshabVersion(), dshVersion))
	fmt.Fprintf(&b, "\n日志路径：%s", a.lg.Path())
	fmt.Fprintf(&b, "\n%s", a.cfgReport.StatusLine())
	return b.String()
}

// onLog cycles off / auto / full. It must never show a popup.
func (a *App) onLog() {
	a.lg.SetLevel(a.lg.NextLevel())
	// auto 档也要看得见每次改档，所以这一行是 debug 不是 info。
	a.lg.Debugf("日志级别切换为 %s", a.lg.Level())
	if a.logItem != nil {
		a.logItem.SetTitle(a.cfg.LogLabel(a.lg.Level()))
	}
}

func (a *App) onExitClicked() {
	if a.cfg.Rollback.ConfirmExit && !confirm("退出 DSH-AB", "退出会同时停止 dsh。\n\n确定退出吗？") {
		return
	}
	a.opMu.Lock()
	a.run.Stop()
	a.opMu.Unlock()
	a.refreshTooltip()
	systray.Quit()
}

// healthLoop reports faults and never restarts anything: automatic restarts are
// forbidden by the requirement, so a fault is only ever shown to the user.
func (a *App) healthLoop() {
	ticker := time.NewTicker(time.Duration(a.cfg.Health.PollIntervalS) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if a.ready.Load() {
			port := a.cfg.Ports.Production
			running := a.run.Running()
			portOpen := running && PortOpen(a.cfg.Ports.Host, port, 800*time.Millisecond)
			switch healthFault(running, portOpen, a.cfg.Health.PopupOnProcessDead, a.cfg.Health.PopupOnPortDown) {
			case faultProcessDead:
				// The tooltip is the only place on screen that still claims dsh
				// runs, so it is refreshed even when the popup is throttled.
				a.refreshTooltip()
				if a.faults.Allow(faultProcessDead) {
					a.lg.Warnf("健康检查：dsh 进程已退出")
					alert("dsh 已退出", "dsh 进程已经不在了。\n可以从托盘菜单重启。")
				}
			case faultPortDown:
				if a.faults.Allow(faultPortDown) {
					a.lg.Warnf("健康检查：端口 %d 无响应", port)
					alert("端口无响应", fmt.Sprintf("dsh 进程还在，但端口 %d 无响应。", port))
				}
			}
		}
	}
}

// A health sample names at most one fault, so one failure can never turn into
// two popups.
const (
	faultProcessDead = "process_dead"
	faultPortDown    = "port_down"
	faultStartFailed = "start_failed"
)

// healthFault names the single fault this sample carries, or "" when healthy. A
// dead process is never reported as a port fault as well, and a fault the user
// switched off is not a fault.
func healthFault(running, portOpen, popupOnProcessDead, popupOnPortDown bool) string {
	if !running {
		if popupOnProcessDead {
			return faultProcessDead
		}
		return ""
	}
	if !portOpen && popupOnPortDown {
		return faultPortDown
	}
	return ""
}

// configNotice asks for this run's one notice about the config file. It never shows a
// dialog on the caller's goroutine: onReady has to walk straight on to the start path
// whether or not the user ever dismisses the notice: the notice must not block startup.
func (a *App) configNotice() {
	go a.showConfigNotice()
}

// nameNoticeShow asks for this run's one notice about a name that fell back to the tag.
// Same rule as configNotice: the dialog runs on a goroutine of its own.
func (a *App) nameNoticeShow() {
	if a.nameNotice == "" {
		return
	}
	go a.popup(a.displayName()+" · 端口需要修改", a.nameNotice, false)
}

// showConfigNotice shows that notice, if there is one. It runs alone, so a modal
// dialog here holds nothing but itself.
func (a *App) showConfigNotice() {
	title, text, ok := a.cfgReport.Popup(a.logNote())
	if !ok {
		return
	}
	// No WARN here: main.go already logged this run's config state - one line with
	// the parse error for a discarded file, one per item for the fallbacks. Logging
	// the bare title again put two WARNs in the log for the same fact.
	a.popup(title, text, false)
}

// authenticatedURL is the only address this program hands to a browser: the
// token-carrying URL dsh announces for the child that is running right now.
// A bare http://host:port/ only ever answers "authentication required", so
// there is deliberately no fallback to one.
func (a *App) authenticatedURL(wait time.Duration) (string, error) {
	raw := a.run.URL()
	if raw == "" {
		var ok bool
		if raw, ok = a.run.AwaitURL(wait); !ok {
			return "", fmt.Errorf("没有读到 dsh 打印的认证地址。\n直接打开 http://%s:%d/ 只会得到 401 认证页，因此没有打开浏览器。\n槽 %s 未启动完成或输出被截断时会出现这种情况，请重试或查看日志。",
				a.cfg.Ports.Host, a.cfg.Ports.Production, a.run.Slot())
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("dsh 打印的认证地址无法解析：%v", err)
	}
	// The address dsh announces is the only one that works. dsh exchanges
	// its one-time token for a cookie on "/" alone (dsh-client-connection), so any
	// other path the caller might splice in lands on the 401 page.
	return u.String(), nil
}

// openBrowser reports whether the page reached the browser; every failure is
// reported here, once.
func (a *App) openBrowser(wait time.Duration) bool {
	link, err := a.authenticatedURL(wait)
	if err != nil {
		a.lg.Errorf("未打开浏览器：%v", err)
		a.notifyErr("未打开浏览器", err.Error()+"\n\n日志："+a.lg.Path())
		return false
	}
	var file, params string
	if exe := strings.TrimSpace(a.cfg.Browser.BrowserExe); exe != "" {
		file = a.cfg.Abs(a.root, exe)
		params = browserParams(link, a.cfg.Browser.ExtraArgs)
	} else {
		file = link
		params = browserParams("", a.cfg.Browser.ExtraArgs)
	}
	if err := shellOpen(file, params); err != nil {
		a.lg.Errorf("打开浏览器失败：%v", err)
		a.notifyErr("打开浏览器失败", err.Error()+"\n\n"+link)
		return false
	}
	a.lg.Info("已打开浏览器：%s", link)
	return true
}

// browserParams renders the argument tail the shell hands to the browser:
// browser.extra_args first, then link (empty when the link itself is the file, i.e.
// the default-browser case). syscall.EscapeArg is the documented Windows quoting rule
// (CommandLineToArgvW); an empty entry names no argument at all, so it is left out.
func browserParams(link string, extra []string) string {
	args := make([]string, 0, len(extra)+1)
	for _, e := range extra {
		if e != "" {
			args = append(args, syscall.EscapeArg(e))
		}
	}
	if link != "" {
		args = append(args, syscall.EscapeArg(link))
	}
	return strings.Join(args, " ")
}
