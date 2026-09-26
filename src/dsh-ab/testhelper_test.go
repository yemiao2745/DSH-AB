package main

// This file is the shared test infrastructure for child processes and for reparse points. It
// names no shell on purpose: the repository must not invoke cmd.exe, powershell.exe, reg.exe or
// taskkill.exe anywhere, tests included.
//
// Every stand-in child is this test binary, re-executed with one environment variable set (the
// documented Go testing pattern). TestMain answers that variable before any test runs, so the
// helper child never runs the suite, needs nothing installed on the machine, and looks like any
// other executable to the code under test. The bundled interpreter
// (tools\python\python.exe -c "import time; time.sleep(60)") would have tied the Go tests to
// the Python payload toolchain for no gain.
//
// The junction is created through DeviceIoControl(FSCTL_SET_REPARSE_POINT) rather than
// os.Symlink: x/sys/windows is already a dependency, a junction needs no privilege at all (a
// directory symlink needs SeCreateSymbolicLinkPrivilege or Developer Mode), and a junction is
// what dsh itself uses for its module fallback.

import (
	"encoding/binary"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const (
	testHelperEnv     = "DSHAB_TEST_HELPER"
	testHelperTrigger = "DSHAB_TEST_HELPER_TRIGGER"
	testHelperPIDFile = "DSHAB_TEST_HELPER_PIDFILE"

	helperSleep  = "sleep"  // alive until it is ended: the long-lived stand-in
	helperExit0  = "exit0"  // starts and exits 0 at once
	helperExit3  = "exit3"  // exits 3: the failing child ExitNote() is about
	helperExit1s = "exit1s" // alive for a second, then exits 0
	helperTree   = "tree"   // waits for a go-ahead, then starts a grandchild and stays alive
)

// TestMain is the helper: with the variable set, this process is a stand-in child and no test
// runs at all.
func TestMain(m *testing.M) {
	if mode := os.Getenv(testHelperEnv); mode != "" {
		os.Exit(runTestHelper(mode))
	}
	os.Exit(m.Run())
}

// runTestHelper answers with an exit code. A mode it does not know exits 2 instead of falling
// through to m.Run: falling through would run the whole suite from inside a child.
func runTestHelper(mode string) int {
	switch mode {
	case helperExit0:
		return 0
	case helperExit3:
		return 3
	case helperExit1s:
		time.Sleep(time.Second)
		return 0
	case helperTree:
		return runTreeHelper()
	case helperSleep:
		time.Sleep(5 * time.Minute)
		return 0
	}
	return 2
}

// runTreeHelper is the child of the tree tests: it starts one grandchild, but only once the
// go-ahead file exists. The order is the point - Windows adds the processes a job member creates
// *after* the assignment, so a grandchild born before Start put this process into the job would
// make the test race the very assignment it exercises.
func runTreeHelper() int {
	trigger := os.Getenv(testHelperTrigger)
	for deadline := time.Now().Add(30 * time.Second); trigger != "" && time.Now().Before(deadline); {
		if _, err := os.Stat(trigger); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	exe, err := helperBinary()
	if err != nil {
		return 4
	}
	gc := helperCommandAt(exe, helperSleep)
	if err := gc.Start(); err != nil {
		return 5
	}
	if file := os.Getenv(testHelperPIDFile); file != "" {
		_ = os.WriteFile(file, []byte(strconv.Itoa(gc.Process.Pid)), 0o644)
	}
	time.Sleep(5 * time.Minute)
	return 0
}

// helperBinary is the test binary itself: the executable every stand-in child is.
func helperBinary() (string, error) { return os.Executable() }

// helperCommandAt builds one stand-in child without starting it.
func helperCommandAt(exe, mode string) *exec.Cmd {
	cmd := exec.Command(exe)
	cmd.Env = helperEnv(mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

// helperEnv is this environment with exactly one value for the helper variable, so a child can
// never see two of them.
func helperEnv(mode string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, testHelperEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, testHelperEnv+"="+mode)
}

// helperCommand is helperCommandAt for a caller whose only other option is to fail the test.
func helperCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	exe, err := helperBinary()
	if err != nil {
		t.Fatalf("找不到测试二进制：%v", err)
	}
	return helperCommandAt(exe, mode)
}

// startHelper starts one stand-in child and makes sure it does not outlive the test.
func startHelper(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := helperCommand(t, mode)
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动替身进程：%v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

// openProcess opens one PID for waiting - and for ending it, for a caller that has to clean up a
// process the product deliberately left alone. The handle is closed with the test.
func openProcess(t *testing.T, pid int, terminate bool) windows.Handle {
	t.Helper()
	access := uint32(windows.SYNCHRONIZE)
	if terminate {
		access |= windows.PROCESS_TERMINATE
	}
	h, err := windows.OpenProcess(access, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess(%d)：%v", pid, err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	return h
}

// waitGone reports whether the process behind h ended within timeout. Handles are opened before
// the stop, never after it: an ended PID can be reused, a handle cannot be handed to another
// process.
func waitGone(t *testing.T, h windows.Handle, timeout time.Duration) bool {
	t.Helper()
	ev, err := windows.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	if err != nil {
		t.Fatalf("WaitForSingleObject：%v", err)
	}
	return ev == windows.WAIT_OBJECT_0
}

// readPIDFile waits for the tree helper's answer: the grandchild PID it wrote down.
func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等不到孙子进程的 PID 文件 %s", path)
	return 0
}

// --- a real NTFS junction, without a shell and without a privilege ---------------------------

// newJunction makes link a real junction pointing at target. FSCTL_SET_REPARSE_POINT is what
// mklink /J does; nothing here needs a console, a privilege, or an external binary.
func newJunction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("建 junction 的目录 %s：%v", link, err)
	}
	// The reparse point is set on the directory itself, so it is opened without following it.
	p, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%s)：%v", link, err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatalf("打开 %s 以设置重解析点：%v", link, err)
	}
	defer windows.CloseHandle(h)
	buf := junctionReparseBuffer(t, target)
	var returned uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT,
		&buf[0], uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		t.Fatalf("FSCTL_SET_REPARSE_POINT(%s)：%v", link, err)
	}
	// Remove the reparse point itself on cleanup, so removing the tree can never walk into the
	// target through it.
	t.Cleanup(func() { _ = os.Remove(link) })
}

