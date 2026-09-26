package main

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObject is the Windows job object that owns the dsh child of one Runner.
//
// It exists for the one failure the graceful Stop cannot cover: a tray that is killed
// outright (Stop-Process -Force, 任务管理器). The child used to survive that kill, keep
// holding the production port, and make the next tray refuse to start ("端口 N 在启动前
// 就已经被占用"). With JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE the kernel ends the whole job
// as soon as the last handle to it closes, which is exactly what happens when this
// process dies for any reason. Stop() ends the child's whole tree through this same job
// (see terminate) - the one call that can end a tree whose root is a CREATE_NO_WINDOW
// console process - and the flag above stays what covers a tray that is killed outright.
//
// The zero value is ready to use and holds no handle. The handle is created on the first
// assign and deliberately never closed: it has to stay open for as long as this process
// lives, because closing it is what kills the job.
type jobObject struct {
	mu     sync.Mutex
	handle windows.Handle
	// assigned says whether the child the latest assign was called for really went into the
	// job. An empty job terminates nothing, and Stop must never read that silent no-op as a
	// killed tree: it would leave dsh running and holding the production port.
	assigned bool
}

// createKillOnCloseJob makes an unnamed job with KILL_ON_JOB_CLOSE set.
func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject 失败：%v", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return 0, fmt.Errorf("SetInformationJobObject 失败：%v", err)
	}
	return h, nil
}

// assign puts one process into the job, creating the job on first use.
//
// Every failure comes back as an error instead of being fatal: the caller logs it and
// starts dsh anyway. The safety net must never become the reason the tray does not run,
// and it never refuses a start.
func (j *jobObject) assign(pid int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	// This child is in the job only if the call at the end of this function returns nil.
	j.assigned = false
	if j.handle == 0 {
		h, err := createKillOnCloseJob()
		if err != nil {
			return err
		}
		j.handle = h
	}
	// os/exec does not expose the process handle it holds, and
	// AssignProcessToJobObject wants one carrying PROCESS_SET_QUOTA.
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess(%d) 失败：%v", pid, err)
	}
	defer windows.CloseHandle(p)
	if err := windows.AssignProcessToJobObject(j.handle, p); err != nil {
		return fmt.Errorf("AssignProcessToJobObject(%d) 失败：%v", pid, err)
	}
	j.assigned = true
	return nil
}

// owns reports whether the child the last assign was called for really went into this job.
// An empty job answers every call successfully and ends nothing, so Stop asks this before it
// trusts the tree route.
func (j *jobObject) owns() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.assigned
}

// terminate ends every process in the job with one call, which is how the whole tree of the
// dsh child goes down at once: the child is a console program started with CREATE_NO_WINDOW,
// so nothing else can end its tree in one go. Best effort as always - the caller logs the
// error and still waits for the child itself afterwards.
func (j *jobObject) terminate() error {
	j.mu.Lock()
	h := j.handle
	j.mu.Unlock()
	if h == 0 {
		return errors.New("作业对象还未创建")
	}
	if err := windows.TerminateJobObject(h, 1); err != nil {
		return fmt.Errorf("TerminateJobObject 失败：%v", err)
	}
	return nil
}
