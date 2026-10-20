//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"rila-spa-audio/internal/applog"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

type slotUI struct {
	page     *walk.TabPage
	enabled  *walk.CheckBox
	name     *walk.LineEdit
	deviceID *walk.LineEdit
	detected *walk.ComboBox
	status   *walk.Label
	browser  *walk.ComboBox
	desc     *walk.TextEdit
	volume   *walk.Slider
	volBox   *walk.NumberEdit // exact value, 1% steps
	volSeq   int              // debounce counter for the volume slider
	volLast  int              // last applied volume
	vol      volumeGesture    // current drag/typing, for the volume report trail

	// sound indicator (gui_sound.go)
	level     *walk.ProgressBar
	soundText *walk.Label
	lastLoud  time.Time // last time this browser made sound
	soundOn   bool      // logged state: playing
	lastPct   int
	lastText  string
}

type app struct {
	cfg       *Config
	mw        *walk.MainWindow
	tabs      *walk.TabWidget
	plusPage  *walk.TabPage // last tab, "+": clicking it adds an output
	autostart *walk.CheckBox
	slots     []*slotUI
	devices   []Device  // all known render devices
	browsers  []Browser // installed browsers + custom picks, shared by all tabs
	runner    *Runner
	tray      *walk.NotifyIcon
	busy      bool // ignore tab events while pages are being added/removed
	quitting  bool // true only after "Quit the program" in the tray menu
	shown     bool // window has been shown at least once
	trayTip   bool // "still running" balloon already shown

	// audit log state (gui_audit.go)
	saved    *Config                    // settings as last saved
	vals     map[string]string          // last logged value per tab field
	names    map[win.HWND]func() string // control names for the mouse log
	lastSize string                     // last logged window state
	tabHwnd  win.HWND                   // native tab control, for tab icons
}

const appTitle = "Rila Spa Audio - outputs and browsers"

// runGUI shows the settings window with a system tray icon. Closing the window
// only hides it; the program exits from the tray menu. With background=true
// (Windows startup) it starts hidden in the tray. Either way it keeps
// switching the open browsers to their audio outputs; it never starts them.
func runGUI(cfg *Config, background bool) {
	a := &app{cfg: cfg}
	a.devices, _ = ListRenderDevices()
	a.browsers = FindBrowsers()

	err := MainWindow{
		AssignTo: &a.mw,
		Title:    appTitle,
		Visible:  !background,
		MinSize:  Size{Width: 640, Height: 480},
		Size:     Size{Width: 900, Height: 620},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "Refresh devices", OnClicked: a.on("Refresh devices", nil, a.refreshDevices)},
					PushButton{Text: "Copy device list", OnClicked: a.on("Copy device list", nil, a.copyDeviceList)},
					PushButton{Text: "List all devices", OnClicked: a.on("List all devices", nil, a.showDeviceList)},
					PushButton{Text: "Rescan browsers", OnClicked: a.on("Rescan browsers", nil, a.rescanBrowsers)},
					HSpacer{},
				},
			},
			TabWidget{
				AssignTo:      &a.tabs,
				StretchFactor: 3,
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					CheckBox{AssignTo: &a.autostart, Text: "Start automatically when Windows starts", OnClicked: a.on("Start automatically when Windows starts", nil, a.toggleAutostart)},
					HSpacer{},
					PushButton{Text: "Save", OnClicked: a.on("Save", nil, func() { a.save(true) })},
					PushButton{Text: "Save && Apply", OnClicked: a.on("Save & Apply", nil, a.applyAll)},
				},
			},
		},
	}.Create()
	if err != nil {
		fatal("Cannot create window: " + err.Error())
	}

	if icon, err := walk.Resources.Icon("APP"); err == nil {
		a.mw.SetIcon(icon)
	}

	// Device tabs first, "+" last: if "+" exists first, Windows keeps it
	// selected while the other tabs are inserted in front of it.
	a.busy = true
	for _, o := range cfg.Outputs {
		a.addSlot(o)
	}
	if a.plusPage, err = walk.NewTabPage(); err != nil {
		fatal("Cannot create tab: " + err.Error())
	}
	a.plusPage.SetTitle("  +  ")
	a.plusPage.SetLayout(walk.NewVBoxLayout())
	a.tabs.Pages().Add(a.plusPage)
	a.selectTab(a.firstActiveTab())
	a.busy = false
	a.tabs.CurrentIndexChanged().Attach(a.onTabChanged)
	for _, s := range a.slots {
		a.snapshot(s)
	}

	a.autostart.SetChecked(AutostartEnabled())
	a.refreshDevices()
	a.mw.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if !a.quitting {
			*canceled = true
			a.hideToTray()
		}
	})
	a.startAudit()
	a.startSoundDisplay()
	if err := a.setupTray(); err != nil {
		a.logf("Cannot create tray icon: %v", err)
	}
	defer a.tray.Dispose()

	if background {
		a.logf("=== Started in background (Windows startup) ===")
		audit("info", applog.EventWindow, "Window hidden in tray (background start)")
		startCfg := cfg.Clone()
		safeGo("background start", func() {
			r := NewRunner(startCfg, a.logf)
			for _, p := range r.Check() {
				a.logf("WARNING: %s", p)
			}
			r.WaitForDevices(time.Duration(cfg.DeviceWaitSec) * time.Second)
			a.mw.Synchronize(func() { a.startRunner() })
		})
	} else {
		a.startRunner()
		a.mw.Synchronize(func() { a.showWindowFrom("program start") })
	}
	a.mw.Run()
}

