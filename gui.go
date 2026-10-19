//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	url      *walk.LineEdit
	args     *walk.LineEdit
	desc     *walk.TextEdit
}

type app struct {
	cfg       *Config
	mw        *walk.MainWindow
	tabs      *walk.TabWidget
	plusPage  *walk.TabPage // last tab, "+": clicking it adds an output
	logView   *walk.TextEdit
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
}

const appTitle = "Rila Spa Audio - outputs and browsers"

// runGUI shows the settings window with a system tray icon. Closing the window
// only hides it; the program exits from the tray menu. With background=true
// (Windows startup) it starts hidden in the tray and launches the browsers.
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
					PushButton{Text: "Refresh devices", OnClicked: a.refreshDevices},
					PushButton{Text: "Copy device list", OnClicked: a.copyDeviceList},
					PushButton{Text: "List all devices", OnClicked: a.showDeviceList},
					PushButton{Text: "Rescan browsers", OnClicked: a.rescanBrowsers},
					HSpacer{},
				},
			},
			TabWidget{
				AssignTo:      &a.tabs,
				StretchFactor: 3,
			},
			Label{Text: "Log:"},
			TextEdit{AssignTo: &a.logView, ReadOnly: true, VScroll: true, StretchFactor: 1, MinSize: Size{Height: 80}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					CheckBox{AssignTo: &a.autostart, Text: "Start automatically when Windows starts", OnClicked: a.toggleAutostart},
					HSpacer{},
					PushButton{Text: "Save", OnClicked: func() { a.save(true) }},
					PushButton{Text: "Save && Launch all", OnClicked: a.launchAll},
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

	a.busy = true
	if a.plusPage, err = walk.NewTabPage(); err != nil {
		fatal("Cannot create tab: " + err.Error())
	}
	a.plusPage.SetTitle("  +  ")
	a.plusPage.SetLayout(walk.NewVBoxLayout())
	a.tabs.Pages().Add(a.plusPage)
	for _, o := range cfg.Outputs {
		a.addSlot(o)
	}
	a.tabs.SetCurrentIndex(0)
	a.busy = false
	a.tabs.CurrentIndexChanged().Attach(a.onTabChanged)

	a.autostart.SetChecked(AutostartEnabled())
	a.refreshDevices()
	a.mw.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if !a.quitting {
			*canceled = true
			a.hideToTray()
		}
	})
	if err := a.setupTray(); err != nil {
		a.logf("Cannot create tray icon: %v", err)
	}
	defer a.tray.Dispose()

	if background {
		a.logf("=== Started in background (Windows startup) ===")
		go func() {
			r := NewRunner(cfg.Clone(), a.logf)
			for _, p := range r.Check() {
				a.logf("WARNING: %s", p)
			}
			r.WaitForDevices(time.Duration(cfg.DeviceWaitSec) * time.Second)
			r.LaunchAll()
			a.mw.Synchronize(func() { a.startRunner(cfg.RestartIfClosed) })
		}()
	} else {
		a.mw.Synchronize(a.showWindow)
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
		act.Triggered().Attach(onClick)
		ni.ContextMenu().Actions().Add(act)
	}
	addAction("Open", a.showWindow)
	addAction("Launch all browsers", func() {
		r := a.startRunner(a.cfg.RestartIfClosed)
		go r.LaunchAll()
	})
	addAction("Open log viewer", a.openLogViewer)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	addAction("Quit the program", a.quit)

	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.showWindow()
		}
	})
	return ni.SetVisible(true)
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
	a.mw.Hide()
	if !a.trayTip && a.tray != nil {
		a.trayTip = true
		a.tray.ShowInfo("Rila Spa Audio", "Still running in the background. Right-click the tray icon and choose \"Quit the program\" to exit.")
	}
}

