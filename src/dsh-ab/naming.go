package main

// 这份安装在「应用和功能」、开始菜单和状态弹窗标题上叫什么名字，以及它的快捷方式放在哪。
//
// 名字规则（安装期由 installer\dsh-ab.iss 的 InstallName 算同一个名字，两处必须逐字同规则）：
// 名字里不出现安装目录的名字，也不看默认目录。**括号是名字本身的一部分**，各显示面（卸载登记项、
// 开始菜单条目、桌面快捷方式、状态弹窗标题）直接用这个名字，不再各补各的括号：
//   * 本机只有这一份，且端口可用            → DSH-AB
//   * 本机有别的份，本份端口可用且不冲突    → DSH-AB (<ports.production>)
//   * 端口不可用，或与别的份的端口相同      → DSH-AB (<tag>) + 一次弹窗请用户改端口
// 测试构建（build\build.ps1 -TestProduct）把 appName 换掉，三种形态同样多一个「test」：
// DSH-ABtest / DSH-ABtest (<端口>) / DSH-ABtest (<tag>)。
// 「端口不可用」= 试绑 127.0.0.1:<ports.production> 失败。本份的 dsh 只可能由本份的托盘拉起，
// 而名字在托盘启动 dsh 之前算出来，所以此刻这个端口上真有程序在听，就一定是别的进程（另一份安装
// 的 dsh，或别的软件）——那正是用户要看见的「端口被占用」。
// 「冲突」= 本份的 ports.production 与另一份安装的生产端口相同。
// tag = 安装根 SHA-256 前 12 位十六进制，与卸载键同源。规则每次启动重算一次：改过 dsh-ab.toml
// 里的端口，条目和快捷方式就跟着改（只在本份真的变了时才动）。
//
// 安装器里有一份同样规则的 Pascal 实现（installer/dsh-ab.iss 的 InstallName）。两份必须给出同一个
// 答案，所以两边的判断依据只能是「本机其它 DSH-AB 安装的 InstallLocation 与各自 toml 的
// ports.production」——那是安装器和 DSH-AB 都能读到的东西，也都不需要碰 dsh。
//
// DSH-AB 只改自己的东西：自己的 Add/Remove 条目、自己的快捷方式。别的安装的条目和快捷方式一个字节
// 都不动。

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// uninstallKeyRoot is where a per-user installation's Add/Remove entry lives.
	uninstallKeyRoot = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	// appID is [Setup] AppId in installer/dsh-ab.iss, straight from the source of
	// truth. Only the installer's own entry names are built from it; finding *this*
	// installation's entry goes by InstallLocation instead, because an installation
	// made by an earlier build has a key that does not carry the tag.
	appID = "{6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}"
	// exeName is [Setup] AppExeName. It is half of the scan rule below: an entry whose
	// UninstallString or DisplayIcon names this executable belongs to DSH-AB whatever its
	// DisplayName says.
	exeName = "DSH_AB.exe"
)

// appName is [Setup] AppName, and a variable rather than a constant on purpose: a test build is a
// separate product (DSH-ABtest), and build\build.ps1 injects that name here with -ldflags -X. The
// name below is the release product's and is never edited per build.
var appName = "DSH-AB"

// productFamily is the beginning every product name in this codebase shares: "DSH-AB" and the test
// build's "DSH-ABtest". A DisplayName starting with it names *some* product of this family, so the
// exe-name half of the scan rule must not reach across to the other one.
const productFamily = "DSH-AB"

// installPeer is one other DSH-AB installation on this machine: where it lives and
// which production port it claims.
type installPeer struct {
	root string
	prod int
}