func (a *app) setupTray() error {
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return err
	}
	a.tray = ni
	if icon, err := walk.Resources.Icon("APP"); err == nil {
		ni.SetIcon(icon)
	}
	ni.SetToolTip("Rila Spa Audio")

	addAction := func(text string, onClick func()) {
		act := walk.NewAction()
		act.SetText(text)
		act.Triggered().Attach(a.on("Tray menu: "+text, nil, onClick))
		ni.ContextMenu().Actions().Add(act)
	}
	addAction("Open", func() { a.showWindowFrom("tray menu") })
	addAction("Apply audio routing now", func() { a.startRunner() })
	addAction("Open log viewer", a.openLogViewer)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	addAction("Quit the program", a.quit)

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		switch button {
		case walk.LeftButton:
			a.action("Tray icon (left click)", nil)
			a.showWindowFrom("tray icon click")
		case walk.RightButton:
			a.action("Tray icon (right click - menu)", nil)
		}
	})
	return ni.SetVisible(true)
}

func (a *app) showWindowFrom(source string) {
	if !win.IsWindowVisible(a.mw.Handle()) || win.IsIconic(a.mw.Handle()) {
		audit("info", applog.EventWindow, "Window opened from "+source, "source", source)
	}
	a.showWindow()
}

func (a *app) showWindow() {
	h := a.mw.Handle()
	switch {
	case !a.shown:
		// First time: maximized (title bar and taskbar stay, not full screen).
		a.shown = true
		win.ShowWindow(h, win.SW_SHOWMAXIMIZED)
	case win.IsIconic(h):
		win.ShowWindow(h, win.SW_RESTORE)
	default:
		win.ShowWindow(h, win.SW_SHOW)
	}
	win.SetForegroundWindow(h)
}

func (a *app) hideToTray() {
	kv := append([]string{"how", "close button (X) or Alt+F4"}, cursorFields(a.mw.Handle())...)
	if changes := a.unsavedChanges(); len(changes) > 0 {
		kv = append(kv, "unsaved_changes", strings.Join(changes, " | "))
	}
	audit("info", applog.EventWindow, "Window closed - program keeps running in the tray", kv...)
	a.mw.Hide()
	if !a.trayTip && a.tray != nil {
		a.trayTip = true
		a.tray.ShowInfo("Rila Spa Audio", "Still running in the background. Right-click the tray icon and choose \"Quit the program\" to exit.")
	}
}

func (a *app) quit() {
	if !a.ask("Quit", "Quit Rila Spa Audio?\n\nOpen browsers keep playing on their current outputs, but browsers opened after this are no longer switched.", walk.MsgBoxIconQuestion) {
		return
	}
	var kv []string
	if changes := a.unsavedChanges(); len(changes) > 0 {
		kv = append(kv, "unsaved_changes_lost", strings.Join(changes, " | "))
		audit("warning", applog.EventChange, fmt.Sprintf("Quit with %d unsaved change(s)", len(changes)), kv...)
	}
	endSession("Quit the program (tray menu)", kv...)
	a.quitting = true
	if a.runner != nil {
		a.runner.Stop()
	}
	a.mw.Close()
}

func (a *app) openLogViewer() {
	path := filepath.Join(exeDir(), "log-viewer.exe")
	if err := exec.Command(path).Start(); err != nil {
		walk.MsgBox(a.mw, "Log viewer", "Cannot start "+path+":\n"+err.Error(), walk.MsgBoxIconError)
	}
}

func (a *app) onTabChanged() {
	defer recoverUI("tabs")
	if a.busy {
		return
	}
	if i := a.tabs.CurrentIndex(); i >= 0 && i < len(a.slots) {
		audit("info", applog.EventTab, "Opened tab "+a.deviceLabel(a.slots[i]), "tab", a.deviceLabel(a.slots[i]))
		return
	}
	if a.tabs.CurrentIndex() != a.tabs.Pages().Index(a.plusPage) {
		return
	}
	// Defer: changing tabs from inside the change handler confuses the control.
	a.mw.Synchronize(func() {
		a.busy = true
		defer func() { a.busy = false }()
		a.readFromUI()
		s := a.addSlot(a.newOutput())
		a.action("+ (add device)", s, "browser", a.selectedBrowser(s))
		a.selectTab(a.tabs.Pages().Index(s.page))
		a.updateStatus(s)
		s.name.SetFocus()
	})
}

