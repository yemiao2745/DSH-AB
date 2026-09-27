package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 名字规则（与 installer\dsh-ab.iss 的 InstallName 逐字同规则）：名字里不出现安装目录的名字，
// 也不看默认目录。可能的答案只有三种形态：DSH-AB、DSH-AB (端口)、DSH-AB (tag)（tag = 安装根
// SHA-256 的前 12 位十六进制）。**括号属于名字本身**，各显示面直接用这个名字，所以这条正则就是
// 「多一个字都不行」本身。
var installNameShape = regexp.MustCompile(`^DSH-AB(?: \([0-9]+\)| \([0-9a-f]{12}\))?$`)

// 固定的根让 tag 变成字面量，于是这条同时钉住了规则表和 tag 的算法：安装器的 InstallTag
// （GetSHA256OfUnicodeString 取小写安装根的前 12 位十六进制）必须给出同一个值。
func TestInstallNameRule(t *testing.T) {
	const root = `D:\test\DSH-AB`

	// 本机只有这一份、端口可用 → 裸名（没有后缀就没有空括号）。名字不看目录，所以非默认目录也一样。
	if got, why := installName(root, 3190, true, nil); got != "DSH-AB" || why != "" {
		t.Fatalf("单份安装 installName = %q / %q, want %q / \"\"", got, why, "DSH-AB")
	}
	// 有别的份共存、端口可用且不冲突 → 带上生产端口，括号在名字里。
	peers := []installPeer{{root: `D:\test\two\DSH-AB`, prod: 3290}}
	if got, why := installName(root, 3190, true, peers); got != "DSH-AB (3190)" || why != "" {
		t.Fatalf("多份安装 installName = %q / %q, want %q / \"\"", got, why, "DSH-AB (3190)")
	}
	// 端口不可用（试绑失败）→ 退化成 tag，并且带出「请改端口」的那条弹窗理由。
	tag := installTag(root)
	got, why := installName(root, 3190, false, nil)
	if got != "DSH-AB ("+tag+")" || why != reasonPortUnusable {
		t.Fatalf("端口不可用时 installName = %q / %q, want %q / %q", got, why, "DSH-AB ("+tag+")", reasonPortUnusable)
	}
	// 三种字面量钉死：合法答案就只有这三串，多一个字都不行。
	if got != "DSH-AB (6e34a04e1de4)" {
		t.Fatalf("tag 形态的字面量 = %q, want \"DSH-AB (6e34a04e1de4)\"", got)
	}
	if single, _ := installName(root, 3190, true, nil); single != "DSH-AB" {
		t.Fatalf("单份形态的字面量 = %q, want \"DSH-AB\"", single)
	}
	if multi, _ := installName(root, 3190, true, peers); multi != "DSH-AB (3190)" {
		t.Fatalf("多份形态的字面量 = %q, want \"DSH-AB (3190)\"", multi)
	}
	if notice := nameNotice(got, why, 3190); !strings.Contains(notice, "3190") || !strings.Contains(notice, "dsh-ab.toml") {
		t.Fatalf("弹窗没有说清要改哪个端口/哪个文件：%q", notice)
	}
	// 与另一份安装的**生产**端口相同 → 冲突。
	sameProd := []installPeer{{root: `D:\test\two\DSH-AB`, prod: 3190}}
	if got, why := installName(root, 3190, true, sameProd); got != "DSH-AB ("+tag+")" || why != reasonPortConflict {
		t.Fatalf("生产端口撞车时 installName = %q / %q, want %q / %q", got, why, "DSH-AB ("+tag+")", reasonPortConflict)
	}
	// 另一份的 toml 读不出来时它的端口是 0（Go 的零值，安装器那边同样是 0），0 不撞任何真实端口。
	unknown := []installPeer{{root: `D:\test\two\DSH-AB`}}
	if got, why := installName(root, 3190, true, unknown); got != "DSH-AB (3190)" || why != "" {
		t.Fatalf("端口未知的另一份不该让名字退化：installName = %q / %q", got, why)
	}
	// 固定输入必须给固定输出：安装器与 DSH-AB 各算一次，两次不一致就会来回改名。
	if again, _ := installName(root, 3190, true, sameProd); again != got {
		t.Fatalf("同样的输入给了不同的输出：%q 然后 %q", got, again)
	}
	if tag != "6e34a04e1de4" {
		t.Fatalf("installTag(%s) = %q, want 6e34a04e1de4（安装器的 InstallTag 必须给出同一个 tag）", root, tag)
	}
}

