//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner launches the browsers and keeps each one routed to its output.
type Runner struct {
	cfg  *Config
	logf func(format string, args ...any)

	mu      sync.Mutex
	routed  map[uint32]string    // pid -> device ID already applied
	volSet  map[string]time.Time // browser path -> when its volume was last applied
	stopped chan struct{}
	restart bool // relaunch closed browsers
}

// Volume is re-applied this often, so audio sessions that start later
// (e.g. when the page begins playing) get it too.
const volumeEvery = 30 * time.Second

func NewRunner(cfg *Config, logf func(string, ...any)) *Runner {
	return &Runner{cfg: cfg, logf: logf, routed: map[uint32]string{}, volSet: map[string]time.Time{}, stopped: make(chan struct{})}
}

// SetVolume changes the volume for a browser while running; it is applied on the next pass.
func (r *Runner) SetVolume(browser string, vol int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, o := range r.cfg.Outputs {
		if samePath(o.Browser, browser) {
			r.cfg.Outputs[i].Volume = &vol
		}
	}
	delete(r.volSet, strings.ToLower(filepath.Clean(browser)))
}

func (r *Runner) enabled() []Output {
	r.mu.Lock()
	defer r.mu.Unlock()
	var outs []Output
	for _, o := range r.cfg.Outputs {
		if o.Enabled && o.Browser != "" {
			outs = append(outs, o)
		}
	}
	return outs
}

// Check returns human-readable problems with the configuration.
func (r *Runner) Check() []string {
	var problems []string
	if _, err := os.Stat(r.cfg.svcl()); err != nil {
		problems = append(problems, "svcl.exe not found at "+r.cfg.svcl()+" (download SoundVolumeCommandLine from nirsoft.net)")
	}
	devices, _ := ListRenderDevices()
	seen := map[string]string{}
	for _, o := range r.enabled() {
		if _, err := os.Stat(o.Browser); err != nil {
			problems = append(problems, fmt.Sprintf("%s: browser not found: %s", o.Name, o.Browser))
		}
		if o.DeviceID == "" {
			// optional: plays on the Windows default output
		} else if d, ok := FindDevice(devices, o.DeviceID); !ok {
			problems = append(problems, fmt.Sprintf("%s: device ID %q is unknown to Windows", o.Name, o.DeviceID))
		} else if d.State != deviceActive {
			problems = append(problems, fmt.Sprintf("%s: device %q is %s", o.Name, d.Label(), stateText(d.State)))
		}
		key := strings.ToLower(filepath.Clean(o.Browser))
		if other, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf("%s and %s use the same browser .exe - Windows routes audio per .exe, so both will play on the same output. Use a different browser or a copy in another folder.", other, o.Name))
		}
		seen[key] = o.Name
	}
	return problems
}

// WaitForDevices blocks until every configured device is active or the timeout passes.
func (r *Runner) WaitForDevices(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		devices, _ := ActiveDevices()
		var missing []string
		for _, o := range r.enabled() {
			if o.DeviceID == "" {
				continue
			}
			if _, ok := FindDevice(devices, o.DeviceID); !ok {
				missing = append(missing, o.Name)
			}
		}
		if len(missing) == 0 {
			r.logf("All audio devices are ready")
			return
		}
		if time.Now().After(deadline) {
			r.logf("Gave up waiting for devices: %s - starting anyway", strings.Join(missing, ", "))
			return
		}
		time.Sleep(3 * time.Second)
	}
}

func (r *Runner) Launch(o Output) error {
	args := splitArgs(o.Args)
	if o.URL != "" {
		args = append(args, o.URL)
	}
	cmd := exec.Command(o.Browser, args...)
	cmd.Dir = filepath.Dir(o.Browser)
	if err := cmd.Start(); err != nil {
		return err
	}
	r.logf("%s: started %s (pid %d)", o.Name, filepath.Base(o.Browser), cmd.Process.Pid)
	go cmd.Wait() // reap; browsers often hand off to another process and exit
	return nil
}