// newOutput suggests a browser that no other output uses yet.
func (a *app) newOutput() Output {
	o := Output{Enabled: true, Name: a.unusedName()}
	used := map[string]bool{}
	for _, x := range a.cfg.Outputs {
		used[strings.ToLower(x.Browser)] = true
	}
	for _, b := range a.browsers {
		if _, err := os.Stat(b.Path); err == nil && !used[strings.ToLower(b.Path)] {
			o.Browser = b.Path
			break
		}
	}
	return o
}

// firstActiveTab is the first enabled device with an active audio output,
// else the first enabled device, else the first tab.
func (a *app) firstActiveTab() int {
	first := -1
	for i, o := range a.cfg.Outputs {
		if !o.Enabled {
			continue
		}
		if first < 0 {
			first = i
		}
		if d, ok := FindDevice(a.devices, o.DeviceID); ok && d.State == deviceActive {
			return i
		}
	}
	if first < 0 {
		first = 0
	}
	return first
}

// selectTab shows tab i. walk skips SetCurrentIndex when it believes i is
// already current, which goes wrong after tabs are inserted in front of the
// current one, so switch away first to force a real change.
func (a *app) selectTab(i int) {
	n := a.tabs.Pages().Len()
	if i < 0 || i >= n {
		return
	}
	busy := a.busy
	a.busy = true
	if a.tabs.CurrentIndex() == i && n > 1 {
		a.tabs.SetCurrentIndex((i + 1) % n)
	}
	a.tabs.SetCurrentIndex(i)
	a.busy = busy
}

// unusedName returns "Device N" with the lowest N no tab is called yet
// (after removing a tab, len+1 could repeat an existing name).
func (a *app) unusedName() string {
	taken := map[string]bool{}
	for _, s := range a.slots {
		taken[strings.ToLower(strings.TrimSpace(s.name.Text()))] = true
	}
	for n := len(a.slots) + 1; ; n++ {
		if name := fmt.Sprintf("Device %d", n); !taken[strings.ToLower(name)] {
			return name
		}
	}
}

func (a *app) slotIndex(s *slotUI) int {
	for i, x := range a.slots {
		if x == s {
			return i
		}
	}
	return -1
}

func tabTitle(i int, name string) string {
	if name = strings.TrimSpace(name); name == "" {
		return fmt.Sprintf("Device %d", i+1)
	}
	return fmt.Sprintf("%d. %s", i+1, name)
}

func (a *app) renumberTabs() {
	for i, s := range a.slots {
		s.page.SetTitle(tabTitle(i, s.name.Text()))
	}
}

