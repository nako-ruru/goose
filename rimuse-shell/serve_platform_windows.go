//go:build windows

package main

import (
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"
)

const createNoWindow = 0x08000000

func hideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// killProcessTree takes the whole tree down (taskkill /t), matching the
// Electron shell's cleanup path so no orphaned goose/npx processes survive.
func killProcessTree(pid int) {
	_ = exec.Command("taskkill", "/pid", strconv.Itoa(pid), "/f", "/t").Run()
}

// ---------------------------------------------------------------------------
// Kill-on-close job object: even if the shell is force-killed (End task,
// crash — OnShutdown never runs), Windows closes the job and takes the
// goose child down with it. Best effort: failures only lose the extra
// guarantee; graceful shutdown still cleans up via killProcessTree.
// ---------------------------------------------------------------------------

var (
	k32                 = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject = k32.NewProc("CreateJobObjectW")
	procAssignJob       = k32.NewProc("AssignProcessToJobObject")
	procSetJobInfo      = k32.NewProc("SetInformationJobObject")

	killJob     syscall.Handle
	killJobOnce sync.Once
	killJobErr  error
)

// IO_COUNTERS is six ULONGLONGs on x64 (counts AND transfers).
type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type basicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type extendedLimitInformation struct {
	BasicLimitInformation basicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	processSetQuotaTerminateAccess    = 0x100 | 0x0002 // PROCESS_SET_QUOTA | PROCESS_TERMINATE
)

func ensureKillJob() error {
	killJobOnce.Do(func() {
		handle, _, err := procCreateJobObject.Call(0, 0)
		if handle == 0 {
			killJobErr = fmt.Errorf("CreateJobObject: %w", err)
			return
		}
		info := extendedLimitInformation{}
		info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
		r, _, err := procSetJobInfo.Call(
			handle,
			uintptr(jobObjectExtendedLimitInformation),
			uintptr(unsafe.Pointer(&info)),
			unsafe.Sizeof(info),
		)
		if r == 0 {
			killJobErr = fmt.Errorf("SetInformationJobObject(size=%d): %w", unsafe.Sizeof(info), err)
			return
		}
		killJob = syscall.Handle(handle)
	})
	return killJobErr
}

// assignChildToKillJob puts the freshly spawned goose process (and its
// future children) into the kill-on-close job.
func assignChildToKillJob(pid int) {
	if err := ensureKillJob(); err != nil || killJob == 0 {
		log.Printf("[serve] kill-job unavailable (graceful cleanup only): %v", err)
		return
	}
	proc, err := syscall.OpenProcess(processSetQuotaTerminateAccess, false, uint32(pid))
	if err != nil {
		log.Printf("[serve] OpenProcess(%d) for job assign failed: %v", pid, err)
		return
	}
	defer syscall.CloseHandle(proc)
	if r, _, err := procAssignJob.Call(uintptr(killJob), uintptr(proc), 0); r == 0 {
		log.Printf("[serve] AssignProcessToJobObject(%d) failed: %v", pid, err)
	}
}
