//go:build windows

// Log viewer for spa-audio-log.xml, protected by a login.
package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rila-spa-audio/internal/applog"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// SHA-256 of "username:password". Default login: admin / admin123.
const credentialHash = "bf6b5bdb74c79ece9fc0ad0ac9fb0359f9555d4f35a83b2e6ec69ae99e09603d"

const maxAttempts = 3

// audit records logins and viewer use in the same log.
var audit *applog.Logger

type logModel struct {
	walk.TableModelBase
	rows []applog.Entry
}

func (m *logModel) RowCount() int { return len(m.rows) }

func (m *logModel) Value(row, col int) interface{} {
	e := m.rows[row]
	switch col {
	case 0:
		if e.When.IsZero() {
			return e.Time
		}
		return e.When.Format("2006-01-02 15:04:05")
	case 1:
		return e.Level
	case 2:
		return e.Event
	case 3:
		return e.User
	case 4:
		return e.Message()
	}
	return e.Details()
}

type statRow struct {
	Control string
	Count   int
	InLog   int
	First   time.Time
	Last    time.Time
}

type statModel struct {
	walk.TableModelBase
	rows []statRow
}

func (m *statModel) RowCount() int { return len(m.rows) }

func (m *statModel) Value(row, col int) interface{} {
	r := m.rows[row]
	date := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02 15:04:05")
	}
	switch col {
	case 0:
		return r.Control
	case 1:
		return r.Count
	case 2:
		return r.InLog
	case 3:
		return date(r.First)
	}
	return date(r.Last)
}

type viewer struct {
	mw          *walk.MainWindow
	table       *walk.TableView
	statTable   *walk.TableView
	level       *walk.ComboBox
	event       *walk.ComboBox
	search      *walk.LineEdit
	autoRefresh *walk.CheckBox
	withOld     *walk.CheckBox
	status      *walk.Label
	statStatus  *walk.Label
	model       *logModel
	stats       *statModel
	path        string
	all         []applog.Entry
	modTime     time.Time
}

var events = []string{"All", applog.EventApp, applog.EventAction, applog.EventChange, applog.EventSave,
	applog.EventMouse, applog.EventWindow, applog.EventTab, applog.EventSystem, applog.EventRunner,
	applog.EventLogin, applog.EventViewer}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func main() {
	path := filepath.Join(exeDir(), "spa-audio-log.xml")
	audit = applog.New(path, "viewer")
	user, ok := login()
	if !ok {
		return
	}
	opened := time.Now()
	audit.Log("info", applog.EventViewer, "Log viewer opened", "login", user)
	v := &viewer{model: &logModel{}, stats: &statModel{}, path: path}
	v.run()
	audit.Log("info", applog.EventViewer, "Log viewer closed", "login", user, "open_for", time.Since(opened).Round(time.Second).String())
}

func checkLogin(user, pass string) bool {
	sum := sha256.Sum256([]byte(strings.TrimSpace(user) + ":" + pass))
	want, _ := hex.DecodeString(credentialHash)
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

// login asks for the credentials; every attempt is logged (never the password).
func login() (string, bool) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var dlg *walk.Dialog
		var userEdit, passEdit *walk.LineEdit
		var okBtn, cancelBtn *walk.PushButton
		ok := false
		name := ""

		Dialog{
			AssignTo:      &dlg,
			Title:         "Rila Spa Audio - Log viewer login",
			DefaultButton: &okBtn,
			CancelButton:  &cancelBtn,
			MinSize:       Size{Width: 340, Height: 170},
			Layout:        Grid{Columns: 2},
			Children: []Widget{
				Label{Text: "Username:"},
				LineEdit{AssignTo: &userEdit},
				Label{Text: "Password:"},
				LineEdit{AssignTo: &passEdit, PasswordMode: true},
				HSpacer{},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						HSpacer{},
						PushButton{AssignTo: &okBtn, Text: "Login", OnClicked: func() {
							name = strings.TrimSpace(userEdit.Text())
							ok = checkLogin(userEdit.Text(), passEdit.Text())
							dlg.Accept()
						}},
						PushButton{AssignTo: &cancelBtn, Text: "Cancel", OnClicked: func() { dlg.Cancel() }},
					},
				},
			},
		}.Create(nil)
		if icon, err := walk.Resources.Icon("APP"); err == nil {
			dlg.SetIcon(icon)
		}
		userEdit.SetFocus()

		if dlg.Run() != walk.DlgCmdOK {
			audit.Log("info", applog.EventLogin, "Log viewer login cancelled", "attempt", strconv.Itoa(attempt))
			return "", false
		}
		if ok {
			audit.Log("info", applog.EventLogin, "Log viewer login successful", "login", name, "attempt", strconv.Itoa(attempt))
			return name, true
		}
		audit.Log("warning", applog.EventLogin, "Log viewer login failed: wrong username or password",
			"login_tried", name, "attempt", strconv.Itoa(attempt))
		left := maxAttempts - attempt
		if left == 0 {
			audit.Log("error", applog.EventLogin, "Log viewer locked out after too many failed logins", "attempts", strconv.Itoa(maxAttempts))
			walk.MsgBox(nil, "Login failed", "Wrong username or password. Too many attempts.", walk.MsgBoxIconError)
			return "", false
		}
		walk.MsgBox(nil, "Login failed", fmt.Sprintf("Wrong username or password. %d attempt(s) left.", left), walk.MsgBoxIconWarning)
	}
	return "", false
}