// 「任何输入都不得出现目录名」：叶子名取得与产品名毫不相干，名字里就不该有它的任何一段；形状也永远
// 只能是那三种。
func TestInstallNameNeverContainsDirectoryName(t *testing.T) {
	cases := []struct {
		root   string
		prod   int
		usable bool
		peers  []installPeer
	}{
		{`D:\test\DSH-AB`, 3190, true, nil},
		{`D:\test\my-dsh-ab`, 3190, false, nil},
		{`E:\installs\nightly-41`, 3291, true, []installPeer{{root: `D:\test\peer`, prod: 3291}}},
		{`C:\Users\test\AppData\Local\DSH-AB`, 3190, true, nil},
	}
	for _, c := range cases {
		got, why := installName(c.root, c.prod, c.usable, c.peers)
		if !installNameShape.MatchString(got) {
			t.Errorf("installName(%s, %d, %v) = %q，形状不对（只允许 DSH-AB[ (<端口>)| (<tag>)]）",
				c.root, c.prod, c.usable, got)
		}
		// 叶子名就是产品名的目录不算：那种情况下名字里的 DSH-AB 是产品名本身。
		leaf := filepath.Base(c.root)
		if !strings.EqualFold(leaf, appName) && strings.Contains(strings.ToLower(got), strings.ToLower(leaf)) {
			t.Errorf("installName(%s, %d) = %q，名字里出现了安装目录名 %q", c.root, c.prod, got, leaf)
		}
		// 理由只有「没有理由」与那两个固定说法；退化时名字必须真的带 tag。
		if why != "" && why != reasonPortUnusable && why != reasonPortConflict {
			t.Errorf("installName(%s) 给了没见过的理由 %q", c.root, why)
		}
		if why != "" && !strings.HasSuffix(got, "("+installTag(c.root)+")") {
			t.Errorf("理由 %q 说明名字该退化，但 installName(%s) = %q 没有带 tag", why, c.root, got)
		}
	}
}

// 扫描口径：一条 HKCU Uninstall 条目算不算 DSH-AB，只看 DisplayName 前缀与 UninstallString/
// DisplayIcon 里的 DSH_AB.exe；安装器里的 OtherInstallDirs 用的是同一句话。第二行是这台机器上
// 真实存在的那份安装：DisplayName 是「DSH-AB 0.1.0」，而旧构建的 AppId 与本构建不同。
func TestIsDshAbEntry(t *testing.T) {
	const root = `C:\Users\yemiao\AppData\Local\DSH-AB`
	cases := []struct {
		name                         string
		displayName, uninstall, icon string
		want                         bool
	}{
		{
			"本构建写的条目（DisplayName 带安装名）",
			"DSH-AB 0.1.5 (DSH-AB 3190)",
			`"` + root + `\unins000.exe"`,
			root + `\DSH_AB.exe`,
			true,
		},
		{
			"旧构建写的条目（本轮修的就是它）",
			"DSH-AB 0.1.0",
			`"` + root + `\unins000.exe"`,
			root + `\DSH_AB.exe`,
			true,
		},
		{
			"测试构建写的条目（正式安装必须看不见它）",
			"DSH-ABtest 0.1.5 (DSH-ABtest)",
			`"` + `C:\Users\yemiao\AppData\Local\DSH-ABtest\unins000.exe"`,
			`C:\Users\yemiao\AppData\Local\DSH-ABtest\DSH_AB.exe`,
			false,
		},
		{"只有 DisplayName 前缀", "DSH-AB", "", "", true},
		{"只有 UninstallString 提到 DSH_AB.exe", "随便什么名字", `"` + root + `\DSH_AB.exe" /uninstall`, "", true},
		{"只有 DisplayIcon 提到 DSH_AB.exe", "", "", root + `\DSH_AB.exe`, true},
		{"别的软件", "Photoshop 2026", `"C:\Program Files\PS\unins000.exe"`, `C:\Program Files\PS\ps.exe`, false},
		{"提到 dsh 但不是 DSH-AB 的那个 exe", "dsh helper", `"C:\x\dsh.exe"`, `"C:\x\dsh.exe"`, false},
		{"什么都没有", "", "", "", false},
	}
	for _, c := range cases {
		if got := isDshAbEntry(c.displayName, c.uninstall, c.icon); got != c.want {
			t.Errorf("%s: isDshAbEntry(%q, %q, %q) = %v, want %v", c.name, c.displayName, c.uninstall, c.icon, got, c.want)
		}
	}
}