func (a *app) quit() {
	if walk.MsgBox(a.mw, "Quit", "Quit Rila Spa Audio?\n\nBrowsers keep playing, but closed browsers will no longer be restarted.", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	a.logf("Quit from tray menu")
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
	if a.busy || a.tabs.CurrentIndex() != a.tabs.Pages().Index(a.plusPage) {
		return
	}
	// Defer: changing tabs from inside the change handler confuses the control.
	a.mw.Synchronize(func() {
		a.busy = true
		defer func() { a.busy = false }()
		a.readFromUI()
		s := a.addSlot(a.newOutput())
		a.tabs.SetCurrentIndex(a.tabs.Pages().Index(s.page))
		a.updateStatus(s)
		s.name.SetFocus()
	})
}

// newOutput suggests a browser that no other output uses yet.
func (a *app) newOutput() Output {
	o := Output{Enabled: true, Name: fmt.Sprintf("Device %d", len(a.slots)+1)}
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
					if idx < 0 || idx >= len(a.devices) {
						return
					}
					s.deviceID.SetText(a.devices[idx].ID)
				},
			},

			Label{Text: "Device ID:"},
			LineEdit{
				AssignTo:      &s.deviceID,
				ReadOnly:      true,
				OnTextChanged: func() { a.updateStatus(s) },
			},
			PushButton{Text: "Clear", OnClicked: func() {
				s.detected.SetCurrentIndex(-1)
				s.deviceID.SetText("")
			}},

			Label{Text: ""},
			Label{AssignTo: &s.status, ColumnSpan: 2},

			Label{Text: "Browser:"},
			ComboBox{AssignTo: &s.browser},
			PushButton{Text: "Browse...", OnClicked: func() { a.browse(s) }},

			Label{Text: "URL:"},
			LineEdit{AssignTo: &s.url, ColumnSpan: 2},

			Label{Text: "Extra args:"},
			LineEdit{AssignTo: &s.args, ColumnSpan: 2},

			Label{Text: "Description:", Alignment: AlignHNearVNear},
			TextEdit{AssignTo: &s.desc, ColumnSpan: 2, VScroll: true, MinSize: Size{Height: 70}, MaxSize: Size{Height: 140}},

			Label{Text: ""},
			Composite{
				Layout:     HBox{MarginsZero: true},
				ColumnSpan: 2,
				Children: []Widget{
					PushButton{Text: "Test this output", OnClicked: func() { a.testOne(s) }},
					HSpacer{},
					PushButton{Text: "Remove this output", OnClicked: func() { a.removeSlot(s) }},
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
	s.url.SetText(o.URL)
	s.args.SetText(o.Args)
	s.desc.SetText(strings.ReplaceAll(o.Description, "\n", "\r\n"))
	s.detected.SetModel(a.deviceLabels())

	a.slots = append(a.slots, s)
	a.selectBrowser(s, o.Browser)
	a.tabs.Pages().Insert(a.tabs.Pages().Index(a.plusPage), page)
	page.SetTitle(tabTitle(len(a.slots)-1, o.Name))
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
	if walk.MsgBox(a.mw, "Remove output", msg, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	a.busy = true
	defer func() { a.busy = false }()
	a.slots = append(a.slots[:i], a.slots[i+1:]...)
	a.tabs.Pages().Remove(s.page)
	s.page.Dispose()
	a.renumberTabs()
	if i >= len(a.slots) {
		i = len(a.slots) - 1
	}
	a.tabs.SetCurrentIndex(i)
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
			URL:         strings.TrimSpace(s.url.Text()),
			Args:        strings.TrimSpace(s.args.Text()),
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
		s.detected.SetModel(labels)
		s.detected.SetCurrentIndex(-1)
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
		a.selectBrowser(s, dlg.FilePath)
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
	if a.runner != nil {
		a.startRunner(a.runner.restart)
	}
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
func (a *app) startRunner(restart bool) *Runner {
	if a.runner != nil {
		a.runner.Stop()
	}
	a.runner = NewRunner(a.cfg.Clone(), a.logf)
	a.runner.restart = restart
	go a.runner.Enforce(restart)
	return a.runner
}

func (a *app) launchAll() {
	if !a.save(false) {
		return
	}
	if problems := NewRunner(a.cfg, a.logf).Check(); len(problems) > 0 {
		msg := strings.Join(problems, "\n\n") + "\n\nLaunch anyway?"
		if walk.MsgBox(a.mw, "Check configuration", msg, walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
			return
		}
	}
	r := a.startRunner(a.cfg.RestartIfClosed)
	go r.LaunchAll()
}

func (a *app) testOne(s *slotUI) {
	if !a.save(false) {
		return
	}
	o := a.cfg.Outputs[a.slotIndex(s)]
	if o.Browser == "" {
		a.logf("%s: no browser set", o.Name)
		return
	}
	r := a.startRunner(a.runner != nil && a.runner.restart)
	go func() {
		if len(pidsForExe(o.Browser)) == 0 {
			if err := r.Launch(o); err != nil {
				a.logf("%s: %v", o.Name, err)
			}
		}
	}()
}

func (a *app) toggleAutostart() {
	on := a.autostart.Checked()
	if err := SetAutostart(on); err != nil {
		a.logf("Autostart error: %v", err)
		a.autostart.SetChecked(!on)
		return
	}
	if on {
		a.logf("Autostart enabled: runs \"spa-audio.exe --run\" at logon")
	} else {
		a.logf("Autostart disabled")
	}
}

// logf is safe to call from any goroutine.
func (a *app) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	log.Println(line)
	if a.mw == nil {
		return
	}
	a.mw.Synchronize(func() {
		a.logView.AppendText(time.Now().Format("15:04:05 ") + line + "\r\n")
	})
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
			a.logf("Copied hardware ID of %s", d.Label())
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
	dlg.Run()
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
