//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
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

// processStartTimeUnix reports (alive, startMatches) for a recorded pid.
// The start-time guard prevents killing an innocent process that recycled
// the PID.
func processMatches(pid int, startUnix int64) (bool, bool) {
	p, err := osFindProcess(pid)
	if err != nil || p == nil {
		return false, false
	}
	cur := processStartTimeUnix(pid)
	if cur == 0 {
		return false, false
	}
	// allow1s slack for filesystem/time rounding
	diff := cur - startUnix
	if diff < 0 {
		diff = -diff
	}
	return true, diff <= 1
}

// processStartTimeUnix: Windows GetProcessTimes via Get-Process equivalent
// (kernel32), seconds since epoch. Returns0 if unavailable.
func processStartTimeUnix(pid int) int64 {
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil || h == 0 {
		return 0
	}
	defer syscall.CloseHandle(h)
	var creation, exit, kernel, user int64
	r, _, _ := procGetProcessTimes.Call(uintptr(h), uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0
	}
	// FILETIME (100ns since1601) → unix seconds
	const epochDiff = 116444736000000000
	return (creation - epochDiff) / 10000000
}

// osFindProcess: on Windows FindProcess fails for a dead pid.
func osFindProcess(pid int) (*os.Process, error) {
	return os.FindProcess(pid)
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
	procGetProcessTimes = k32.NewProc("GetProcessTimes")

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