// 测试构建（DSH-ABtest）与正式安装必须互相看不见：两边都装 DSH_AB.exe，所以显示名的第一个词是唯一
// 能把它们分开的东西。installer\dsh-ab.iss 的 IsOwnProductEntry 是同一句话（两处必须逐字同规则）。
func TestTestBuildAndReleaseBuildDoNotSeeEachOther(t *testing.T) {
	const releaseDN = "DSH-AB 0.1.0 (DSH-AB 3190)"
	const testDN = "DSH-ABtest 0.1.0 (DSH-ABtest)"
	const un = `"C:\x\unins000.exe"`
	const releaseIcon = `C:\Users\yemiao\AppData\Local\DSH-AB\DSH_AB.exe`
	const testIcon = `C:\Users\yemiao\AppData\Local\DSH-ABtest\DSH_AB.exe`

	defer func(prev string) { appName = prev }(appName)
	if !isDshAbEntry(releaseDN, un, releaseIcon) {
		t.Fatal("正式安装认不出自己的条目")
	}
	appName = productFamily + "test"
	if !isDshAbEntry(testDN, un, testIcon) {
		t.Fatal("测试构建认不出自己的条目")
	}
	if isDshAbEntry(releaseDN, un, releaseIcon) {
		t.Error("测试构建把正式安装的条目当成了自己的")
	}
	appName = productFamily
	if isDshAbEntry(testDN, un, testIcon) {
		t.Error("正式安装把测试构建的条目当成了自己的")
	}
}

