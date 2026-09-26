package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// writeSlotDshPackage plants the package.json a real slot carries, so the version
// read out of it can be checked without a dsh installation.
func writeSlotDshPackage(t *testing.T, root, slot, version string) {
	t.Helper()
	dir := filepath.Join(slotRootPath(root, slot), "app", "node_modules", "@deepseek-ai", "dsh")
	mustWrite(t, filepath.Join(dir, "package.json"), fmt.Sprintf(`{"name":"@deepseek-ai/dsh","version":%q}`, version))
}

// writeSlotSessions plants n session files in one slot's DSH_HOME.
func writeSlotSessions(t *testing.T, root, slot string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		mustWrite(t, filepath.Join(sessionsDir(root, slot), fmt.Sprintf("w%d", i), "session.jsonl"), "{}")
	}
}

// TestDshVersionInSlotReadsTheSlotNotTheBuild: the popup used to print the dsh
// version baked into this exe by -ldflags, so a slot upgraded after the build kept
// being reported as the old one. The version now comes from the slot.
func TestDshVersionInSlotReadsTheSlotNotTheBuild(t *testing.T) {
	cfg := DefaultConfig()
	root := t.TempDir()
	writeSlot(t, root, slotA, cfg)
	writeSlot(t, root, slotB, cfg)
	writeSlotDshPackage(t, root, slotA, "0.1.7-rc.1")

	if got := dshVersionInSlot(root, cfg, slotA); got != "0.1.7-rc.1" {
		t.Fatalf("slot-a 的 dsh 版本 = %q，应为 0.1.7-rc.1", got)
	}
	// 一个连 package.json 都没有的槽必须降级，绝不能回落到编译期常量。
	if got := dshVersionInSlot(root, cfg, slotB); got != "" {
		t.Fatalf("没有 package.json 的槽读出 %q，应为空", got)
	}
	if got := slotVersionText(dshVersionInSlot(root, cfg, slotB)); got != "未知" {
		t.Fatalf("读不到版本时显示 %q，应为 未知", got)
	}
	if got := dshVersionInSlot(root, cfg, "slot-c"); got != "" {
		t.Fatalf("无效槽读出 %q，应为空", got)
	}
}

// TestSessionCountCountsFilesInTheSlot: the number behind 「切换后看不到当前槽的对话」
// has to be a fact about the disk.
func TestSessionCountCountsFilesInTheSlot(t *testing.T) {
	root := t.TempDir()
	if got := sessionCount(root, slotA); got != 0 {
		t.Fatalf("没有 sessions 目录时 = %d，应为 0", got)
	}
	writeSlotSessions(t, root, slotA, 4)
	writeSlotSessions(t, root, slotB, 2)
	if got := sessionCount(root, slotA); got != 4 {
		t.Fatalf("slot-a 会话数 = %d，应为 4", got)
	}
	if got := sessionCount(root, slotB); got != 2 {
		t.Fatalf("slot-b 会话数 = %d，应为 2", got)
	}
}

// TestStatusPopupCarriesEachSlotsOwnDshVersion: one line per slot, each with the
// version really in that slot and its session count, and the installation line with
// DSH-AB 自己的版本 only - the build-time dsh version is exactly what used to be
// printed here and be wrong.
func TestStatusPopupCarriesEachSlotsOwnDshVersion(t *testing.T) {
	root := t.TempDir()
	cfg := launchTestConfig()
	writeSlot(t, root, slotA, cfg)
	writeSlot(t, root, slotB, cfg)
	writeSlotDshPackage(t, root, slotA, "0.1.7-rc.1")
	writeSlotSessions(t, root, slotA, 2)
	a, _ := newTrayTestApp(t, root, cfg)

	text := a.statusText()
	for _, want := range []string{
		"slot-a：已安装，未运行　dsh 0.1.7-rc.1　会话 2 个",
		"slot-b：已安装，未运行　dsh 未知　会话 0 个",
		"版本：DSH-AB " + effectiveDshabVersion(),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("状态弹窗里没有 %q：\n%s", want, text)
		}
	}
}

// TestSessionsDirMatchesTheChildsDshHome: the count must look where the running dsh
// really writes, which is the DSH_HOME childEnv hands it.
func TestSessionsDirMatchesTheChildsDshHome(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, slotA, "data", "sessions")
	if got := sessionsDir(root, slotA); got != want {
		t.Fatalf("sessionsDir = %q，应为 %q", got, want)
	}
	cfg := DefaultConfig()
	r := NewRunner(root, cfg, nil)
	var home string
	for _, kv := range r.childEnv(slotRootPath(root, slotA)) {
		if strings.HasPrefix(kv, "DSH_HOME=") {
			home = strings.TrimPrefix(kv, "DSH_HOME=")
		}
	}
	if filepath.Join(home, "sessions") != want {
		t.Fatalf("DSH_HOME=%q 与计数目录 %q 不是同一个地方", home, want)
	}
}
