//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"rila-spa-audio/internal/applog"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const appVersion = "1.1.0"

// auditLog is the shared XML log (spa-audio-log.xml).
var auditLog *applog.Logger

func audit(level, event, msg string, kv ...string) {
	if auditLog != nil {
		auditLog.Log(level, event, msg, kv...)
	}
}

// ---------- session: start, heartbeat, clean end, crash detection ----------

// sessionState is written next to the exe while the program runs and removed
// on a clean exit. Finding it at start means the previous run did not end
// cleanly (killed in Task Manager, crash, power loss, forced shutdown).
type sessionState struct {
	ID    string    `json:"id"`
	PID   int       `json:"pid"`
	Mode  string    `json:"mode"`
	Start time.Time `json:"start"`
	Last  time.Time `json:"last"` // heartbeat, updated every minute
}

var (
	sessionStart time.Time
	sessionEnded sync.Once
)

func sessionPath() string { return filepath.Join(exeDir(), "spa-audio.session") }

func writeSessionState(mode string) {
	st := sessionState{ID: auditLog.Session(), PID: os.Getpid(), Mode: mode, Start: sessionStart, Last: time.Now()}
	if data, err := json.Marshal(st); err == nil {
		os.WriteFile(sessionPath(), data, 0o644)
	}
}

func bootTime() time.Time {
	return time.Now().Add(-time.Duration(windows.DurationSinceBoot()))
}

func checkPreviousSession() {
	data, err := os.ReadFile(sessionPath())
	if err != nil {
		return
	}
	var st sessionState
	if json.Unmarshal(data, &st) != nil {
		return
	}
	cause := "the program was closed unexpectedly (ended in Task Manager or crashed)"
	if bootTime().After(st.Last) {
		cause = "the computer was restarted or lost power while the program was running"
	}
	audit("error", applog.EventApp, "Previous run did not end normally: "+cause,
		"previous_session", st.ID,
		"previous_mode", st.Mode,
		"started", st.Start.Format(time.DateTime),
		"last_seen_alive", st.Last.Format(time.DateTime),
		"ran_for", roundDur(st.Last.Sub(st.Start)),
		"windows_boot", bootTime().Format(time.DateTime))
}

