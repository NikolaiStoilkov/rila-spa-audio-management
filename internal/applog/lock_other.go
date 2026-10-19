//go:build !windows

package applog

// Non-Windows builds (tests) only need in-process locking.
type crossProcessLock struct{}

func newLock(string) crossProcessLock { return crossProcessLock{} }
func (crossProcessLock) Lock()        {}
func (crossProcessLock) Unlock()      {}