// addSlot creates a tab for o just before the "+" tab.
func (a *app) addSlot(o Output) *slotUI {
	s := &slotUI{}
	page, err := walk.NewTabPage()
	if err != nil {
		a.logf("Cannot create tab: %v", err)
		return s
	}
	s.page = page
	page.SetLayout(walk.NewVBoxLayout())

	err = Composite{
		Layout: Grid{Columns: 3},
		Children: []Widget{
			Label{Text: "Enabled:"},
			CheckBox{AssignTo: &s.enabled, ColumnSpan: 2},

			Label{Text: "Device name:"},
			LineEdit{
				AssignTo:   &s.name,
				ColumnSpan: 2,
				OnTextChanged: func() {
					if i := a.slotIndex(s); i >= 0 {
						s.page.SetTitle(tabTitle(i, s.name.Text()))
					}
				},
			},

			Label{Text: "Audio output:"},
			ComboBox{
				AssignTo:   &s.detected,
				ColumnSpan: 2,
				OnCurrentIndexChanged: func() {
					idx := s.detected.CurrentIndex()
					if a.busy || idx < 0 || idx >= len(a.devices) {
						return // a.busy: the list is being filled, not a user choice
					}
					s.deviceID.SetText(a.devices[idx].ID)
					a.audioOutputChosen(s)
				},
			},

			Label{Text: "Device ID:"},
			LineEdit{
				AssignTo:      &s.deviceID,
				ReadOnly:      true,
				OnTextChanged: func() { a.updateStatus(s) },
			},
			PushButton{Text: "Clear", OnClicked: a.on("Clear audio output", s, func() {
				a.withBusy(func() { s.detected.SetCurrentIndex(-1) })
				s.deviceID.SetText("")
				a.audioOutputChosen(s)
			})},

			Label{Text: ""},
			Label{AssignTo: &s.status, ColumnSpan: 2},

			Label{Text: "Browser:"},
			ComboBox{AssignTo: &s.browser},
			PushButton{Text: "Browse...", OnClicked: a.on("Browse... (browser)", s, func() { a.browse(s) })},

			Label{Text: "Volume:"},
			Slider{
				AssignTo:       &s.volume,
				MinValue:       0,
				MaxValue:       100,
				LineSize:       1, // arrow keys and mouse wheel
				PageSize:       1, // clicking the bar, Page Up/Down
				Tracking:       true,
				ToolTipsHidden: true,
				OnValueChanged: func() {
					defer recoverUI("volume slider")
					if s.volBox != nil && int(s.volBox.Value()) != s.volume.Value() {
						s.volBox.SetValue(float64(s.volume.Value()))
					}
					a.volumeChanged(s, "slider")
				},
			},
			NumberEdit{
				AssignTo:           &s.volBox,
				MinValue:           0,
				MaxValue:           100,
				Decimals:           0,
				Increment:          1,
				SpinButtonsVisible: true,
				Suffix:             " %",
				MinSize:            Size{Width: 70},
				OnValueChanged: func() {
					defer recoverUI("volume % box")
					v := int(s.volBox.Value())
					if s.volume != nil && v != s.volume.Value() {
						s.volume.SetValue(v) // SetValue does not raise ValueChanged
						a.volumeChanged(s, "% box")
					}
				},
			},

			Label{Text: "Sound:"},
			Composite{
				Layout:     HBox{MarginsZero: true},
				ColumnSpan: 2,
				Children: []Widget{
					ProgressBar{AssignTo: &s.level, MinValue: 0, MaxValue: 100, MinSize: Size{Width: 200}, MaxSize: Size{Height: 14}, StretchFactor: 2},
					Label{AssignTo: &s.soundText, MinSize: Size{Width: 260}, StretchFactor: 1},
				},
			},

			Label{Text: "Description:", Alignment: AlignHNearVNear},
			TextEdit{AssignTo: &s.desc, ColumnSpan: 2, VScroll: true, MinSize: Size{Height: 70}, MaxSize: Size{Height: 140}},

			Label{Text: ""},
			Composite{
				Layout:     HBox{MarginsZero: true},
				ColumnSpan: 2,
				Children: []Widget{
					PushButton{Text: "Apply to this browser", OnClicked: a.on("Apply to this browser", s, func() { a.applyOne(s) })},
					HSpacer{},
					PushButton{Text: "Remove this output", OnClicked: a.on("Remove this output", s, func() { a.removeSlot(s) })},
				},
			},

			VSpacer{ColumnSpan: 3},
		},
	}.Create(NewBuilder(page))
	if err != nil {
		a.logf("Cannot create tab: %v", err)
	}

	s.enabled.SetChecked(o.Enabled)
	s.name.SetText(o.Name)
	s.deviceID.SetText(o.DeviceID)
	s.browser.SetModel(a.browserLabels())
	s.volume.SetValue(o.VolumePercent())
	s.volLast = o.VolumePercent()
	s.volBox.SetValue(float64(o.VolumePercent()))
	s.desc.SetText(strings.ReplaceAll(o.Description, "\n", "\r\n"))
	a.fillDetected(s, a.deviceLabels())

	a.slots = append(a.slots, s)
	if len(a.cfg.Outputs) < len(a.slots) {
		// A tab added with "+": add it to the settings right away, so code
		// that looks up this tab's settings (volume, audio output) finds them.
		a.cfg.Outputs = append(a.cfg.Outputs, o)
	}
	a.selectBrowser(s, o.Browser)
	a.tabs.Pages().Insert(len(a.slots)-1, page) // just before "+"
	page.SetTitle(tabTitle(len(a.slots)-1, o.Name))
	a.trackSlot(s)
	a.snapshot(s)
	return s
}

func (a *app) removeSlot(s *slotUI) {
	if len(a.slots) == 1 {
		walk.MsgBox(a.mw, "Remove output", "At least one output is needed.", walk.MsgBoxIconInformation)
		return
	}
	i := a.slotIndex(s)
	if i < 0 {
		return
	}
	msg := fmt.Sprintf("Remove %q?\n\nClick Save afterwards to keep the change.", tabTitle(i, s.name.Text()))
	if !a.ask("Remove output", msg, walk.MsgBoxIconQuestion) {
		return
	}
	audit("info", applog.EventChange, "Removed device "+tabTitle(i, s.name.Text())+" (not saved yet)", "device", tabTitle(i, s.name.Text()))
	a.busy = true
	defer func() { a.busy = false }()
	a.slots = append(a.slots[:i], a.slots[i+1:]...)
	a.tabs.Pages().Remove(s.page)
	s.page.Dispose()
	a.renumberTabs()
	if i >= len(a.slots) {
		i = len(a.slots) - 1
	}
	a.selectTab(i)
	a.readFromUI()
}

