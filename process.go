//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// procCache holds one process snapshot (exe path -> PIDs) shared by the
// runner, the sound meter and the GUI, refreshed at most once a second.
var procCache struct {
	sync.Mutex
	at     time.Time
	byPath map[string][]uint32
	paths  map[uint32]string // PID -> exe path; paths never change for a PID
}

func pathKey(p string) string { return strings.ToLower(filepath.Clean(p)) }

// processesByPath maps lower-case exe path -> running PIDs.
func processesByPath() map[string][]uint32 {
	procCache.Lock()
	defer procCache.Unlock()
	if time.Since(procCache.at) < time.Second && procCache.byPath != nil {
		return procCache.byPath
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return procCache.byPath
	}
	defer windows.CloseHandle(snap)

	if procCache.paths == nil {
		procCache.paths = map[uint32]string{}
	}
	alive := map[uint32]bool{}
	byPath := map[string][]uint32{}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		pid := pe.ProcessID
		alive[pid] = true
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		path, ok := procCache.paths[pid]
		if !ok || (path != "." && filepath.Base(path) != name) { // new, or PID reused by another program
			path = pathKey(processPath(pid))
			procCache.paths[pid] = path
		}
		if path != "" && path != "." {
			byPath[path] = append(byPath[path], pid)
		}
	}
	for pid := range procCache.paths {
		if !alive[pid] {
			delete(procCache.paths, pid)
		}
	}
	procCache.byPath, procCache.at = byPath, time.Now()
	return byPath
}

// pidsForExe returns all running processes whose image path equals exePath.
// Matching by full path (not just name) lets two copies of the same browser
// in different folders be routed to different outputs.
func pidsForExe(exePath string) []uint32 {
	return processesByPath()[pathKey(exePath)]
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

// setAppDevice routes one process to an output via NirSoft svcl.exe.
// Windows persists the choice per executable, so it survives reboots.
func setAppDevice(svcl, deviceID string, pid uint32) (string, error) {
	out, err := hiddenCommand(svcl, "/SetAppDefault", deviceID, "all", strconv.FormatUint(uint64(pid), 10)).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// splitArgs splits a command line into arguments, honoring double quotes.
func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			have = true
		case (r == ' ' || r == '\t') && !inQuote:
			if have {
				args = append(args, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		args = append(args, cur.String())
	}
	return args
}
