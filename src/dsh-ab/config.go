package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config mirrors dsh-ab.toml (docs/PLAN.md section 4). Every field drives one
// real behaviour; there are deliberately no decorative knobs.
type Config struct {
	Ports    PortsConfig    `toml:"ports"`
	Browser  BrowserConfig  `toml:"browser"`
	Launch   LaunchConfig   `toml:"launch"`
	Tray     TrayConfig     `toml:"tray"`
	Logs     LogsConfig     `toml:"logs"`
	Health   HealthConfig   `toml:"health"`
	Rollback RollbackConfig `toml:"rollback"`
}

type PortsConfig struct {
	Production int    `toml:"production"`
	Host       string `toml:"host"`
}

type BrowserConfig struct {
	OpenOnStart bool     `toml:"open_on_start"`
	BrowserExe  string   `toml:"browser_exe"`
	ExtraArgs   []string `toml:"extra_args"`
}

type LaunchConfig struct {
	NodeExe         string            `toml:"node_exe"`
	DshEntry        string            `toml:"dsh_entry"`
	ExtraArgs       []string          `toml:"extra_args"`
	Env             map[string]string `toml:"env"`
	StartTimeoutS   int               `toml:"start_timeout_s"`
	ProbeIntervalMs int               `toml:"probe_interval_ms"`
	StopGraceS      int               `toml:"stop_grace_s"`
	KillTree        bool              `toml:"kill_tree"`
}

type TrayConfig struct {
	Icon   string            `toml:"icon"`
	Labels map[string]string `toml:"labels"`
}

type LogsConfig struct {
	Level     string `toml:"level"`
	Dir       string `toml:"dir"`
	MaxFiles  int    `toml:"max_files"`
	MaxSizeMB int    `toml:"max_size_mb"`
}

type HealthConfig struct {
	PollIntervalS      int  `toml:"poll_interval_s"`
	PopupThrottleMin   int  `toml:"popup_throttle_min"`
	PopupOnProcessDead bool `toml:"popup_on_process_dead"`
	PopupOnPortDown    bool `toml:"popup_on_port_down"`
}

type RollbackConfig struct {
	Confirm          bool `toml:"confirm"`
	RequireOtherSlot bool `toml:"require_other_slot"`
	ConfirmRestart   bool `toml:"confirm_restart"`
	ConfirmExit      bool `toml:"confirm_exit"`
}

// defaultLabels are the six fixed tray items, in their fixed order.
func defaultLabels() map[string]string {
	return map[string]string{
		"open":     "打开浏览器",
		"status":   "当前状态",
		"log":      "log：{level}",
		"rollback": "槽位切换",
		"restart":  "重启",
		"exit":     "退出",
	}
}

// The default production port. DefaultConfig is the only place the number is written down;
// Validate's repair values and messages are derived from there, and config_test.go pins it.
// 只有这一个端口：第二个槽是 AI 在它自己挑的空闲端口上验证的，DSH-AB 从不占用那个端口。
const defaultProductionPort = 3090

// DefaultConfig returns the documented defaults; it is also the fallback when
// the config file is missing or unreadable.
func DefaultConfig() *Config {
	return &Config{
		Ports:   PortsConfig{Production: defaultProductionPort, Host: "127.0.0.1"},
		Browser: BrowserConfig{OpenOnStart: true},
		Launch: LaunchConfig{
			NodeExe:         "node/node.exe",
			DshEntry:        "app/node_modules/@deepseek-ai/dsh/lib/bin.js",
			StartTimeoutS:   60,
			ProbeIntervalMs: 500,
			StopGraceS:      10,
			KillTree:        true,
		},
		Tray: TrayConfig{Icon: "embedded", Labels: defaultLabels()},
		Logs: LogsConfig{Level: LevelAuto, Dir: "logs", MaxFiles: 20, MaxSizeMB: 5},
		Health: HealthConfig{
			PollIntervalS:      30,
			PopupThrottleMin:   5,
			PopupOnProcessDead: true,
			PopupOnPortDown:    true,
		},
		Rollback: RollbackConfig{Confirm: true, RequireOtherSlot: true, ConfirmRestart: true, ConfirmExit: true},
	}
}