func (v *viewer) run() {
	err := MainWindow{
		AssignTo: &v.mw,
		Title:    "Rila Spa Audio - Log viewer",
		MinSize:  Size{Width: 700, Height: 450},
		Size:     Size{Width: 1100, Height: 700},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{
					{
						Title:  "Log",
						Layout: VBox{},
						Children: []Widget{
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									Label{Text: "Level:"},
									ComboBox{AssignTo: &v.level, Model: []string{"All", "info", "warning", "error"}, CurrentIndex: 0, OnCurrentIndexChanged: v.applyFilter},
									Label{Text: "Event:"},
									ComboBox{AssignTo: &v.event, Model: events, CurrentIndex: 0, OnCurrentIndexChanged: v.applyFilter},
									Label{Text: "Search:"},
									LineEdit{AssignTo: &v.search, OnTextChanged: v.applyFilter, MinSize: Size{Width: 200}},
									CheckBox{AssignTo: &v.withOld, Text: "Include older log", OnClicked: v.forceReload},
									CheckBox{AssignTo: &v.autoRefresh, Text: "Auto refresh", Checked: true},
									HSpacer{},
									PushButton{Text: "Refresh", OnClicked: v.forceReload},
									PushButton{Text: "Open file...", OnClicked: v.openFile},
								},
							},
							TableView{
								AssignTo:            &v.table,
								AlternatingRowBG:    true,
								LastColumnStretched: true,
								Columns: []TableViewColumn{
									{Title: "Time", Width: 140},
									{Title: "Level", Width: 60},
									{Title: "Event", Width: 65},
									{Title: "User", Width: 120},
									{Title: "Message", Width: 380},
									{Title: "Details"},
								},
								Model:           v.model,
								OnItemActivated: v.showDetails,
								StyleCell: func(style *walk.CellStyle) {
									if style.Row() >= len(v.model.rows) {
										return
									}
									switch v.model.rows[style.Row()].Level {
									case "error":
										style.TextColor = walk.RGB(190, 0, 0)
									case "warning":
										style.TextColor = walk.RGB(170, 100, 0)
									}
								},
							},
							Label{AssignTo: &v.status},
						},
					},
					{
						Title:  "Click statistics",
						Layout: VBox{},
						Children: []Widget{
							Label{Text: "How many times each button, menu item and control was clicked. \"All time\" comes from spa-audio-stats.json, \"In loaded log\" from the log entries shown."},
							TableView{
								AssignTo:            &v.statTable,
								AlternatingRowBG:    true,
								LastColumnStretched: true,
								Columns: []TableViewColumn{
									{Title: "Control", Width: 320},
									{Title: "All time", Width: 80},
									{Title: "In loaded log", Width: 100},
									{Title: "First click", Width: 150},
									{Title: "Last click"},
								},
								Model: v.stats,
							},
							Label{AssignTo: &v.statStatus},
						},
					},
				},
			},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(nil, "Log viewer", "Cannot create window: "+err.Error(), walk.MsgBoxIconError)
		return
	}
	if icon, err := walk.Resources.Icon("APP"); err == nil {
		v.mw.SetIcon(icon)
	}

	v.reload()
	go func() {
		for range time.Tick(5 * time.Second) {
			v.mw.Synchronize(func() {
				if v.autoRefresh.Checked() {
					v.reload()
				}
			})
		}
	}()
	v.mw.Synchronize(func() { win.ShowWindow(v.mw.Handle(), win.SW_SHOWMAXIMIZED) })
	v.mw.Run()
}

func (v *viewer) forceReload() {
	v.modTime = time.Time{}
	v.reload()
}

