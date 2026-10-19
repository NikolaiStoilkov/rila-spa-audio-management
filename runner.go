//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Runner launches the browsers and keeps each one routed to its output.
type Runner struct {
	cfg  *Config
	logf func(format string, args ...any)

	mu      sync.Mutex
	routed  map[uint32]string // pid -> device ID already applied
	stopped chan struct{}
	restart bool // relaunch closed browsers
}

func NewRunner(cfg *Config, logf func(string, ...any)) *Runner {
	return &Runner{cfg: cfg, logf: logf, routed: map[uint32]string{}, stopped: make(chan struct{})}
}

func (r *Runner) enabled() []Output {
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
		for _, o := range r.enabled() {
			r.enforceOne(o, restart)
		}
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
}

func (r *Runner) Stop() {
	select {
	case <-r.stopped:
	default:
		close(r.stopped)
	}
}