func startSession(mode string, cfg *Config) {
	sessionStart = time.Now()
	checkPreviousSession()

	exe, _ := os.Executable()
	host, _ := os.Hostname()
	v := windows.RtlGetVersion()
	devices, _ := ListRenderDevices()
	active := 0
	for _, d := range devices {
		if d.State == deviceActive {
			active++
		}
	}
	how := "started by a user"
	if mode == "run" {
		how = "started automatically with Windows"
	}
	audit("info", applog.EventApp, "Program "+how,
		"version", appVersion,
		"exe", exe,
		"pid", strconv.Itoa(os.Getpid()),
		"computer", host,
		"windows", fmt.Sprintf("%d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber),
		"screen", fmt.Sprintf("%dx%d", win.GetSystemMetrics(win.SM_CXSCREEN), win.GetSystemMetrics(win.SM_CYSCREEN)),
		"windows_boot", bootTime().Format(time.DateTime),
		"audio_outputs", fmt.Sprintf("%d (%d active)", len(devices), active),
		"configured_devices", strconv.Itoa(len(cfg.Outputs)),
		"autostart", strconv.FormatBool(AutostartEnabled()))

	writeSessionState(mode)
	go func() {
		for range time.Tick(time.Minute) {
			writeSessionState(mode)
		}
	}()
}

// endSession logs a clean end once and removes the session marker.
func endSession(reason string, kv ...string) {
	sessionEnded.Do(func() {
		kv = append(kv,
			"reason", reason,
			"ran_for", roundDur(time.Since(sessionStart)),
			"clicks_this_session", strconv.Itoa(stats.sessionTotal()))
		audit("info", applog.EventApp, "Program stopped: "+reason, kv...)
		os.Remove(sessionPath())
	})
}

// logPanics records a crash with its stack trace, then lets it crash.
func logPanics() {
	if r := recover(); r != nil {
		buf := make([]byte, 16<<10)
		buf = buf[:runtime.Stack(buf, false)]
		audit("error", applog.EventApp, fmt.Sprintf("Program crashed: %v", r), "stack", string(buf))
		os.Remove(sessionPath()) // already logged as a crash
		panic(r)
	}
}

func roundDur(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

// ---------- click statistics (per control, all time) ----------

type clickStat struct {
	Count int       `json:"count"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

type clickStats struct {
	mu      sync.Mutex
	Total   map[string]*clickStat `json:"controls"`
	session map[string]int
}

var stats = loadStats()

func statsPath() string { return filepath.Join(exeDir(), "spa-audio-stats.json") }

func loadStats() *clickStats {
	s := &clickStats{Total: map[string]*clickStat{}, session: map[string]int{}}
	if data, err := os.ReadFile(statsPath()); err == nil {
		json.Unmarshal(data, s)
		if s.Total == nil {
			s.Total = map[string]*clickStat{}
		}
	}
	return s
}

// count records one click of control and returns this session's and all-time counts.
func (s *clickStats) count(control string) (session, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	st := s.Total[control]
	if st == nil {
		st = &clickStat{First: now}
		s.Total[control] = st
	}
	st.Count++
	st.Last = now
	s.session[control]++
	if data, err := json.MarshalIndent(s, "", "  "); err == nil {
		os.WriteFile(statsPath(), data, 0o644)
	}
	return s.session[control], st.Count
}

func (s *clickStats) sessionTotal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.session {
		n += c
	}
	return n
}

// ---------- cursor ----------

func cursorFields(hwnd win.HWND) []string {
	var pt win.POINT
	if !win.GetCursorPos(&pt) {
		return nil
	}
	kv := []string{"cursor_screen", fmt.Sprintf("%d,%d", pt.X, pt.Y)}
	if hwnd != 0 {
		cp := pt
		win.ScreenToClient(hwnd, &cp)
		kv = append(kv, "cursor_window", fmt.Sprintf("%d,%d", cp.X, cp.Y))
	}
	return kv
}

// ---------- mouse hook: every click inside this program ----------

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procSetWindowsHookEx = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx   = user32.NewProc("CallNextHookEx")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
)

const whMouse = 7

type mouseHookStruct struct {
	Pt          win.POINT
	Hwnd        win.HWND
	HitTestCode uint32
	DwExtraInfo uintptr
}

var mouseButtons = map[uintptr]string{
	0x201: "left click", 0x203: "left double-click",
	0x204: "right click", 0x206: "right double-click",
	0x207: "middle click",
	0xA1:  "left click", 0xA3: "left double-click", 0xA4: "right click", // title bar / borders
}

var hitAreas = map[uint32]string{
	2: "title bar", 3: "window menu", 8: "minimize button", 9: "maximize button",
	20: "close button (X)", 10: "left border", 11: "right border", 12: "top border", 15: "bottom border",
}

// installMouseHook records mouse clicks in this program's windows (this
// thread only - not other programs).
func installMouseHook(describe func(win.HWND) string, top func() win.HWND) {
	cb := syscall.NewCallback(func(code, wParam uintptr, m *mouseHookStruct) uintptr {
		if int32(code) >= 0 && m != nil {
			if button, ok := mouseButtons[wParam]; ok {
				pt, hit := m.Pt, m.HitTestCode
				control := describe(m.Hwnd)
				if area, ok := hitAreas[hit]; ok && wParam >= 0xA0 && wParam <= 0xA9 {
					control = area
				}
				cp := pt
				win.ScreenToClient(top(), &cp)
				go audit("info", applog.EventMouse, fmt.Sprintf("Mouse %s on %s", button, control),
					"button", button,
					"control", control,
					"cursor_screen", fmt.Sprintf("%d,%d", pt.X, pt.Y),
					"cursor_window", fmt.Sprintf("%d,%d", cp.X, cp.Y))
			}
		}
		r, _, _ := procCallNextHookEx.Call(0, code, wParam, uintptr(unsafe.Pointer(m)))
		return r
	})
	procSetWindowsHookEx.Call(whMouse, cb, 0, uintptr(windows.GetCurrentThreadId()))
}

func windowText(h win.HWND) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func className(h win.HWND) string {
	buf := make([]uint16, 128)
	n, _ := win.GetClassName(h, &buf[0], len(buf))
	return windows.UTF16ToString(buf[:n])
}

// ---------- window messages walk does not expose ----------

var (
	origWndProc  uintptr
	wndProcHooks func(msg uint32, wParam, lParam uintptr)
)

// subclassWindow sees shutdown, sleep, minimize and activation messages
// before passing them on to walk unchanged.
func subclassWindow(h win.HWND, onMsg func(msg uint32, wParam, lParam uintptr)) {
	wndProcHooks = onMsg
	cb := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		wndProcHooks(msg, wParam, lParam)
		return win.CallWindowProc(origWndProc, hwnd, msg, wParam, lParam)
	})
	origWndProc = win.SetWindowLongPtr(h, win.GWLP_WNDPROC, cb)
}

// ---------- settings diff ----------

// diffConfig lists what changed between two saved configurations.
func diffConfig(old, cur *Config) []string {
	var out []string
	label := func(i int, o Output) string {
		if o.Name != "" {
			return o.Name
		}
		return fmt.Sprintf("Device %d", i+1)
	}
	field := func(dev, name, a, b string) {
		if a != b {
			out = append(out, fmt.Sprintf("%s: %s %q -> %q", dev, name, a, b))
		}
	}
	n := max(len(old.Outputs), len(cur.Outputs))
	for i := 0; i < n; i++ {
		switch {
		case i >= len(old.Outputs):
			out = append(out, "Added device: "+label(i, cur.Outputs[i]))
			continue
		case i >= len(cur.Outputs):
			out = append(out, "Removed device: "+label(i, old.Outputs[i]))
			continue
		}
		a, b := old.Outputs[i], cur.Outputs[i]
		dev := label(i, b)
		field(dev, "name", a.Name, b.Name)
		field(dev, "enabled", strconv.FormatBool(a.Enabled), strconv.FormatBool(b.Enabled))
		field(dev, "audio output", a.DeviceID, b.DeviceID)
		field(dev, "browser", a.Browser, b.Browser)
		field(dev, "volume", strconv.Itoa(a.VolumePercent())+"%", strconv.Itoa(b.VolumePercent())+"%")
		field(dev, "URL", a.URL, b.URL)
		field(dev, "extra args", a.Args, b.Args)
		field(dev, "description", a.Description, b.Description)
	}
	return out
}
