package main

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestTheJobTakesItsProcessDownWhenTheHandleCloses is the one check for the mechanism
// the kill safety net rests on: the process assigned to the job is gone as soon as the
// last job handle closes, which is what a killed tray (Stop-Process -Force) does to it.
// That the tray really assigns its dsh child is a process-level fact a unit test cannot
// show; what is pinned here is the flag, the handle lifetime and the assignment.
func TestTheJobTakesItsProcessDownWhenTheHandleCloses(t *testing.T) {
	// A long-running stand-in: the point is a live process that a normal test run will
	// never wait out. It is this test binary re-executed as the sleep helper, so no shell is
	// named anywhere (see testhelper_test.go).
	cmd := startHelper(t, helperSleep)

	var job jobObject
	if err := job.assign(cmd.Process.Pid); err != nil {
		t.Fatalf("归入作业对象：%v", err)
	}
	if job.handle == 0 {
		t.Fatal("assign 成功却没留下作业对象句柄")
	}

	// 托盘进程没了 = 最后一个句柄关掉。内核必须立刻带走整个 job。
	if err := windows.CloseHandle(job.handle); err != nil {
		t.Fatalf("CloseHandle：%v", err)
	}
	job.handle = 0

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("关掉作业对象句柄后子进程还活着：KILL_ON_JOB_CLOSE 没有生效")
	}
}
