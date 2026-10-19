//go:build windows

package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type slotUI struct {
	enabled  *walk.CheckBox
	name     *walk.LineEdit
	deviceID *walk.LineEdit
	detected *walk.ComboBox
	status   *walk.Label
	browser  *walk.LineEdit
	url      *walk.LineEdit
	args     *walk.LineEdit
}

type app struct {
	cfg       *Config
	mw        *walk.MainWindow
	logView   *walk.TextEdit
	autostart *walk.CheckBox
	slots     []*slotUI
	devices   []Device // all known render devices
	runner    *Runner
}

func runGUI(cfg *Config) {
	a := &app{cfg: cfg}
	a.devices, _ = ListRenderDevices()

	var groups []Widget
	for i := range cfg.Outputs {
		s := &slotUI{}
		a.slots = append(a.slots, s)
		groups = append(groups, a.slotGroup(i, s))
	}

	err := MainWindow{
		AssignTo: &a.mw,
		Title:    "Rila Spa Audio - outputs and browsers",
		MinSize:  Size{Width: 600, Height: 450},
		Size:     Size{Width: 1150, Height: 820},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "Refresh devices", OnClicked: a.refreshDevices},
					PushButton{Text: "Copy device list", OnClicked: a.copyDeviceList},
					Label{Text: "Pick a detected device or type/paste the ID manually: {0.0.0.00000000}.{GUID}"},
					HSpacer{},
				},
			},
			ScrollView{
				Layout: VBox{MarginsZero: true},
				Children: []Widget{
					Composite{
						Layout:   Grid{Columns: 2, MarginsZero: true},
						MinSize:  Size{Width: 860},
						Children: groups,
					},
				},
			},
			Label{Text: "Log:"},
			TextEdit{AssignTo: &a.logView, ReadOnly: true, VScroll: true, MinSize: Size{Height: 100}, MaxSize: Size{Height: 140}},
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

	a.autostart.SetChecked(AutostartEnabled())
	a.loadIntoUI()
	a.refreshDevices()
	a.mw.Closing().Attach(func(*bool, walk.CloseReason) {
		if a.runner != nil {
			a.runner.Stop()
		}
	})
	a.mw.Run()
}

func (a *app) slotGroup(i int, s *slotUI) Widget {
	return GroupBox{
		Title:  fmt.Sprintf("Output %d", i+1),
		Layout: Grid{Columns: 3},
		Children: []Widget{
			Label{Text: "Enabled:"},
			CheckBox{AssignTo: &s.enabled, ColumnSpan: 2},

			Label{Text: "Room name:"},
			LineEdit{AssignTo: &s.name, ColumnSpan: 2},

			Label{Text: "Detected:"},
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
				ColumnSpan:    2,
				OnTextChanged: func() { a.updateStatus(s) },
			},

			Label{Text: ""},
			Label{AssignTo: &s.status, ColumnSpan: 2},

			Label{Text: "Browser .exe:"},
			LineEdit{AssignTo: &s.browser},
			PushButton{Text: "Browse...", OnClicked: func() { a.browse(s) }},

			Label{Text: "URL:"},
			LineEdit{AssignTo: &s.url, ColumnSpan: 2},

			Label{Text: "Extra args:"},
			LineEdit{AssignTo: &s.args},
			PushButton{Text: "Test", OnClicked: func() { a.testOne(i) }},
		},
	}
}

func (a *app) loadIntoUI() {
	for i, o := range a.cfg.Outputs {
		s := a.slots[i]
		s.enabled.SetChecked(o.Enabled)
		s.name.SetText(o.Name)
		s.deviceID.SetText(o.DeviceID)
		s.browser.SetText(o.Browser)
		s.url.SetText(o.URL)
		s.args.SetText(o.Args)
	}
}

func (a *app) readFromUI() {
	for i, s := range a.slots {
		a.cfg.Outputs[i] = Output{
			Enabled:  s.enabled.Checked(),
			Name:     strings.TrimSpace(s.name.Text()),
			DeviceID: NormalizeID(s.deviceID.Text()),
			Browser:  strings.Trim(strings.TrimSpace(s.browser.Text()), `"`),
			URL:      strings.TrimSpace(s.url.Text()),
			Args:     strings.TrimSpace(s.args.Text()),
		}
	}
}

func (a *app) refreshDevices() {
	devices, err := ListRenderDevices()
	if err != nil {
		a.logf("Cannot read audio devices: %v", err)
		return
	}
	a.devices = devices
	labels := make([]string, len(devices))
	for i, d := range devices {
		labels[i] = fmt.Sprintf("%s  [%s]", d.Label(), stateText(d.State))
	}
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
		s.status.SetText("No device selected")
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
	dlg := walk.FileDialog{Title: "Choose browser", Filter: "Programs (*.exe)|*.exe", FilePath: s.browser.Text()}
	if ok, _ := dlg.ShowOpen(a.mw); ok {
		s.browser.SetText(dlg.FilePath)
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

func (a *app) startRunner() *Runner {
	if a.runner != nil {
		a.runner.Stop()
	}
	a.runner = NewRunner(a.cfg, a.logf)
	go a.runner.Enforce(false)
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
	r := a.startRunner()
	go r.LaunchAll()
}

func (a *app) testOne(i int) {
	if !a.save(false) {
		return
	}
	o := a.cfg.Outputs[i]
	if o.Browser == "" {
		a.logf("%s: no browser set", o.Name)
		return
	}
	r := a.startRunner()
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
