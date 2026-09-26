package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// dshVersionSearchDepth is how many parent directories above the dsh entry are
// searched for a package.json: the entry lives at
// app\node_modules\@deepseek-ai\dsh\lib\bin.js, so the version is two levels up
// and the third is slack for a differently packaged slot.
const dshVersionSearchDepth = 3

// dshVersionInSlot is the dsh version one slot really carries, or "" when there is
// nothing readable to report.
//
// This is the single read-only exception to "DSH-AB never parses anything inside
// dsh". The status popup used to print the dsh version baked into this exe by
// -ldflags, so a slot that was upgraded after the build kept being reported as the
// old one. Only the "version" field of the nearest package.json is read - the
// entry's own directory first, then up to three parents - and anything missing,
// unreadable or unparsable degrades to "". Nothing is written, nothing is cached,
// and no start path depends on the answer.
func dshVersionInSlot(root string, cfg *Config, slot string) string {
	if !validSlot(slot) {
		return ""
	}
	entry := cfg.Abs(slotRootPath(root, slot), cfg.Launch.DshEntry)
	if entry == "" {
		return ""
	}
	dir := filepath.Dir(entry)
	for i := 0; i <= dshVersionSearchDepth; i++ {
		var pkg struct {
			Version string `json:"version"`
		}
		if ok, err := readJSON(filepath.Join(dir, "package.json"), &pkg); err == nil && ok {
			if v := strings.TrimSpace(pkg.Version); v != "" {
				return v
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // 到盘符根了，再往上还是它自己
		}
		dir = parent
	}
	return ""
}

// slotVersionText renders that version for the user: the version the slot carries,
// or 未知 when nothing readable is there. The popup must never fall back to the
// build-time constant - that constant is the bug this reports.
func slotVersionText(v string) string {
	if strings.TrimSpace(v) == "" {
		return "未知"
	}
	return v
}

// sessionsDir is one slot's dsh session directory: a slot is that dsh's DSH_HOME
// (see Runner.childEnv), so the two slots keep their conversations apart.
func sessionsDir(root, slot string) string {
	return filepath.Join(slotRootPath(root, slot), "data", "sessions")
}

// sessionCount counts the files under one slot's session directory, 0 when it is
// not there. It only counts: the number behind "切换后看不到当前槽的对话" has to be a
// fact about the disk, not an assumption.
func sessionCount(root, slot string) int {
	if !validSlot(slot) {
		return 0
	}
	n := 0
	// A missing or unreadable tree counts as 0 - the walk stops on the first error.
	_ = filepath.WalkDir(sessionsDir(root, slot), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}