// 回归（2026-09-22 真机实测）：安装器的 InstallLocation 带尾反斜杠（dsh-ab.iss 写的是
// Root + '\'），本程序算出来的安装根不带。两串判不出是同一个目录时，每份安装会把自己算成「另一份
// 安装」：名字永远退化成 tag 加一次弹窗，自己的显示名也永远不改。本机只有自己这一条条目时 peers 必须
// 为空——当时错的就是这里。
func TestPeersOfDoesNotCountThisInstallation(t *testing.T) {
	root := t.TempDir()
	for _, loc := range []string{root, root + `\`, root + "/", strings.ToUpper(root)} {
		entries := []installEntry{{key: "k", location: loc}}
		if peers := peersOf(entries, root); len(peers) != 0 {
			t.Errorf("InstallLocation %q 时把自己算成了另一份安装：%+v", loc, peers)
		}
	}
	// 盘符根的尾反斜杠必须保留（Inno 的 RemoveBackslashUnlessRoot 保留它），否则安装器与本程序算出的
	// tag 会不一样，两边会给同一台机器两个不同的名字。
	if got := removeTrailingSep(`D:\`); got != `D:\` {
		t.Errorf("盘符根的尾反斜杠被丢了：%q", got)
	}
	if got := removeTrailingSep(root + `\`); got != root {
		t.Errorf("removeTrailingSep(%q) = %q, want %q", root+`\`, got, root)
	}
}

// 开始菜单的自愈：快捷方式**永远**在 <Programs> 根下，
// 没有子文件夹，单份 ↔ 多份之间只有名字在变。要搬的那一条来自本安装自己的注册表记录——安装器写的
// DshAbStartMenuLink / DshAbDesktopLink（installer\dsh-ab.iss 的 WriteUninstallEntry）——而不去认 .lnk
// 的内容：外壳把目标路径拆成 shell item 与相对的 LinkInfo 路径，绝对路径在文件里根本不连续（实测），
// 按内容猜就可能动到别的安装的快捷方式。**只动记录里那一条**就是「绝不替别的安装改快捷方式」的底线。
func TestLinkTargetAlwaysUsesTheProgramsRoot(t *testing.T) {
	programs := `D:\Programs`
	sub := filepath.Join(programs, appName)
	mine := filepath.Join(programs, appName+".lnk")

	// 多份：只改名，不换目录——落点还是根下，只是名字带了端口。
	if got, want := linkTarget(mine, programs, "DSH-AB (3190)"), filepath.Join(programs, "DSH-AB (3190).lnk"); got != want {
		t.Errorf("多份共存时的落点 = %q, want %q", got, want)
	}
	// 单份 ↔ 多份的两个方向都不碰目录：同一个根下的落点对两次切换是同一个答案。
	if got, want := linkTarget(mine, programs, appName), ""; got != want {
		t.Errorf("单份时已经在对的位置还要搬：%q", got)
	}
	// 旧版（多份进子文件夹）留下的那一条：搬出来。同一个搬运路径，只是目标目录变成了根。
	inSub := filepath.Join(sub, "DSH-AB (3190).lnk")
	if got := linkTarget(inSub, programs, appName); got != mine {
		t.Errorf("子文件夹里的旧条目没有搬回根下：落点 = %q, want %q", got, mine)
	}
	// 桌面没有子文件夹：dir 传空 = 留在原目录，只改名（OneDrive 重定向过的桌面也走这条路）。
	desk := filepath.Join("C:\\", "Desktop")
	onDesk := filepath.Join(desk, appName+".lnk")
	if got, want := linkTarget(onDesk, "", "DSH-AB (3190)"), filepath.Join(desk, "DSH-AB (3190).lnk"); got != want {
		t.Errorf("桌面上的落点 = %q, want %q", got, want)
	}
}

// 落点是 <Programs> 本身（startMenuDir 已经没有 hasPeers 那个参数了），旧版留在 <Programs>\<appName>\
// 的空壳在搬空之后消失。删只能是「删空目录」：里面还有别的安装的快捷方式就留着，里面的文件一个都不能少，
// <Programs> 根本身更不是这个函数的目标（uninstaller 那边是另一回事：它的文件夹是从记录下来的链接路径
// 推出来的，所以它得自己把根排除掉）。
func TestStartMenuDirHasNoSubfolderAndDropsTheEmptyOne(t *testing.T) {
	appdata := t.TempDir()
	t.Setenv("APPDATA", appdata)
	programs := filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs")
	if got := startMenuDir(); got != programs {
		t.Fatalf("开始菜单落点 = %q, want %q", got, programs)
	}

	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	sub := filepath.Join(programs, appName)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	removeEmptyStartMenuFolder(lg) // 空的：删掉
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("空的 %s 没被删掉（stat err=%v）", sub, err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(sub, "别人的.lnk")
	if err := os.WriteFile(keep, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	removeEmptyStartMenuFolder(lg) // 非空：留着，里面的东西一个字都不动
	if b, err := os.ReadFile(keep); err != nil || !bytes.Equal(b, []byte{1, 2, 3}) {
		t.Errorf("没删成的目录里的文件被动了（读了 %x，err=%v）", b, err)
	}
	if fi, err := os.Stat(programs); err != nil || !fi.IsDir() {
		t.Errorf("<Programs> 根本身被动了：%v", err)
	}
	lg.Close()
}

// 2026-09-22 真机：MoveFile 搬出来的快捷方式，正在运行的开始菜单进程不认（用户看到两条散落在开始
// 菜单里的条目），重启该进程才正常；安装器**直接创建**的那条从来都当场正确。所以搬 = 在新位置新建
// 一条内容逐字节相同的 .lnk，再删掉旧的（moveLinkFile）。这条测试钉住的就是这两点，而且能分辨
// 「新建」与「改名」：源的创建时间被钉在过去，改名会把它原样带过来，新建则是「现在」——改回
// os.Rename 这条立刻红（内容和「旧位置没有」都对，只有创建时间出卖它）。
func TestMoveLinkFileCreatesInsteadOfRenaming(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Programs", appName+".lnk")
	dst := filepath.Join(dir, "Programs", appName, "DSH-AB (3190).lnk")
	for _, d := range []string{filepath.Dir(src), filepath.Dir(dst)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 真的 .lnk 是二进制、含 0 字节，逐字节比较才有意义。
	want := []byte{0x4c, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xc0,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46, 0x00, 0x00, 0x00}
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	past := windows.NsecToFiletime(time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano())
	if err := setCreationTime(src, past); err != nil {
		t.Fatalf("无法把 %s 的创建时间钉到过去：%v", src, err)
	}

	if err := moveLinkFile(src, dst); err != nil {
		t.Fatalf("搬 %s → %s 失败：%v", src, dst, err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("新位置没有快捷方式：%v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("新位置的 .lnk 与原来不是逐字节相同：%x，want %x", got, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("旧位置 %s 还在（stat err=%v）", src, err)
	}
	if ct := creationTime(t, dst); ct <= past.Nanoseconds() {
		t.Errorf("新文件的创建时间 %d 没刷新（等于源被钉的 %d，说明是改名不是新建）", ct, past.Nanoseconds())
	}
}

// creationTime / setCreationTime：创建时间是唯一能分辨「新建」与「改名」的东西，两个小助手把它
// 隔离在这里，测试主体只管业务。
func creationTime(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		t.Fatalf("%s 的文件信息不是 Win32 的：%T", path, info.Sys())
	}
	return d.CreationTime.Nanoseconds()
}

func setCreationTime(path string, ft windows.Filetime) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.SetFileTime(h, &ft, nil, nil)
}

// 就地更新不经过安装程序，条目里的版本与体积会停在安装那一刻的值：启动时对齐它们
// （setDisplayVersion / setEstimatedSize）——同值不写、不同值写。观察点是日志：两个函数都是**真的
// 写成功之后**才记一条，所以「日志里没有这一条」就等于「这一次没写」。两个函数只碰调用方给的那个键，
// 所以这条测试用 HKCU 下一个临时键，绝不碰任何真实的卸载登记项。
func TestEntryVersionAndSizeAreOnlyWrittenWhenTheyDiffer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.bin"), make([]byte, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	total, err := dirSizeBytes(root)
	if err != nil {
		t.Fatalf("dirSizeBytes(%s) 失败：%v", root, err)
	}
	if total != 4097 {
		t.Fatalf("dirSizeBytes = %d, want 4097（子目录里的文件也要算）", total)
	}
	sizeKB := uint32((total + 1023) / 1024) // 4097 字节 → 5 KB（向上取整，绝不为 0）

	k, done := tempRegistryKey(t)
	defer done()

	version := effectiveDshabVersion()
	if version == "" {
		t.Fatal("effectiveDshabVersion 是空的：条目里的版本会被写成空")
	}

	// 同值：一个字都不写。
	if err := k.SetStringValue("DisplayVersion", version); err != nil {
		t.Fatal(err)
	}
	if err := k.SetDWordValue("EstimatedSize", sizeKB); err != nil {
		t.Fatal(err)
	}
	same := writeAndReadLog(t, func(lg *Logger) {
		setDisplayVersion(k, version, lg)
		setEstimatedSize(k, root, lg)
	})
	for _, line := range []string{"版本已更新", "体积已更新"} {
		if strings.Contains(same, line) {
			t.Errorf("值没变却又写了一次（日志里有 %q）：\n%s", line, same)
		}
	}

	// 不同值：两个都该被改写，各留一条日志。
	if err := k.SetStringValue("DisplayVersion", "0.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := k.SetDWordValue("EstimatedSize", sizeKB+1); err != nil {
		t.Fatal(err)
	}
	changed := writeAndReadLog(t, func(lg *Logger) {
		setDisplayVersion(k, version, lg)
		setEstimatedSize(k, root, lg)
	})
	if got, _, err := k.GetStringValue("DisplayVersion"); err != nil || got != version {
		t.Errorf("DisplayVersion = %q（err %v），want %q", got, err, version)
	}
	if got, _, err := k.GetIntegerValue("EstimatedSize"); err != nil || uint32(got) != sizeKB {
		t.Errorf("EstimatedSize = %d（err %v），want %d", got, err, sizeKB)
	}
	for _, line := range []string{"版本已更新", "体积已更新"} {
		if !strings.Contains(changed, line) {
			t.Errorf("值变了却没有写（日志里没有 %q）：\n%s", line, changed)
		}
	}
}

// 顺着重解析点走会把同一批文件算两遍，还可能绕回上一层变成死循环——dsh 的模块回退就是用 junction
// 搭出来的，所以安装根体积只在重解析点外面算。junction 由测试自己用 Windows API 建（newJunction，
// DeviceIoControl + FSCTL_SET_REPARSE_POINT，与 mklink /J 同一个调用），不需要管理员权限，也不经过
// 任何 shell；建不出来就失败，而不是假装通过。
func TestDirSizeDoesNotWalkThroughAReparsePoint(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "f.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	newJunction(t, link, inner)
	if !isReparsePoint(link) {
		t.Fatalf("%s 不是重解析点：这条测试会变成对普通目录的空断言", link)
	}
	got, err := dirSizeBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != 100 {
		t.Errorf("dirSizeBytes = %d，want 100：junction 里的文件不该被算第二遍", got)
	}
}

// tempRegistryKey 给测试一个自己的键：两个函数只碰传进去的那个键，碰不到任何真实的卸载登记项。
// 拿不到注册表（策略、只读会话）就跳过，而不是假装通过。
func tempRegistryKey(t *testing.T) (registry.Key, func()) {
	t.Helper()
	path := "Software\\dsh-ab-test\\" + strconv.Itoa(os.Getpid())
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Skipf("打不开临时注册表键 %s：%v", path, err)
	}
	return k, func() {
		k.Close()
		registry.DeleteKey(registry.CURRENT_USER, path)
	}
}

// writeAndReadLog 跑一段代码再把日志整个读回来：日志是当场写盘的，Close 之后读到的就是全部内容。
func writeAndReadLog(t *testing.T, body func(lg *Logger)) string {
	t.Helper()
	lg := NewLogger(t.TempDir(), LevelFull, 1, 1)
	body(lg)
	lg.Close()
	b, err := os.ReadFile(lg.Path())
	if err != nil {
		// 一条都没写时日志文件根本不会创建——这正是「这一次没写」的样子。
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("读不出日志 %s：%v", lg.Path(), err)
	}
	return string(b)
}
