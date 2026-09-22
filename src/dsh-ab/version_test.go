package main

import (
	"os"
	"strings"
	"testing"
)

// 阶段 7 第 2 步：两个版本号必须真的编进产物，看得见、验得了，而不是靠猜。
func TestVersionTextCarriesBothVersions(t *testing.T) {
	got := versionText("0.1.0", "0.1.5-rc.2")
	for _, want := range []string{"0.1.0", "0.1.5-rc.2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("versionText(\"0.1.0\", \"0.1.5-rc.2\") = %q，缺少版本号 %q", got, want)
		}
	}
	if lines := strings.Count(strings.TrimRight(got, "\n"), "\n"); lines != 0 {
		t.Errorf("versionText 应该是单行，实际有 %d 个换行：%q", lines, got)
	}
}

// dsh 版本没注入时也要说清楚，不能印出一个空版本号。
func TestVersionTextWithoutInjectedDshVersion(t *testing.T) {
	got := versionText("0.1.0", "")
	if !strings.Contains(got, "0.1.0") {
		t.Fatalf("versionText = %q，必须至少带上 dshab 自己的版本", got)
	}
	if strings.HasSuffix(got, "dsh )") || strings.HasSuffix(got, "dsh )\n") {
		t.Fatalf("versionText = %q，dsh 版本缺失时不能留一个空括号", got)
	}
}

// dshab 自身版本的唯一来源是 src/dsh-ab/VERSION：没有 -ldflags 注入时也必须拿到它。
func TestDefaultDshabVersionComesFromTheVersionFile(t *testing.T) {
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("读不到 src/dsh-ab/VERSION：%v", err)
	}
	want := strings.TrimSpace(string(raw))
	if want == "" {
		t.Fatal("src/dsh-ab/VERSION 是空的")
	}
	if got := effectiveDshabVersion(); got != want {
		t.Fatalf("effectiveDshabVersion() = %q，VERSION 里是 %q（版本号必须只有一个来源）", got, want)
	}
}

// --version 必须在单实例互斥和托盘之前就把进程结束掉：安装包与校验脚本靠它读取
// 「这个 exe 里到底编了哪个 dsh 版本」。
func TestVersionFlagPrintsOneLineAndStopsTheProgram(t *testing.T) {
	var out strings.Builder
	if !handleVersionFlag([]string{"--version"}, &out) {
		t.Fatal("--version 没有被识别")
	}
	printed := strings.TrimRight(out.String(), "\r\n")
	if printed == "" {
		t.Fatal("--version 什么都没输出")
	}
	if strings.ContainsAny(printed, "\r\n") {
		t.Fatalf("--version 输出了多行：%q", printed)
	}
	if !strings.Contains(printed, effectiveDshabVersion()) {
		t.Errorf("--version 的输出 %q 里没有 dshab 版本 %q", printed, effectiveDshabVersion())
	}

	var ignored strings.Builder
	for _, args := range [][]string{{}, {"web"}, {"--help"}} {
		if handleVersionFlag(args, &ignored) {
			t.Errorf("参数 %v 被误判成版本查询", args)
		}
	}
	if ignored.String() != "" {
		t.Errorf("非版本查询也往输出里写了东西：%q", ignored.String())
	}

	var short strings.Builder
	if !handleVersionFlag([]string{"-v"}, &short) {
		t.Error("-v 没有被识别")
	}
}