// junctionReparseBuffer is REPARSE_DATA_BUFFER with a MountPointReparseBuffer: the 8-byte header,
// the four offsets and lengths, then the substitute name (verbatim "\\??\\C:\\...") and the
// print name - both UTF-16, neither NUL-terminated, because the lengths carry that.
// junctionReparseBuffer is REPARSE_DATA_BUFFER with a MOUNT_POINT_REPARSE_BUFFER, in the layout a
// real junction carries: the 8-byte header, the four uint16 fields, then PathBuffer holding the
// substitute name, a NUL, the print name and a NUL. There is no Flags field here - that belongs
// to the *symbolic link* buffer - and the offsets are relative to PathBuffer, so
// PrintNameOffset is the byte length of the substitute name plus its terminator. ReparseDataLength
// covers the four fields and the whole path buffer: 8 + (SubLen+2) + (PrintLen+2), i.e. 12 + both
// names in bytes. Layout and lengths are copied from a junction Windows itself made
// (C:\Documents and Settings), read back with FSCTL_GET_REPARSE_POINT.
func junctionReparseBuffer(t *testing.T, target string) []byte {
	t.Helper()
	substitute := utf16.Encode([]rune("\\??\\" + target))
	shown := utf16.Encode([]rune(target))
	path := append(append(append(append([]uint16{}, substitute...), 0), shown...), 0)
	data := 12 + 2*len(substitute) + 2*len(shown)
	buf := make([]byte, 8+data)
	binary.LittleEndian.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(data))                 // buf[6:] is Reserved, and stays 0
	binary.LittleEndian.PutUint16(buf[8:], 0)                            // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:], uint16(2*len(substitute)))   // SubstituteNameLength
	binary.LittleEndian.PutUint16(buf[12:], uint16(2*len(substitute)+2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(buf[14:], uint16(2*len(shown)))        // PrintNameLength
	for i, c := range path {
		binary.LittleEndian.PutUint16(buf[16+2*i:], c)
	}
	return buf
}