func (v *viewer) reload() {
	fi, err := os.Stat(v.path)
	if err == nil && fi.ModTime().Equal(v.modTime) {
		return // unchanged; keep selection and scroll position
	}
	entries, err := applog.ReadFile(v.path)
	if err != nil {
		v.all = nil
		v.applyFilter()
		if os.IsNotExist(err) {
			v.status.SetText("No log yet: " + v.path)
		} else {
			v.status.SetText("Cannot read " + v.path + ": " + err.Error())
		}
		return
	}
	v.modTime = fi.ModTime()
	if v.withOld.Checked() {
		old := strings.TrimSuffix(v.path, ".xml") + ".old.xml"
		if more, err := applog.ReadFile(old); err == nil {
			entries = append(more, entries...)
		}
	}
	v.all = entries
	v.applyFilter()
	v.loadStats()
}

func (v *viewer) applyFilter() {
	if v.table == nil || v.status == nil {
		return
	}
	level, event := v.level.Text(), v.event.Text()
	search := strings.ToLower(strings.TrimSpace(v.search.Text()))
	var rows []applog.Entry
	for _, e := range v.all {
		if level != "All" && e.Level != level {
			continue
		}
		if event != "All" && e.Event != event {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(e.Message()+" "+e.Details()+" "+e.User), search) {
			continue
		}
		rows = append(rows, e)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].When.After(rows[j].When) }) // newest first
	v.model.rows = rows
	v.model.PublishRowsReset()
	v.status.SetText(fmt.Sprintf("%d of %d entries  -  %s  -  double-click a row for all details", len(rows), len(v.all), v.path))
}

// loadStats merges all-time counts from spa-audio-stats.json with the
// "action" entries in the loaded log.
func (v *viewer) loadStats() {
	byControl := map[string]*statRow{}
	get := func(c string) *statRow {
		if byControl[c] == nil {
			byControl[c] = &statRow{Control: c}
		}
		return byControl[c]
	}
	var file struct {
		Controls map[string]struct {
			Count int       `json:"count"`
			First time.Time `json:"first"`
			Last  time.Time `json:"last"`
		} `json:"controls"`
	}
	statsFile := filepath.Join(filepath.Dir(v.path), "spa-audio-stats.json")
	if data, err := os.ReadFile(statsFile); err == nil {
		json.Unmarshal(data, &file)
	}
	for c, s := range file.Controls {
		r := get(c)
		r.Count, r.First, r.Last = s.Count, s.First, s.Last
	}
	for _, e := range v.all {
		if e.Event != applog.EventAction || e.Get("control") == "" {
			continue
		}
		r := get(e.Get("control"))
		r.InLog++
		if r.First.IsZero() || e.When.Before(r.First) {
			r.First = e.When
		}
		if e.When.After(r.Last) {
			r.Last = e.When
		}
	}
	rows := make([]statRow, 0, len(byControl))
	total := 0
	for _, r := range byControl {
		rows = append(rows, *r)
		total += max(r.Count, r.InLog)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Control < rows[j].Control
	})
	v.stats.rows = rows
	v.stats.PublishRowsReset()
	v.statStatus.SetText(fmt.Sprintf("%d controls, %d clicks in total", len(rows), total))
}

// showDetails shows every field of the double-clicked entry.
func (v *viewer) showDetails() {
	i := v.table.CurrentIndex()
	if i < 0 || i >= len(v.model.rows) {
		return
	}
	e := v.model.rows[i]
	var b strings.Builder
	line := func(k, val string) {
		if val != "" {
			fmt.Fprintf(&b, "%-20s %s\r\n", k+":", val)
		}
	}
	line("Time", e.Time)
	line("Level", e.Level)
	line("Event", e.Event)
	line("Message", e.Message())
	line("Windows user", e.User)
	line("Started as", e.Mode)
	line("Session", e.Session)
	if len(e.Data) > 0 {
		b.WriteString("\r\n")
		for _, f := range e.Data {
			line(f.Name, strings.ReplaceAll(f.Value, "\n", "\r\n"))
		}
	}

	var dlg *walk.Dialog
	var closeBtn *walk.PushButton
	Dialog{
		AssignTo:     &dlg,
		Title:        "Log entry",
		CancelButton: &closeBtn,
		MinSize:      Size{Width: 700, Height: 420},
		Layout:       VBox{},
		Children: []Widget{
			TextEdit{Text: b.String(), ReadOnly: true, VScroll: true, HScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{Text: "Copy", OnClicked: func() { walk.Clipboard().SetText(b.String()) }},
					PushButton{AssignTo: &closeBtn, Text: "Close", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Run(v.mw)
}

func (v *viewer) openFile() {
	dlg := walk.FileDialog{Title: "Open log", Filter: "XML log (*.xml)|*.xml", FilePath: v.path}
	if ok, _ := dlg.ShowOpen(v.mw); ok {
		v.path = dlg.FilePath
		v.forceReload()
	}
}