// ConfigReport is everything reading dsh-ab.toml has to say beyond the values
// themselves. It exists because a discarded file used to hide in one log line the
// user never opens: the failure and the per-item fallbacks are now carried out of
// LoadConfig, so the tray can say what is really in force (docs/DEFECTS.md, O1).
type ConfigReport struct {
	// FileErr is set when the file exists but none of it could be used - a parse
	// error or an unreadable file. Every value in force is then the default.
	FileErr error
	// Repairs lists the per-item fallbacks Validate made, one line each, in the
	// order it made them ("xx 无效，已回退为 yy").
	Repairs []string
}

// StatusLine is the one line the status popup adds about the config file, so the
// user can see whether this run uses what the file says without opening a log.
func (r ConfigReport) StatusLine() string {
	switch {
	case r.FileErr != nil:
		return "配置：读取失败，使用默认值"
	case len(r.Repairs) > 0:
		return fmt.Sprintf("配置：%d 项已回退", len(r.Repairs))
	}
	return "配置：正常"
}

// Popup renders the one notice this run owes the user about the config file, or
// ok=false when the file loaded cleanly. logNote is where the details are - the
// caller's ready-made line ("完整日志：<path>", what the level keeps when it keeps
// less, or why the file could not be written; the logs.dir the failed file named
// went down with it). It is appended as-is: the caller
// already labels the log, so a "日志：" label here rendered the line as
// "日志：完整日志：<path>" (docs/DEFECTS.md, O1-1).
func (r ConfigReport) Popup(logNote string) (title, text string, ok bool) {
	switch {
	case r.FileErr != nil:
		return "配置读取失败", fmt.Sprintf("配置文件读取失败，本次使用全部默认值。\n\n%v\n\n%s", r.FileErr, logNote), true
	case len(r.Repairs) > 0:
		var b strings.Builder
		fmt.Fprintf(&b, "配置文件里有 %d 项无效，已回退为默认值：\n", len(r.Repairs))
		for _, w := range r.Repairs {
			fmt.Fprintf(&b, "  %s\n", w)
		}
		fmt.Fprintf(&b, "\n%s", logNote)
		return "配置有无效项", b.String(), true
	}
	return "", "", false
}

// LoadConfig overlays the file onto the defaults, so a partial file is valid and
// an absent file is not an error. Changing the file takes effect on restart. The
// report says whether that overlay really happened.
func LoadConfig(path string) (*Config, ConfigReport) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), ConfigReport{}
		}
		return DefaultConfig(), ConfigReport{FileErr: err}
	}
	cfg := DefaultConfig()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		// Decoding stops wherever the error is, so cfg can hold an arbitrary half of
		// the file. Start over from the defaults: the popup and the status line
		// promise "全部默认值", and that promise has to be true.
		return DefaultConfig(), ConfigReport{FileErr: err}
	}
	return cfg, ConfigReport{Repairs: cfg.Validate()}
}