func (r *Runner) LaunchAll() {
	for _, o := range r.enabled() {
		if len(pidsForExe(o.Browser)) > 0 {
			r.logf("%s: %s is already running", o.Name, filepath.Base(o.Browser))
			continue
		}
		if err := r.Launch(o); err != nil {
			r.logf("%s: failed to start browser: %v", o.Name, err)
		}
	}
}

// Enforce routes every new process of each browser to its device. Browsers
// spawn helper processes later (Chrome's audio service starts when sound
// first plays), so this runs periodically rather than once.
func (r *Runner) Enforce(restart bool) {
	for {
		// Devices are handled in parallel: each svcl call is a separate
		// process, so several outputs are routed at the same time.
		var wg sync.WaitGroup
		for _, o := range r.enabled() {
			wg.Add(1)
			go func(o Output) {
				defer wg.Done()
				defer recoverGoroutine("routing " + o.Name)
				r.enforceOne(o, restart)
			}(o)
		}
		wg.Wait()
		select {
		case <-r.stopped:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (r *Runner) enforceOne(o Output, restart bool) {
	pids := pidsForExe(o.Browser)
	if len(pids) == 0 {
		if restart {
			r.logf("%s: browser is not running, restarting", o.Name)
			if err := r.Launch(o); err != nil {
				r.logf("%s: restart failed: %v", o.Name, err)
			}
		}
		return
	}
	// No device: reset to the Windows default, since Windows remembers
	// earlier per-app routing for this .exe.
	id := NormalizeID(o.DeviceID)
	if id == "" {
		id = "DefaultRenderDevice"
	}
	for _, pid := range pids {
		r.mu.Lock()
		done := r.routed[pid] == id
		r.mu.Unlock()
		if done {
			continue
		}
		out, err := setAppDevice(r.cfg.svcl(), id, pid)
		if err != nil {
			r.logf("%s: routing pid %d failed: %v %s", o.Name, pid, err, out)
			continue
		}
		r.mu.Lock()
		r.routed[pid] = id
		r.mu.Unlock()
		r.logf("%s: pid %d -> %s", o.Name, pid, id)
	}

	key := strings.ToLower(filepath.Clean(o.Browser))
	r.mu.Lock()
	due := time.Since(r.volSet[key]) >= volumeEvery
	r.mu.Unlock()
	if due {
		applyVolume(r.cfg.svcl(), volumeTargets(r.enabled(), o.Browser), o.VolumePercent())
		r.mu.Lock()
		r.volSet[key] = time.Now()
		r.mu.Unlock()
	}
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// volumeTargets says what to pass to svcl /SetVolume for a browser: its exe
// name (one call covers every audio session of that browser) unless another
// device uses an exe with the same name, then each process ID.
func volumeTargets(outputs []Output, browser string) []string {
	pids := pidsForExe(browser)
	if len(pids) == 0 {
		return nil
	}
	base := filepath.Base(browser)
	for _, o := range outputs {
		if o.Enabled && !samePath(o.Browser, browser) && strings.EqualFold(filepath.Base(o.Browser), base) {
			targets := make([]string, len(pids))
			for i, pid := range pids {
				targets[i] = strconv.FormatUint(uint64(pid), 10)
			}
			return targets
		}
	}
	return []string{base}
}

// applyVolume sets the per-application volume (Windows volume mixer) and
// returns how many svcl calls succeeded.
func applyVolume(svcl string, targets []string, vol int) int {
	ok := 0
	for _, t := range targets {
		if hiddenCommand(svcl, "/SetVolume", t, strconv.Itoa(vol)).Run() == nil {
			ok++
		}
	}
	return ok
}

func (r *Runner) Stop() {
	select {
	case <-r.stopped:
	default:
		close(r.stopped)
	}
}
