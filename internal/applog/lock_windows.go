//go:build windows

package applog

import "golang.org/x/sys/windows"

// crossProcessLock is a named mutex shared by all programs writing the log.
type crossProcessLock struct{ h windows.Handle }

func newLock(name string) crossProcessLock {
	n, _ := windows.UTF16PtrFromString(name)
	h, _ := windows.CreateMutex(nil, false, n)
	return crossProcessLock{h}
}

func (l crossProcessLock) Lock() {
	if l.h != 0 {
		windows.WaitForSingleObject(l.h, 2000)
	}
}

func (l crossProcessLock) Unlock() {
	if l.h != 0 {
		windows.ReleaseMutex(l.h)
	}
}