func (a *app) readFromUI() {
	a.cfg.Outputs = a.cfg.Outputs[:0]
	for _, s := range a.slots {
		a.cfg.Outputs = append(a.cfg.Outputs, Output{
			Enabled:     s.enabled.Checked(),
			Name:        strings.TrimSpace(s.name.Text()),
			DeviceID:    NormalizeID(s.deviceID.Text()),
			Browser:     a.selectedBrowser(s),
			Volume:      intPtr(s.volume.Value()),
			Description: strings.TrimSpace(strings.ReplaceAll(s.desc.Text(), "\r\n", "\n")),
		})
	}
}

func (a *app) deviceLabels() []string {
	labels := make([]string, len(a.devices))
	for i, d := range a.devices {
		labels[i] = fmt.Sprintf("%s  [%s]", d.Label(), stateText(d.State))
	}
	return labels
}

func (a *app) refreshDevices() {
	devices, err := ListRenderDevices()
	if err != nil {
		a.logf("Cannot read audio devices: %v", err)
		return
	}
	a.devices = devices
	labels := a.deviceLabels()
	for _, s := range a.slots {
		a.fillDetected(s, labels)
		a.updateStatus(s)
	}
	active := 0
	for _, d := range devices {
		if d.State == deviceActive {
			active++
		}
	}
	a.logf("Found %d output devices (%d active)", len(devices), active)
}

func (a *app) updateStatus(s *slotUI) {
	if s.status == nil {
		return
	}
	raw := strings.TrimSpace(s.deviceID.Text())
	switch d, ok := FindDevice(a.devices, raw); {
	case raw == "":
		s.status.SetText("No audio output selected - plays on the Windows default output")
		s.status.SetTextColor(walk.RGB(120, 120, 120))
	case !ok:
		s.status.SetText("Unknown ID - Windows has no output with this ID")
		s.status.SetTextColor(walk.RGB(200, 0, 0))
	case d.State != deviceActive:
		s.status.SetText(d.Label() + " - " + stateText(d.State))
		s.status.SetTextColor(walk.RGB(200, 120, 0))
	default:
		s.status.SetText("OK: " + d.Label())
		s.status.SetTextColor(walk.RGB(0, 140, 0))
	}
}

func (a *app) browse(s *slotUI) {
	dlg := walk.FileDialog{Title: "Choose browser", Filter: "Programs (*.exe)|*.exe", FilePath: a.selectedBrowser(s)}
	if ok, _ := dlg.ShowOpen(a.mw); ok {
		audit("info", applog.EventAction, "Chose browser file", "device", a.deviceLabel(s), "file", dlg.FilePath)
		a.selectBrowser(s, dlg.FilePath)
	} else {
		audit("info", applog.EventAction, "Browse for browser cancelled", "device", a.deviceLabel(s))
	}
}

func (a *app) copyDeviceList() {
	var b strings.Builder
	for _, d := range a.devices {
		fmt.Fprintf(&b, "%s\t%s\t%s\r\n", d.ID, d.Label(), stateText(d.State))
	}
	if err := walk.Clipboard().SetText(b.String()); err != nil {
		a.logf("Clipboard error: %v", err)
		return
	}
	a.logf("Copied %d devices to clipboard", len(a.devices))
}

func (a *app) save(showProblems bool) bool {
	a.readFromUI()
	if err := a.cfg.Save(); err != nil {
		walk.MsgBox(a.mw, "Save failed", err.Error()+"\n\nPut the program in a writable folder, e.g. C:\\SpaAudio.", walk.MsgBoxIconError)
		return false
	}
	a.logf("Saved %s", configPath())
	a.logSave()
	a.startRunner() // switch with the saved settings
	if a.autostart.Checked() {
		SetAutostart(true) // refresh path in case the .exe was moved
	}
	problems := NewRunner(a.cfg, a.logf).Check()
	for _, p := range problems {
		a.logf("WARNING: %s", p)
	}
	if showProblems && len(problems) > 0 {
		walk.MsgBox(a.mw, "Check configuration", strings.Join(problems, "\n\n"), walk.MsgBoxIconWarning)
	}
	return true
}

// startRunner (re)starts background routing with a snapshot of the config.
// Its first pass runs immediately.
func (a *app) startRunner() *Runner {
	if a.runner != nil {
		a.runner.Stop()
	}
	r := NewRunner(a.cfg.Clone(), a.logf)
	a.runner = r
	safeGo("routing loop", r.Enforce)
	return r
}

// applyAll saves and switches every open browser to its audio output now.
func (a *app) applyAll() {
	if !a.save(false) {
		return
	}
	var lines []string
	for i, o := range a.cfg.Outputs {
		if o.Enabled && o.Browser != "" {
			lines = append(lines, a.applyLine(i, o))
		}
	}
	msg := strings.Join(lines, "\n")
	if problems := NewRunner(a.cfg, a.logf).Check(); len(problems) > 0 {
		msg += "\n\nPlease check:\n- " + strings.Join(problems, "\n- ")
	}
	audit("info", applog.EventRunner, "Audio routing applied", "summary", strings.ReplaceAll(msg, "\n", " | "))
	walk.MsgBox(a.mw, "Audio routing applied", msg, walk.MsgBoxIconInformation)
}

