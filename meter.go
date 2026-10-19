//go:build windows

package main

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"rila-spa-audio/internal/applog"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// soundLevel is what one browser (all its processes) is playing right now.
type soundLevel struct {
	Peak    float32 // 0..1, loudest audio session of the browser
	Session bool    // the browser has an audio session (may be silent)
}

// soundMeter reads the Windows audio meters (the same as the green bars in
// the volume mixer) on its own thread.
type soundMeter struct {
	mu     sync.Mutex
	levels map[string]soundLevel // lower-case exe path -> level
}

var meter = &soundMeter{levels: map[string]soundLevel{}}

func (m *soundMeter) Level(browser string) soundLevel {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.levels[pathKey(browser)]
}

const (
	meterEvery       = 100 * time.Millisecond
	meterDeviceEvery = 5 * time.Second // re-check which outputs exist
)

// run polls all audio sessions on every active output. COM objects are
// created on this locked thread and released after each pass.
func (m *soundMeter) run() {
	defer recoverGoroutine("sound meter")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		audit("error", applog.EventApp, "Sound meter unavailable: "+err.Error())
		return
	}
	defer ole.CoUninitialize()

	unk, err := ole.CreateInstance(wca.CLSID_MMDeviceEnumerator, wca.IID_IMMDeviceEnumerator)
	if err != nil {
		audit("error", applog.EventApp, "Sound meter unavailable: "+err.Error())
		return
	}
	enum := (*wca.IMMDeviceEnumerator)(unsafe.Pointer(unk))
	defer enum.Release()

	var managers []*wca.IAudioSessionManager2
	release := func() {
		for _, mgr := range managers {
			mgr.Release()
		}
		managers = nil
	}
	defer release()

	var refreshed time.Time
	for range time.Tick(meterEvery) {
		if time.Since(refreshed) > meterDeviceEvery {
			release()
			managers = sessionManagers(enum)
			refreshed = time.Now()
		}
		peaks := map[uint32]float32{}
		for _, mgr := range managers {
			readSessions(mgr, peaks)
		}

		levels := map[string]soundLevel{}
		for path, pids := range processesByPath() {
			var l soundLevel
			for _, pid := range pids {
				if p, ok := peaks[pid]; ok {
					l.Session = true
					l.Peak = max(l.Peak, p)
				}
			}
			if l.Session {
				levels[path] = l
			}
		}
		m.mu.Lock()
		m.levels = levels
		m.mu.Unlock()
	}
}

// sessionManagers returns one session manager per active output.
func sessionManagers(enum *wca.IMMDeviceEnumerator) []*wca.IAudioSessionManager2 {
	var coll *wca.IMMDeviceCollection
	if err := enum.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &coll); err != nil || coll == nil {
		return nil
	}
	defer coll.Release()
	var n uint32
	coll.GetCount(&n)
	var list []*wca.IAudioSessionManager2
	for i := uint32(0); i < n; i++ {
		var dev *wca.IMMDevice
		if err := coll.Item(i, &dev); err != nil || dev == nil {
			continue
		}
		var mgr *wca.IAudioSessionManager2
		if activate(dev, wca.IID_IAudioSessionManager2, unsafe.Pointer(&mgr)) == nil && mgr != nil {
			list = append(list, mgr)
		}
		dev.Release()
	}
	return list
}

// activate calls IMMDevice::Activate. go-wca's version passes the address of
// the CLSCTX flags instead of their value, so it is called directly here.
func activate(dev *wca.IMMDevice, iid *ole.GUID, out unsafe.Pointer) error {
	hr, _, _ := syscall.SyscallN(dev.VTable().Activate,
		uintptr(unsafe.Pointer(dev)),
		uintptr(unsafe.Pointer(iid)),
		uintptr(wca.CLSCTX_ALL),
		0,
		uintptr(out))
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

// readSessions adds each session's peak level to peaks, keyed by process ID.
func readSessions(mgr *wca.IAudioSessionManager2, peaks map[uint32]float32) {
	var sessions *wca.IAudioSessionEnumerator
	if err := mgr.GetSessionEnumerator(&sessions); err != nil || sessions == nil {
		return
	}
	defer sessions.Release()
	var n int
	sessions.GetCount(&n)
	for i := 0; i < n; i++ {
		var ctl *wca.IAudioSessionControl
		if err := sessions.GetSession(i, &ctl); err != nil || ctl == nil {
			continue
		}
		var pid uint32
		var ctl2 *wca.IAudioSessionControl2
		if ctl.PutQueryInterface(wca.IID_IAudioSessionControl2, &ctl2) == nil && ctl2 != nil {
			ctl2.GetProcessId(&pid) // may report a "multi-process" status but still fills pid
			ctl2.Release()
		}
		var peak float32
		var ami *wca.IAudioMeterInformation
		if ctl.PutQueryInterface(wca.IID_IAudioMeterInformation, &ami) == nil && ami != nil {
			ami.GetPeakValue(&peak)
			ami.Release()
		}
		ctl.Release()
		if pid != 0 {
			peaks[pid] = max(peaks[pid], peak)
		}
	}
}
