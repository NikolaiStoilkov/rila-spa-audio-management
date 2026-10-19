//go:build windows

package main

import (
	"fmt"
	"strconv"
	"strings"

	"rila-spa-audio/internal/applog"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// on wraps a button/menu handler so the click is logged and counted first.
// s is the device tab the control belongs to (nil for global controls).
func (a *app) on(control string, s *slotUI, f func()) func() {
	return func() {
		a.action(control, s)
		f()
	}
}

// action logs a click on a named control with click counts and cursor position.
func (a *app) action(control string, s *slotUI, kv ...string) {
	key := control
	device := ""
	if s != nil {
		device = a.deviceLabel(s)
	}
	session, total := stats.count(key)
	fields := append([]string{
		"control", control,
		"device", device,
		"count_session", strconv.Itoa(session),
		"count_total", strconv.Itoa(total),
		"window", a.windowState(),
	}, kv...)
	fields = append(fields, cursorFields(a.mw.Handle())...)
	msg := fmt.Sprintf("Clicked %q", control)
	if device != "" {
		msg += " on " + device
	}
	audit("info", applog.EventAction, msg, fields...)
}

func (a *app) deviceLabel(s *slotUI) string {
	if i := a.slotIndex(s); i >= 0 {
		return tabTitle(i, s.name.Text())
	}
	return "new device"
}

func (a *app) windowState() string {
	h := a.mw.Handle()
	switch {
	case !win.IsWindowVisible(h):
		return "hidden in tray"
	case win.IsIconic(h):
		return "minimized"
	case win.IsZoomed(h):
		return "maximized"
	}
	return "normal"
}

// fieldChanged logs a setting change in a device tab (old -> new). The first
// call for a field only records its starting value.
func (a *app) fieldChanged(s *slotUI, field, value string, kv ...string) {
	if a.vals == nil {
		a.vals = map[string]string{}
	}
	key := fmt.Sprintf("%p/%s", s, field)
	old, seen := a.vals[key]
	a.vals[key] = value
	if !seen || a.busy || old == value || a.slotIndex(s) < 0 {
		return
	}
	device := a.deviceLabel(s)
	audit("info", applog.EventChange, fmt.Sprintf("%s: %s changed", device, field),
		append([]string{"device", device, "field", field, "old", old, "new", value, "saved", savedNow[field]}, kv...)...)
}

// savedNow tells, per field, whether a change is saved immediately.
var savedNow = map[string]string{
	"Audio output": "yes (saved immediately)",
	"Enabled":      "no", "Device name": "no", "Browser": "no", "Volume": "no",
	"URL": "no", "Extra args": "no", "Description": "no",
}

// snapshot records the starting values of a tab so later edits log old -> new.
func (a *app) snapshot(s *slotUI) {
	busy := a.busy
	a.busy = true
	a.fieldChanged(s, "Enabled", strconv.FormatBool(s.enabled.Checked()))
	a.fieldChanged(s, "Device name", s.name.Text())
	a.fieldChanged(s, "Audio output", s.deviceID.Text())
	a.fieldChanged(s, "Browser", a.selectedBrowser(s))
	a.fieldChanged(s, "Volume", fmt.Sprintf("%d%%", s.volume.Value()))
	a.fieldChanged(s, "URL", s.url.Text())
	a.fieldChanged(s, "Extra args", s.args.Text())
	a.fieldChanged(s, "Description", s.desc.Text())
	a.busy = busy
}

// trackSlot logs edits in a tab and names its controls for the mouse log.
func (a *app) trackSlot(s *slotUI) {
	text := func(field string, w *walk.LineEdit) {
		w.EditingFinished().Attach(func() { a.fieldChanged(s, field, w.Text()) })
	}
	text("Device name", s.name)
	text("URL", s.url)
	text("Extra args", s.args)
	s.desc.FocusedChanged().Attach(func() {
		if !s.desc.Focused() {
			a.fieldChanged(s, "Description", s.desc.Text())
		}
	})
	s.enabled.CheckedChanged().Attach(func() {
		a.fieldChanged(s, "Enabled", strconv.FormatBool(s.enabled.Checked()))
	})
	s.browser.CurrentIndexChanged().Attach(func() {
		a.fieldChanged(s, "Browser", a.selectedBrowser(s))
	})
	s.deviceID.TextChanged().Attach(func() {
		label := "Windows default output"
		if d, ok := FindDevice(a.devices, s.deviceID.Text()); ok {
			label = d.Label()
		}
		a.fieldChanged(s, "Audio output", s.deviceID.Text(), "output_name", label)
	})

	dev := func(field string) func() string {
		return func() string { return field + " in " + a.deviceLabel(s) }
	}
	a.nameControl(s.enabled, dev("Enabled checkbox"))
	a.nameControl(s.name, dev("Device name field"))
	a.nameControl(s.detected, dev("Audio output dropdown"))
	a.nameControl(s.deviceID, dev("Device ID field"))
	a.nameControl(s.browser, dev("Browser dropdown"))
	a.nameControl(s.volume, dev("Volume slider"))
	a.nameControl(s.volBox, dev("Volume box"))
	a.nameControl(s.url, dev("URL field"))
	a.nameControl(s.args, dev("Extra args field"))
	a.nameControl(s.desc, dev("Description field"))
	a.nameControl(s.page, func() string { return "tab " + a.deviceLabel(s) })
}

func (a *app) nameControl(w walk.Window, name func() string) {
	if a.names == nil {
		a.names = map[win.HWND]func() string{}
	}
	if w != nil {
		a.names[w.Handle()] = name
	}
}

var friendlyClass = map[string]string{
	"button":            "button",
	"edit":              "text field",
	"combobox":          "dropdown",
	"comboboxex32":      "dropdown",
	"combolbox":         "dropdown list",
	"msctls_trackbar32": "slider",
	"systabcontrol32":   "tabs",
	"syslistview32":     "table",
	"sysheader32":       "table header",
	"msctls_updown32":   "spin buttons",
	"static":            "label",
	"#32768":            "menu",
	"#32770":            "message box",
}

// describeControl names the control under the cursor for the mouse log.
// Text typed into fields is never read (passwords, URLs).
func (a *app) describeControl(h win.HWND) string {
	if f, ok := a.names[h]; ok {
		return f()
	}
	class := className(h)
	desc := friendlyClass[strings.ToLower(class)]
	if desc == "" {
		desc = strings.Trim(strings.TrimPrefix(class, `\o/ Walk_`), `\o/ `)
	}
	if strings.EqualFold(class, "Button") {
		if t := strings.ReplaceAll(windowText(h), "&&", "&"); t != "" {
			desc = fmt.Sprintf("button %q", strings.ReplaceAll(t, "&", ""))
		}
	}
	for p, i := win.GetParent(h), 0; p != 0 && i < 8; p, i = win.GetParent(p), i+1 {
		if f, ok := a.names[p]; ok {
			return desc + " in " + f()
		}
	}
	if top := win.GetAncestor(h, win.GA_ROOT); top != 0 && top != a.mw.Handle() {
		if t := windowText(top); t != "" {
			return fmt.Sprintf("%s in window %q", desc, t)
		}
	}
	return desc
}

// ask shows a Yes/No question and logs the question and the answer.
func (a *app) ask(title, msg string, style walk.MsgBoxStyle) bool {
	res := walk.MsgBox(a.mw, title, msg, walk.MsgBoxYesNo|style)
	answer := "No"
	if res == walk.DlgCmdYes {
		answer = "Yes"
	}
	audit("info", applog.EventAction, fmt.Sprintf("Answered %q to %q", answer, title),
		"question", strings.ReplaceAll(msg, "\n", " "), "answer", answer)
	return res == walk.DlgCmdYes
}

// logSave records what changed since the last save.
func (a *app) logSave() {
	if a.saved == nil {
		a.saved = a.cfg.Clone()
	}
	changes := diffConfig(a.saved, a.cfg)
	a.saved = a.cfg.Clone()
	if len(changes) == 0 {
		audit("info", applog.EventSave, "Settings saved (no changes)", "devices", strconv.Itoa(len(a.cfg.Outputs)))
		return
	}
	kv := []string{"devices", strconv.Itoa(len(a.cfg.Outputs)), "changes", strconv.Itoa(len(changes))}
	for i, c := range changes {
		kv = append(kv, fmt.Sprintf("change_%d", i+1), c)
	}
	audit("info", applog.EventSave, fmt.Sprintf("Settings saved with %d change(s)", len(changes)), kv...)
}

// unsavedChanges compares the window with the last saved settings.
func (a *app) unsavedChanges() []string {
	if a.saved == nil {
		return nil
	}
	a.readFromUI()
	return diffConfig(a.saved, a.cfg)
}

// onWindowMessage logs shutdown, sleep, window state and focus changes.
func (a *app) onWindowMessage(msg uint32, wParam, lParam uintptr) {
	const (
		wmSize            = 0x0005
		wmQueryEndSession = 0x0011
		wmEndSession      = 0x0016
		wmActivateApp     = 0x001C
		wmPowerBroadcast  = 0x0218
		endSessionLogoff  = 0x80000000
		endSessionClose   = 0x00000001
	)
	switch msg {
	case wmQueryEndSession, wmEndSession:
		what := "shutting down or restarting"
		if lParam&endSessionLogoff != 0 {
			what = "logging off the user"
		} else if lParam&endSessionClose != 0 {
			what = "closing programs (update or installer)"
		}
		if msg == wmQueryEndSession {
			audit("warning", applog.EventSystem, "Windows is "+what)
		} else if wParam != 0 {
			a.quitting = true
			endSession("Windows is " + what)
		}
	case wmPowerBroadcast:
		switch wParam {
		case 0x4:
			audit("warning", applog.EventSystem, "Computer is going to sleep")
		case 0x12:
			go audit("info", applog.EventSystem, "Computer woke up from sleep")
		}
	case wmSize:
		state := map[uintptr]string{0: "restored", 1: "minimized", 2: "maximized"}[wParam]
		if state != "" && state != a.lastSize && win.IsWindowVisible(a.mw.Handle()) {
			if a.lastSize != "" {
				go audit("info", applog.EventWindow, "Window "+state, "window", state)
			}
			a.lastSize = state
		}
	case wmActivateApp:
		if win.IsWindowVisible(a.mw.Handle()) {
			what := "Program window lost focus (user switched to another program)"
			if wParam != 0 {
				what = "Program window got focus"
			}
			go audit("info", applog.EventWindow, what)
		}
	}
}

// startAudit hooks the main window and mouse once the window exists.
func (a *app) startAudit() {
	a.saved = a.cfg.Clone()
	a.nameControl(a.tabs, func() string { return "device tabs" })
	a.nameControl(a.autostart, func() string { return "Autostart checkbox" })
	subclassWindow(a.mw.Handle(), a.onWindowMessage)
	installMouseHook(a.describeControl, func() win.HWND { return a.mw.Handle() })
}