// applyOne saves and switches one tab's browser now.
func (a *app) applyOne(s *slotUI) {
	if !a.save(false) {
		return
	}
	i := a.slotIndex(s)
	o := a.cfg.Outputs[i]
	if o.Browser == "" {
		walk.MsgBox(a.mw, "Apply", "Choose a browser first.", walk.MsgBoxIconInformation)
		return
	}
	msg := a.applyLine(i, o)
	audit("info", applog.EventRunner, "Audio routing applied to one browser", "device", tabTitle(i, o.Name), "summary", msg)
	walk.MsgBox(a.mw, "Audio routing applied", msg, walk.MsgBoxIconInformation)
}

// applyLine describes what happens to one device's browser.
func (a *app) applyLine(i int, o Output) string {
	output := "Windows default output"
	if d, ok := FindDevice(a.devices, o.DeviceID); ok {
		output = d.Label()
	}
	name := tabTitle(i, o.Name)
	if len(pidsForExe(o.Browser)) == 0 {
		return fmt.Sprintf("%s: %s is not open - it will be switched to %s as soon as it is opened.", name, browserName(o.Browser), output)
	}
	return fmt.Sprintf("%s: %s -> %s", name, browserName(o.Browser), output)
}

func (a *app) toggleAutostart() {
	on := a.autostart.Checked()
	if err := SetAutostart(on); err != nil {
		a.logf("Autostart error: %v", err)
		a.autostart.SetChecked(!on)
		return
	}
	audit("info", applog.EventChange, "Autostart "+map[bool]string{true: "enabled", false: "disabled"}[on],
		"field", "Start automatically when Windows starts", "old", strconv.FormatBool(!on), "new", strconv.FormatBool(on), "saved", "yes")
	if on {
		a.logf("Autostart enabled: runs \"spa-audio.exe --run\" at logon")
	} else {
		a.logf("Autostart disabled")
	}
}

// logf writes to the log file only (shown in log-viewer.exe, not in this
// window). Safe to call from any goroutine.
func (a *app) logf(format string, args ...any) {
	audit("", applog.EventRunner, fmt.Sprintf(format, args...))
}

type deviceTable struct {
	walk.TableModelBase
	rows []Device
}

func (m *deviceTable) RowCount() int { return len(m.rows) }

func (m *deviceTable) Value(row, col int) interface{} {
	d := m.rows[row]
	switch col {
	case 0:
		return d.Label()
	case 1:
		return d.ID
	}
	return stateText(d.State)
}