// removeTrailingSep drops a trailing separator unless that would leave a bare root,
// which is Inno's RemoveBackslashUnlessRoot and has to stay that way: the installer
// and this program have to hash the same string.
func removeTrailingSep(p string) string {
	t := strings.TrimRight(p, `\/`)
	if t == "" {
		return p
	}
	if len(t) == 2 && t[1] == ':' {
		return t + `\`
	}
	return t
}

// sameDir compares two paths the way Windows does: case-insensitively, and with a
// trailing separator ignored.
func sameDir(a, b string) bool {
	return strings.EqualFold(removeTrailingSep(a), removeTrailingSep(b))
}

// installTag is the installer's InstallTag: the first twelve hex characters of the
// SHA-256 of the lowercased installation root, hashed as UTF-16LE
// (GetSHA256OfUnicodeString). It is the last-resort disambiguator below.
func installTag(root string) string {
	units := utf16.Encode([]rune(strings.ToLower(removeTrailingSep(root))))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[i*2:], u)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])[:12]
}

// The two reasons a name falls back to the tag. They are what the startup popup tells the user,
// so they are fixed strings rather than sentences built at the call site.
const (
	reasonPortUnusable = "端口不可用"
	reasonPortConflict = "端口与另一份安装冲突"
)

// portUsable reports whether this machine can still serve this installation's port: a throwaway
// bind on the address dsh listens on. It is the install-time probe of installer/dsh-ab.iss
// (IsPortFree) on the other side of the same rule, and it catches a listener *and* a port that is
// reserved without anyone listening - a client socket, an excluded port range (WSAEACCES). Any
// failure to even ask counts as not usable: the safe side, and the popup explains it.
func portUsable(host string, port int) bool {
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// portsConflict is the second half of the fallback: this installation and another one serve the same
// production port, so neither can tell the other apart in the 开始菜单 folder - that folder has no
// subfolders, so the names alone are what separates the two installations.
// Only the production port counts: it is the one port that is really this installation's identity
// (the second slot is verified on a port the AI picks for itself, which DSH-AB never owns).
func portsConflict(prod int, peers []installPeer) bool {
	for _, p := range peers {
		if p.prod == prod {
			return true
		}
	}
	return false
}

// installName is the name one installation shows, and when it had to fall back to the tag, why.
// usable is portUsable of this installation's production port; the peer list comes from the
// machine, not from the installation itself. This is InstallName in installer/dsh-ab.iss, and the
// two must answer the same thing for the same machine, so a change here lands there too.
func installName(root string, prod int, usable bool, peers []installPeer) (name, reason string) {
	fallback := func(reason string) (string, string) {
		return appName + " (" + installTag(root) + ")", reason
	}
	if !usable {
		return fallback(reasonPortUnusable)
	}
	if portsConflict(prod, peers) {
		return fallback(reasonPortConflict)
	}
	// 只有这一份：名字里不需要端口，也不需要 tag。
	if len(peers) == 0 {
		return appName, ""
	}
	// 有别的份共存：端口是区分它们的那一个事实。
	return appName + " (" + strconv.Itoa(prod) + ")", ""
}

// nameNotice is the one popup the tag fallback owes the user: which port is in the way, what the
// installation is called meanwhile, and the one file that fixes it. It is pure so the wording can
// be pinned without a desktop, and it is only ever built for a name that really fell back.
func nameNotice(name, reason string, prod int) string {
	return fmt.Sprintf(
		"这份安装暂时叫 %s，因为%s。\n\n"+
			"生产端口 %d：这个端口现在可能被别的程序占着（或落在系统保留段），"+
			"也可能本机另一份 DSH-AB 安装用的是同一个端口。\n\n"+
			"请改 dsh-ab.toml 里的 ports.production（避开别的安装用着的端口），"+
			"再重启 DSH_AB.exe：名字会自动改成 %s (<端口>)。",
		name, reason, prod, appName)
}

// installEntry is one DSH-AB Add/Remove entry read back from the registry.
type installEntry struct {
	key      string
	location string
}

// scanInstallEntries returns every DSH-AB Add/Remove entry in HKCU, this installation's
// included.
//
// The rule for what counts as one is fixed here, and installer/dsh-ab.iss's OtherInstallDirs
// applies it verbatim: the entry has an InstallLocation, and either its DisplayName starts with
// the product name or its UninstallString / DisplayIcon names DSH_AB.exe. The first half covers
// the entry this build writes and the ones earlier builds wrote; the second half keeps an entry
// recognisable when its name is something else. Matching on the key name would not: the key
// carries a compile-time AppId, and the installation this machine already has was made by a
// build with a different one.
func scanInstallEntries() []installEntry {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKeyRoot, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []installEntry
	for _, n := range names {
		sub, err := registry.OpenKey(registry.CURRENT_USER, uninstallKeyRoot+`\`+n, registry.READ)
		if err != nil {
			continue
		}
		loc, _, locErr := sub.GetStringValue("InstallLocation")
		dn, _, _ := sub.GetStringValue("DisplayName")
		un, _, _ := sub.GetStringValue("UninstallString")
		ic, _, _ := sub.GetStringValue("DisplayIcon")
		sub.Close()
		if locErr != nil {
			continue
		}
		if !isDshAbEntry(dn, un, ic) {
			continue
		}
		out = append(out, installEntry{key: n, location: removeTrailingSep(loc)})
	}
	return out
}

// isDshAbEntry is ruling B's scan rule without the registry around it: an entry belongs to this
// product when its DisplayName names this product, or - when the DisplayName names no product of
// this family at all - when its UninstallString or DisplayIcon names DSH_AB.exe. Whether
// InstallLocation exists is the caller's half of the rule, because only the caller can tell a
// missing value from an empty one.
//
// The first word of a DisplayName is the product name and Inno appends its own "<version>
// (<installation name>)" behind it, so that word alone decides whose entry it is. That is what keeps
// the release build and a test build (DSH-ABtest) from claiming each other's entry: both ship
// DSH_AB.exe, so the exe-name fallback would otherwise match either one.
// installer/dsh-ab.iss's IsOwnProductEntry is the same rule.
func isDshAbEntry(displayName, uninstallString, displayIcon string) bool {
	if dn := strings.TrimSpace(displayName); dn != "" {
		word := dn
		if i := strings.IndexAny(word, " \t"); i >= 0 {
			word = word[:i]
		}
		if strings.HasPrefix(word, productFamily) {
			return word == appName
		}
	}
	return strings.Contains(uninstallString, exeName) || strings.Contains(displayIcon, exeName)
}

// peersOf splits those entries into this installation and the others, and reads the
// two ports each of the others claims out of its own dsh-ab.toml.
func peersOf(entries []installEntry, root string) []installPeer {
	var peers []installPeer
	for _, e := range entries {
		if e.location == "" || sameDir(e.location, root) {
			continue
		}
		if st, err := os.Stat(e.location); err != nil || !st.IsDir() {
			continue
		}
		// A peer with no readable dsh-ab.toml counts as 0, which collides with nothing. That is
		// the installer's answer too (dsh-ab.iss PortsOfInstall leaves 0 in a variable it cannot
		// read), and it has to be: LoadConfig alone would answer the shipped default port here,
		// and the installer and this program would then disagree about whether the two
		// installations conflict, i.e. about the name on screen.
		prod := 0
		if _, err := os.Stat(filepath.Join(e.location, "dsh-ab.toml")); err == nil {
			if cfg, _ := LoadConfig(filepath.Join(e.location, "dsh-ab.toml")); cfg != nil {
				prod = cfg.Ports.Production
			}
		}
		peers = append(peers, installPeer{root: e.location, prod: prod})
	}
	return peers
}

// The installer records where it put this installation's two shortcuts, in this
// installation's own Add/Remove entry (installer/dsh-ab.iss, WriteUninstallEntry). These
// two value names are that contract, and the app is the one that keeps them current.
const (
	linkValueStartMenu = "DshAbStartMenuLink"
	linkValueDesktop   = "DshAbDesktopLink"
)

// startMenuDir is where this installation's Start menu entry always lives: <Programs> itself,
// whether this is the only installation or one of several. 不再有
// <Programs>\<appName>\ 子文件夹——这台机器的外壳看到 .lnk 被改动过就不再按文件夹分组，显示成散的、
// 和磁盘不一致，重启外壳才恢复。快捷方式直接放根下，磁盘上是什么、用户就看到什么，不再依赖外壳分组。
// Empty when Windows does not tell us where the Start menu is; the move below then leaves the
// shortcut in the folder it is already in.
func startMenuDir() string {
	ap := os.Getenv("APPDATA")
	if ap == "" {
		return ""
	}
	return filepath.Join(ap, "Microsoft", "Windows", "Start Menu", "Programs")
}

// removeEmptyStartMenuFolder deletes <Programs>\<appName> once the last shortcut that used to live
// there has moved out - 旧版（多份安装时各进一个子文件夹）装出来的空壳，升级后的第一次启动就该消失。
// os.Remove removes an empty directory only, never its content, and a folder another installation
// still keeps its shortcut in stays where it is. The one path it can ever build is a child of
// <Programs>, so the Start menu's Programs root itself is never at risk (unlike the uninstaller,
// which derives that folder from the recorded link path and therefore has to exclude the root by
// hand). A missing or non-empty folder is the normal case and says nothing.
func removeEmptyStartMenuFolder(lg *Logger) {
	programs := startMenuDir()
	if programs == "" {
		return
	}
	dir := filepath.Join(programs, appName)
	if err := os.Remove(dir); err == nil {
		lg.Info("已删除空的开始菜单文件夹：%s", dir)
	}
}

// linkTarget is where a recorded shortcut has to end up: <dir>\<name>.lnk, or just a new name
// in the folder it already sits in when dir is empty (the desktop, whose real path only the
// installer knows). An empty result means "already in place, leave it alone".
//
// 开始菜单那一份的 dir 永远是 <Programs> 根，所以落在 <Programs>\<appName>\ 里的旧条目由这里搬出来，
// 从多份切回单份时也只是改名，不换目录——落点本来就只有名字在变。
func linkTarget(src, dir, name string) string {
	if dir == "" {
		dir = filepath.Dir(src)
	}
	desired := filepath.Join(dir, name+".lnk")
	if sameDir(src, desired) {
		return ""
	}
	return desired
}

// moveRecordedLink moves the shortcut recorded under valueName to where linkTarget wants it and
// stores the new path back under the same value, so the next start knows where to look. A path
// that is gone stays gone: the desktop shortcut is opt-in, and creating shortcuts is the
// installer's job - this only ever recreates and moves one that already exists.
//
// It goes by the recorded path and never by the shortcut's own bytes: the shell stores the target
// as a list of shell items plus a relative LinkInfo path, so the absolute path of our exe does not
// appear contiguously in the .lnk at all (measured 2026-09-22 on a fresh install and on a C:
// shortcut pointing at a D: target). A content scan therefore finds nothing, and renaming by a
// guessed name could take another installation's shortcut, and a shortcut may only ever be renamed
// or moved through its own recorded path. That path is exact, and no other installation's file can
// ever be named by it.
func moveRecordedLink(k registry.Key, valueName, dir, name string, lg *Logger) {
	src, _, err := k.GetStringValue(valueName)
	if err != nil || src == "" {
		return
	}
	if _, err := os.Stat(src); err != nil {
		// 开始菜单那条必须存在：它不在，说明安装器记下的路径不是它真正创建的那个文件（2026-09-22
		// 真机遇到过一次：两份安装同时进行，记录指向 <Programs>\{#AppName}\… 而实际建的是根下
		// 那一个，于是那份安装的开始菜单里什么都没有）。桌面那条本来就可能没建（opt-in），不报警。
		if valueName == linkValueStartMenu {
			lg.Warnf("条目里记的开始菜单快捷方式不存在：%s", src)
		}
		return
	}
	desired := linkTarget(src, dir, name)
	if desired == "" {
		return
	}
	if _, err := os.Stat(desired); err == nil {
		lg.Warnf("快捷方式 %s 没搬：目标名已被占（%s）", src, desired)
		return
	}
	if err := os.MkdirAll(filepath.Dir(desired), 0o755); err != nil {
		lg.Warnf("无法创建快捷方式目录 %s：%v", filepath.Dir(desired), err)
		return
	}
	if err := moveLinkFile(src, desired); err != nil {
		lg.Warnf("%v", err)
		return
	}
	// 外壳是另外告诉的：MoveFile 出来的新条目，正在跑的开始菜单进程看不见位置变化。
	if err := notifyShellRenameItem(src, desired); err != nil {
		lg.Warnf("无法通知外壳快捷方式已移动（%s → %s）：%v", src, desired, err)
	}
	if err := k.SetStringValue(valueName, desired); err != nil {
		lg.Warnf("无法把新的快捷方式路径写回 %s：%v", valueName, err)
	}
	lg.Info("快捷方式已更新：%s → %s", src, desired)
}

// moveLinkFile is one shortcut move: a byte-for-byte copy of src is created at dst and src is
// deleted. Copying the bytes instead of os.Rename-ing the file is the point（2026-09-22 真机实测）：
// MoveFile 出来的快捷方式，正在运行的开始菜单进程（StartMenuExperienceHost）不认——用户看到的是
// 两条散落在开始菜单里的条目，而不是一个 <appName> 文件夹，重启该进程才正常；而安装器直接**创建**
// 的（例如 <Programs>\DSH-AB\DSH-AB.lnk）从来都当场正确。.lnk 的字节本身不变（目标路径在文件里是
// shell item 加相对的 LinkInfo，见 moveRecordedLink），变的只是它所在的路径，所以拷贝内容是对的。
//
// 删不掉旧的就把新的收回：宁可这一次什么都没搬（记录还指着源文件，下次启动重试），也不要留下
// 一条重复条目。
func moveLinkFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("无法读取旧快捷方式 %s：%v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return fmt.Errorf("无法在新位置 %s 创建快捷方式：%v", dst, err)
	}
	if err := os.Remove(src); err != nil {
		os.Remove(dst)
		return fmt.Errorf("无法删掉旧快捷方式 %s（%s 已撤回）：%v", src, dst, err)
	}
	return nil
}

// alignInstallIdentity makes the two things the user reads match the names and the
// layout the requirements fix, and returns the name in force plus, when the name had to
// fall back to the tag, the one popup the user has to see about it (an empty string
// means there is nothing to say). It runs once, on startup: everything it touches is
// DSH-AB's own entry and DSH-AB's own shortcuts, and no failure here may stop the start.
func alignInstallIdentity(root string, ports PortsConfig, lg *Logger) (name, notice string) {
	entries := scanInstallEntries()
	peers := peersOf(entries, root)
	usable := portUsable(ports.Host, ports.Production)
	var reason string
	name, reason = installName(root, ports.Production, usable, peers)
	if reason != "" {
		lg.Warnf("名字退化为 %s：%s（生产端口 %d）", name, reason, ports.Production)
		// 端口不可用时不再单独弹这条：那次启动必然失败，只会看到一条
		// 「dsh 启动失败」，两条弹窗说的其实是同一件事（去改 ports.production）。端口本身是空的、
		// 只是与别的安装撞了端口时，启动能成功，这条提示就是那个名字的唯一解释，得留着。
		if reason != reasonPortUnusable {
			notice = nameNotice(name, reason, ports.Production)
		}
	} else {
		lg.Info("安装名：%s（本机另有 %d 份 DSH-AB）", name, len(peers))
	}

	// 自己那一条条目：InstallLocation 就是本安装根（安装器写的是 Root + '\'，所以这个比较
	// 必须忽略尾分隔符——2026-09-22 真机实测：忽略不掉时每份安装会把自己当成「另一份安装」，
	// 名字永远退化成 tag，自己的显示名也永远不改）。
	if key := ownEntryKey(entries, root); key != "" {
		if k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE|registry.SET_VALUE); err != nil {
			lg.Warnf("无法打开自己的应用和功能条目 %s：%v", key, err)
		} else {
			setDisplayName(k, name, lg)
			setDisplayVersion(k, effectiveDshabVersion(), lg)
			setEstimatedSize(k, root, lg)
			moveRecordedLink(k, linkValueStartMenu, startMenuDir(), name, lg)
			moveRecordedLink(k, linkValueDesktop, "", name, lg)
			// 搬完之后：旧版留在 <Programs>\<appName>\ 里的空壳（本份是最后一条时才有）由这一步消失。
			// 放在两条搬运动作之后，因为搬出去的那条可能正是让那个文件夹变空的那一条。
			removeEmptyStartMenuFolder(lg)
			k.Close()
		}
	}
	return name, notice
}

// ownEntryKey is this installation's own Add/Remove key: the entry whose InstallLocation is our
// own root. Entries made by earlier builds carry a different key name, so the key is looked up by
// its location and never by name.
func ownEntryKey(entries []installEntry, root string) string {
	for _, e := range entries {
		if sameDir(e.location, root) {
			return uninstallKeyRoot + `\` + e.key
		}
	}
	return ""
}

// setDisplayName rewrites the display name of our own entry to the name in force. It is written as
// one whole string, keeping nothing of Inno's "<product> <version>" prefix: the name carries no
// version number (that one lives in DisplayVersion) and the parentheses are already part of it. The installer
// writes this same string (dsh-ab.iss, WriteUninstallEntry), so the two are literally identical.
func setDisplayName(k registry.Key, name string, lg *Logger) {
	cur, _, err := k.GetStringValue("DisplayName")
	if err != nil || cur == name {
		return
	}
	if err := k.SetStringValue("DisplayName", name); err != nil {
		lg.Warnf("无法更新应用和功能条目里的显示名：%v", err)
		return
	}
	lg.Info("应用和功能条目显示名已更新：%s → %s", cur, name)
}

// 就地更新（Release 里的 DSH-AB-<版本>-update.zip，流程写在随包 skill 里）不经过安装程序，条目里的
// 版本与体积会停在安装那一刻的值。启动时在这里一并刷新，用户看到的就一直是这台机器上真实的程序版本
// 与体积。三条保证与 setDisplayName 一模一样：只写自己那一条条目、只在真的不一样时写、失败只告警、
// 绝不影响启动。

// setDisplayVersion 把条目里的 DisplayVersion 对齐到本程序自己的版本（effectiveDshabVersion）。
func setDisplayVersion(k registry.Key, version string, lg *Logger) {
	if version == "" {
		return
	}
	cur, _, err := k.GetStringValue("DisplayVersion")
	if err == nil && cur == version {
		return
	}
	if err := k.SetStringValue("DisplayVersion", version); err != nil {
		lg.Warnf("无法更新应用和功能条目里的版本：%v", err)
		return
	}
	lg.Info("应用和功能条目版本已更新：%s → %s", cur, version)
}

// setEstimatedSize 把条目里的 EstimatedSize（应用和功能的「大小」列，DWORD，单位 KB）对齐到安装根
// 真实的体积。安装程序的 DirSizeBytes 是同一个口径：走目录累加，向上取整，所以一处真实安装永远不会
// 显示 0 KB。
func setEstimatedSize(k registry.Key, root string, lg *Logger) {
	bytes, err := dirSizeBytes(root)
	if err != nil {
		lg.Warnf("无法统计安装根 %s 的体积：%v", root, err)
		return
	}
	kb := (bytes + 1023) / 1024
	// EstimatedSize 是 DWORD：4 GB 以上根本表达不了，宁可把上限写满，也不写一个回绕之后的小数字。
	if max := int64(^uint32(0)); kb > max {
		kb = max
	}
	want := uint32(kb)
	cur, _, err := k.GetIntegerValue("EstimatedSize")
	if err == nil && uint32(cur) == want {
		return
	}
	if err := k.SetDWordValue("EstimatedSize", want); err != nil {
		lg.Warnf("无法更新应用和功能条目里的体积：%v", err)
		return
	}
	lg.Info("应用和功能条目体积已更新：%d KB → %d KB", cur, want)
}

// dirSizeBytes 是安装程序里 DirSizeBytes 的 Go 版本：递归累加安装根下每个普通文件的大小。
// **重解析点（junction / 符号链接）不进去**：dsh 的模块回退就是用 junction 搭出来的，顺着它走既可能
// 把同一批文件算两遍，也可能绕回上一层变成死循环——而这里只是要给「大小」列一个数。
func dirSizeBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			// 单个文件读不到（正被写、权限不足）：跳过它。报一个近似的体积好过什么都不写。
			return nil
		}
		if d.IsDir() {
			if path != root && isReparsePoint(path) {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// isReparsePoint reports whether path is a reparse point (a junction or a symbolic link) without
// following it: os.Lstat alone cannot tell a junction from a plain directory.
func isReparsePoint(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