// Validate repairs every out-of-range value back to its documented default and
// returns one message per repair. It never leaves a zero value in place. The
// defaults all come from DefaultConfig, so the value written back and the number
// the message names are one source and cannot drift apart.
func (c *Config) Validate() []string {
	var w []string
	def := DefaultConfig()
	fix := func(cond bool, msg string, apply func()) {
		if cond {
			apply()
			w = append(w, msg)
		}
	}

	fix(c.Ports.Production < 1 || c.Ports.Production > 65535,
		fmt.Sprintf("ports.production 无效，已回退为 %d", def.Ports.Production),
		func() { c.Ports.Production = def.Ports.Production })
	fix(strings.TrimSpace(c.Ports.Host) == "",
		fmt.Sprintf("ports.host 为空，已回退为 %s", def.Ports.Host),
		func() { c.Ports.Host = def.Ports.Host })

	fix(strings.TrimSpace(c.Launch.NodeExe) == "",
		"launch.node_exe 为空，已回退为默认值",
		func() { c.Launch.NodeExe = def.Launch.NodeExe })
	fix(strings.TrimSpace(c.Launch.DshEntry) == "",
		"launch.dsh_entry 为空，已回退为默认值",
		func() { c.Launch.DshEntry = def.Launch.DshEntry })
	fix(c.Launch.StartTimeoutS < 1,
		fmt.Sprintf("launch.start_timeout_s 无效，已回退为 %d", def.Launch.StartTimeoutS),
		func() { c.Launch.StartTimeoutS = def.Launch.StartTimeoutS })
	fix(c.Launch.ProbeIntervalMs < 50,
		fmt.Sprintf("launch.probe_interval_ms 无效，已回退为 %d", def.Launch.ProbeIntervalMs),
		func() { c.Launch.ProbeIntervalMs = def.Launch.ProbeIntervalMs })
	fix(c.Launch.StopGraceS < 0,
		fmt.Sprintf("launch.stop_grace_s 无效，已回退为 %d", def.Launch.StopGraceS),
		func() { c.Launch.StopGraceS = def.Launch.StopGraceS })

	fix(strings.TrimSpace(c.Tray.Icon) == "",
		fmt.Sprintf("tray.icon 为空，已回退为 %s", def.Tray.Icon),
		func() { c.Tray.Icon = def.Tray.Icon })
	if c.Tray.Labels == nil {
		c.Tray.Labels = map[string]string{}
	}
	for k, v := range def.Tray.Labels {
		if strings.TrimSpace(c.Tray.Labels[k]) == "" {
			c.Tray.Labels[k] = v
		}
	}

	fix(!validLogLevel(c.Logs.Level),
		fmt.Sprintf("logs.level 无效，已回退为 %s", def.Logs.Level),
		func() { c.Logs.Level = def.Logs.Level })
	fix(strings.TrimSpace(c.Logs.Dir) == "",
		fmt.Sprintf("logs.dir 为空，已回退为 %s", def.Logs.Dir),
		func() { c.Logs.Dir = def.Logs.Dir })
	fix(c.Logs.MaxFiles < 1,
		fmt.Sprintf("logs.max_files 无效，已回退为 %d", def.Logs.MaxFiles),
		func() { c.Logs.MaxFiles = def.Logs.MaxFiles })
	fix(c.Logs.MaxSizeMB < 1,
		fmt.Sprintf("logs.max_size_mb 无效，已回退为 %d", def.Logs.MaxSizeMB),
		func() { c.Logs.MaxSizeMB = def.Logs.MaxSizeMB })

	fix(c.Health.PollIntervalS < 1,
		fmt.Sprintf("health.poll_interval_s 无效，已回退为 %d", def.Health.PollIntervalS),
		func() { c.Health.PollIntervalS = def.Health.PollIntervalS })
	fix(c.Health.PopupThrottleMin < 0,
		fmt.Sprintf("health.popup_throttle_min 无效，已回退为 %d", def.Health.PopupThrottleMin),
		func() { c.Health.PopupThrottleMin = def.Health.PopupThrottleMin })

	return w
}

// Abs resolves a config path against a base directory: the installation root, or
// one slot's root for launch.node_exe / launch.dsh_entry. A relative value is joined
// to that base; an absolute one is used exactly as written (docs/DEFECTS.md, O2), so
// a path like C:\node\node.exe is no longer spliced behind the slot root.
func (c *Config) Abs(root, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// LogLabel renders the templated label of menu item 3.
func (c *Config) LogLabel(level string) string {
	return strings.ReplaceAll(c.Tray.Labels["log"], "{level}", level)
}