// showDeviceList opens a window with every output Windows knows about.
func (a *app) showDeviceList() {
	a.refreshDevices()
	model := &deviceTable{rows: a.devices}
	var dlg *walk.Dialog
	var table *walk.TableView
	var closeBtn *walk.PushButton

	copySelected := func() {
		i := table.CurrentIndex()
		if i < 0 || i >= len(model.rows) {
			return
		}
		d := model.rows[i]
		if err := walk.Clipboard().SetText(d.ID); err == nil {
			a.action("Copy hardware ID", nil, "output_name", d.Label(), "hardware_id", d.ID)
		}
	}

	err := Dialog{
		AssignTo:     &dlg,
		Title:        "All audio output devices",
		CancelButton: &closeBtn,
		MinSize:      Size{Width: 800, Height: 400},
		Layout:       VBox{},
		Children: []Widget{
			TableView{
				AssignTo:            &table,
				AlternatingRowBG:    true,
				LastColumnStretched: true,
				Columns: []TableViewColumn{
					{Title: "Device name", Width: 320},
					{Title: "Hardware ID", Width: 420},
					{Title: "State"},
				},
				Model:           model,
				OnItemActivated: copySelected,
				StyleCell: func(style *walk.CellStyle) {
					if style.Row() < len(model.rows) && model.rows[style.Row()].State != deviceActive {
						style.TextColor = walk.RGB(130, 130, 130)
					}
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: fmt.Sprintf("%d devices. Double-click a row to copy its hardware ID.", len(model.rows))},
					HSpacer{},
					PushButton{Text: "Copy hardware ID", OnClicked: copySelected},
					PushButton{AssignTo: &closeBtn, Text: "Close", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		a.logf("Cannot open device list: %v", err)
		return
	}
	a.nameControl(table, func() string { return "device list table" })
	opened := time.Now()
	audit("info", applog.EventWindow, "Device list window opened", "devices", strconv.Itoa(len(model.rows)))
	dlg.Run()
	audit("info", applog.EventWindow, "Device list window closed", "open_for", roundDur(time.Since(opened)))
}

func (a *app) browserLabels() []string {
	labels := make([]string, len(a.browsers))
	for i, b := range a.browsers {
		labels[i] = b.Label()
	}
	return labels
}

func (a *app) browserIndex(path string) int {
	for i, b := range a.browsers {
		if strings.EqualFold(filepath.Clean(b.Path), filepath.Clean(path)) {
			return i
		}
	}
	return -1
}

func (a *app) selectedBrowser(s *slotUI) string {
	if i := s.browser.CurrentIndex(); i >= 0 && i < len(a.browsers) {
		return a.browsers[i].Path
	}
	return ""
}

// setBrowserList replaces the dropdown entries in every tab, keeping each selection.
func (a *app) setBrowserList(list []Browser) {
	selected := make([]string, len(a.slots))
	for i, s := range a.slots {
		selected[i] = a.selectedBrowser(s)
	}
	a.browsers = list
	labels := a.browserLabels()
	busy := a.busy
	a.busy = true // refilling the lists is not a user change
	defer func() { a.busy = busy }()
	for i, s := range a.slots {
		s.browser.SetModel(labels)
		s.browser.SetCurrentIndex(a.browserIndex(selected[i]))
	}
}

// selectBrowser selects path in the tab's dropdown, adding it to the list
// first if it is not an installed browser (e.g. a portable copy).
func (a *app) selectBrowser(s *slotUI, path string) {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if path == "" {
		s.browser.SetCurrentIndex(-1)
		return
	}
	if a.browserIndex(path) < 0 {
		name := browserName(path) + " - custom"
		if _, err := os.Stat(path); err != nil {
			name = browserName(path) + " - NOT FOUND"
		}
		a.setBrowserList(append(append([]Browser(nil), a.browsers...), Browser{Name: name, Path: filepath.Clean(path)}))
	}
	s.browser.SetCurrentIndex(a.browserIndex(path))
}

func (a *app) rescanBrowsers() {
	list := FindBrowsers()
	found := map[string]bool{}
	for _, b := range list {
		found[strings.ToLower(b.Path)] = true
	}
	// Keep custom picks that a tab still uses.
	for _, s := range a.slots {
		if p := a.selectedBrowser(s); p != "" && !found[strings.ToLower(p)] {
			list = append(list, a.browsers[a.browserIndex(p)])
			found[strings.ToLower(p)] = true
		}
	}
	a.setBrowserList(list)
	a.logf("Found %d browsers", len(list))
}

func intPtr(v int) *int { return &v }

// volumeChanged applies the slider value to the running browser right away
// (debounced while the slider is being dragged) and to the background runner.
// Every step is checked and reported (event "volume") to catch slider bugs.
func (a *app) volumeChanged(s *slotUI, source string) {
	defer recoverUI("volume slider")
	if a.busy || s.volume == nil {
		return // values set by the program, not the user
	}
	i := a.slotIndex(s)
	if i < 0 || i >= len(a.cfg.Outputs) {
		// The tab and the settings list are out of step - the bug that used to
		// crash here. Report it with a stack trace instead.
		a.volumeBug(s, source, fmt.Sprintf("tab index %d, %d tabs, %d devices in settings", i, len(a.slots), len(a.cfg.Outputs)))
		return
	}
	vol := s.volume.Value()
	if vol < 0 || vol > 100 {
		a.volumeBug(s, source, fmt.Sprintf("value %d is outside 0-100", vol))
		return
	}
	a.cfg.Outputs[i].Volume = intPtr(vol)

	g := &s.vol
	if !g.active {
		*g = volumeGesture{active: true, from: s.volLast, source: source, started: time.Now()}
		audit("info", applog.EventVolume, fmt.Sprintf("%s: volume change started (%s)", a.deviceLabel(s), source),
			"device", a.deviceLabel(s), "from", fmt.Sprintf("%d%%", s.volLast), "source", source,
			"browser", a.selectedBrowser(s), "window", a.windowState())
	}
	g.steps++

	s.volSeq++
	seq := s.volSeq
	time.AfterFunc(300*time.Millisecond, func() {
		a.mw.Synchronize(func() {
			defer recoverUI("volume apply")
			if seq != s.volSeq {
				return // a newer value is pending
			}
			a.applyVolumeNow(s, vol)
		})
	})
}

type volumeGesture struct {
	active  bool
	from    int
	steps   int
	source  string
	started time.Time
}

// applyVolumeNow ends a gesture: saves the value in the runner, sets it on
// the browser and logs a summary of what happened.
func (a *app) applyVolumeNow(s *slotUI, vol int) {
	g := s.vol
	s.vol = volumeGesture{}
	from := s.volLast
	s.volLast = vol
	dev := a.deviceLabel(s)
	browser := a.selectedBrowser(s)
	a.fieldChanged(s, "Volume", fmt.Sprintf("%d%%", vol))

	kv := []string{"device", dev, "from", fmt.Sprintf("%d%%", from), "to", fmt.Sprintf("%d%%", vol),
		"source", g.source, "steps", fmt.Sprint(g.steps), "gesture_took", time.Since(g.started).Round(time.Millisecond).String()}
	if browser == "" {
		audit("warning", applog.EventVolume, dev+": volume set, but no browser is selected - nothing to apply it to", kv...)
		return
	}
	if a.runner != nil {
		a.runner.SetVolume(browser, vol)
	}
	pids := pidsForExe(browser)
	if len(pids) == 0 {
		audit("info", applog.EventVolume, fmt.Sprintf("%s: volume %d%% saved for %s, which is not open - applied when it opens", dev, vol, browserName(browser)),
			append(kv, "browser", browser)...)
		return
	}
	svcl, outputs := a.cfg.svcl(), a.cfg.Clone().Outputs
	safeGo("set volume", func() {
		start := time.Now()
		targets := volumeTargets(outputs, browser)
		ok, failed := applyVolumeDetailed(svcl, targets, vol)
		kv := append(kv, "browser", browser, "processes", fmt.Sprint(len(pids)),
			"svcl_targets", strings.Join(targets, " "), "svcl_ok", fmt.Sprint(ok),
			"svcl_failed", strings.Join(failed, " | "), "apply_took", time.Since(start).Round(time.Millisecond).String())
		switch {
		case len(failed) > 0:
			audit("error", applog.EventVolume, fmt.Sprintf("%s: volume %d%% could not be fully applied", dev, vol), kv...)
		case ok == 0:
			audit("warning", applog.EventVolume, fmt.Sprintf("%s: volume %d%% - no audio session found to apply it to", dev, vol), kv...)
		default:
			audit("info", applog.EventVolume, fmt.Sprintf("%s: volume %d%% -> %d%% applied", dev, from, vol), kv...)
		}
	})
}

// volumeBug reports an impossible slider state with a stack trace in
// log-viewer-crash.txt, instead of crashing.
func (a *app) volumeBug(s *slotUI, source, detail string) {
	msg := "volume " + source + " in an invalid state: " + detail
	audit("error", applog.EventVolume, msg, "device", a.deviceLabel(s))
	if auditLog != nil {
		auditLog.CrashReport(applog.KindRecovered+" (volume "+source+")", msg, string(debug.Stack()), time.Now(), nil)
	}
}

func (a *app) withBusy(f func()) {
	busy := a.busy
	a.busy = true
	defer func() { a.busy = busy }()
	f()
}

// fillDetected refills the Audio output list and selects the saved device,
// so the dropdown always shows what is stored.
func (a *app) fillDetected(s *slotUI, labels []string) {
	a.withBusy(func() {
		s.detected.SetModel(labels)
		idx := -1
		if id := NormalizeID(s.deviceID.Text()); id != "" {
			for i, d := range a.devices {
				if strings.EqualFold(d.ID, id) {
					idx = i
					break
				}
			}
		}
		s.detected.SetCurrentIndex(idx)
	})
}

// audioOutputChosen saves the tab's audio output to config.json right away
// (only that field, so other unsaved edits are not saved with it) and
// routes the running browser to it.
func (a *app) audioOutputChosen(s *slotUI) {
	defer recoverUI("audio output")
	i := a.slotIndex(s)
	if i < 0 || i >= len(a.cfg.Outputs) {
		return
	}
	id := NormalizeID(s.deviceID.Text())
	a.cfg.Outputs[i].DeviceID = id

	saved := false
	if _, err := os.Stat(configPath()); err == nil {
		if disk, err := LoadConfig(); err == nil && i < len(disk.Outputs) {
			disk.Outputs[i].DeviceID = id
			if err := disk.Save(); err == nil {
				saved = true
				if a.saved != nil && i < len(a.saved.Outputs) {
					a.saved.Outputs[i].DeviceID = id
				}
			}
		}
	}
	if !saved {
		// First save, or a device tab that has never been saved: save everything.
		saved = a.save(false)
	}

	label := "Windows default output"
	if d, ok := FindDevice(a.devices, id); ok {
		label = d.Label()
	}
	dev := a.deviceLabel(s)
	if saved {
		audit("info", applog.EventSave, dev+": audio output saved: "+label, "device", dev, "audio_output", label, "hardware_id", id)
	}

	// Use it now: the background runner and the browser if it is running.
	browser := a.selectedBrowser(s)
	if browser == "" {
		return
	}
	if a.runner != nil {
		a.runner.SetDevice(browser, id)
	}
	target := id
	if target == "" {
		target = "DefaultRenderDevice"
	}
	svcl := a.cfg.svcl()
	safeGo("route audio output", func() {
		for _, pid := range pidsForExe(browser) {
			if out, err := setAppDevice(svcl, target, pid); err != nil {
				a.logf("%s: routing pid %d failed: %v %s", dev, pid, err, out)
			}
		}
	})
}
