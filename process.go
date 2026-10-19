//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// pidsForExe returns all running processes whose image path equals exePath.
// Matching by full path (not just name) lets two copies of the same browser
// in different folders be routed to different outputs.
func pidsForExe(exePath string) []uint32 {
	want := strings.ToLower(filepath.Clean(exePath))
	base := strings.ToLower(filepath.Base(exePath))

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var pids []uint32
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.ToLower(windows.UTF16ToString(pe.ExeFile[:])) != base {
			continue
		}
		if strings.ToLower(filepath.Clean(processPath(pe.ProcessID))) == want {
			pids = append(pids, pe.ProcessID)
		}
	}
	return pids
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
