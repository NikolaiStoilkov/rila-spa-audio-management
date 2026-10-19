//go:build windows

// Log viewer for spa-audio-log.xml, protected by a login.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// SHA-256 of "username:password". Default login: admin / admin123.
const credentialHash = "bf6b5bdb74c79ece9fc0ad0ac9fb0359f9555d4f35a83b2e6ec69ae99e09603d"

const maxAttempts = 3

type entry struct {
	Time  string `xml:"time,attr"`
	Level string `xml:"level,attr"`
	Mode  string `xml:"mode,attr"`
	Text  string `xml:",chardata"`

	when time.Time
}

type logDoc struct {
	Entries []entry `xml:"entry"`
}

type model struct {
	walk.TableModelBase
	rows []entry
}

func (m *model) RowCount() int { return len(m.rows) }

func (m *model) Value(row, col int) interface{} {
	e := m.rows[row]
	switch col {
	case 0:
		if e.when.IsZero() {
			return e.Time
		}
		return e.when.Format("2006-01-02 15:04:05")
	case 1:
		return e.Level
	case 2:
		return e.Mode
	}
	return e.Text
}

type viewer struct {
	mw          *walk.MainWindow
	table       *walk.TableView
	level       *walk.ComboBox
	mode        *walk.ComboBox
	search      *walk.LineEdit
	autoRefresh *walk.CheckBox
	withOld     *walk.CheckBox
	status      *walk.Label
	model       *model
	path        string
	all         []entry
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func main() {
	if !login() {
		return
	}
	v := &viewer{model: &model{}, path: filepath.Join(exeDir(), "spa-audio-log.xml")}
	v.run()
}

func checkLogin(user, pass string) bool {
	sum := sha256.Sum256([]byte(strings.TrimSpace(user) + ":" + pass))
	want, _ := hex.DecodeString(credentialHash)
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

func login() bool {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var dlg *walk.Dialog
		var user, pass *walk.LineEdit
		var okBtn, cancelBtn *walk.PushButton
		ok := false

		Dialog{
			AssignTo:      &dlg,
			Title:         "Rila Spa Audio - Log viewer login",
			DefaultButton: &okBtn,
			CancelButton:  &cancelBtn,
			MinSize:       Size{Width: 340, Height: 170},
			Layout:        Grid{Columns: 2},
			Children: []Widget{
				Label{Text: "Username:"},
				LineEdit{AssignTo: &user},
				Label{Text: "Password:"},
				LineEdit{AssignTo: &pass, PasswordMode: true},
				HSpacer{},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						HSpacer{},
						PushButton{AssignTo: &okBtn, Text: "Login", OnClicked: func() {
							ok = checkLogin(user.Text(), pass.Text())
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
		user.SetFocus()

		if dlg.Run() != walk.DlgCmdOK {
			return false
		}
		if ok {
			return true
		}
		left := maxAttempts - attempt
		if left == 0 {
			walk.MsgBox(nil, "Login failed", "Wrong username or password. Too many attempts.", walk.MsgBoxIconError)
			return false
		}
		walk.MsgBox(nil, "Login failed", fmt.Sprintf("Wrong username or password. %d attempt(s) left.", left), walk.MsgBoxIconWarning)
	}
	return false
}

func (v *viewer) run() {
	err := MainWindow{
		AssignTo: &v.mw,
		Title:    "Rila Spa Audio - Log viewer",
		MinSize:  Size{Width: 640, Height: 400},
		Size:     Size{Width: 1000, Height: 650},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "Level:"},
					ComboBox{AssignTo: &v.level, Model: []string{"All", "info", "warning", "error"}, CurrentIndex: 0, OnCurrentIndexChanged: v.applyFilter},
					Label{Text: "Mode:"},
					ComboBox{AssignTo: &v.mode, Model: []string{"All", "gui", "run"}, CurrentIndex: 0, OnCurrentIndexChanged: v.applyFilter},
					Label{Text: "Search:"},
					LineEdit{AssignTo: &v.search, OnTextChanged: v.applyFilter, MinSize: Size{Width: 200}},
					CheckBox{AssignTo: &v.withOld, Text: "Include older log", OnClicked: v.reload},
					CheckBox{AssignTo: &v.autoRefresh, Text: "Auto refresh", Checked: true},
					HSpacer{},
					PushButton{Text: "Refresh", OnClicked: v.reload},
					PushButton{Text: "Open file...", OnClicked: v.openFile},
				},
			},
			TableView{
				AssignTo:            &v.table,
				AlternatingRowBG:    true,
				LastColumnStretched: true,
				Columns: []TableViewColumn{
					{Title: "Time", Width: 150},
					{Title: "Level", Width: 70},
					{Title: "Mode", Width: 50},
					{Title: "Message"},
				},
				Model: v.model,
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

func readLog(path string) ([]entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Tolerate a file cut off mid-write.
	if !bytes.Contains(data, []byte("</log>")) {
		if i := bytes.LastIndex(data, []byte("</entry>")); i >= 0 {
			data = append(data[:i+len("</entry>")], "\r\n</log>"...)
		}
	}
	var doc logDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	for i := range doc.Entries {
		doc.Entries[i].when, _ = time.Parse(time.RFC3339, doc.Entries[i].Time)
		doc.Entries[i].Text = strings.TrimSpace(doc.Entries[i].Text)
	}
	return doc.Entries, nil
}

func (v *viewer) reload() {
	entries, err := readLog(v.path)
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
	if v.withOld.Checked() {
		old := strings.TrimSuffix(v.path, ".xml") + ".old.xml"
		if more, err := readLog(old); err == nil {
			entries = append(more, entries...)
		}
	}
	if len(entries) == len(v.all) {
		return // nothing new; keep selection and scroll position
	}
	v.all = entries
	v.applyFilter()
}

func (v *viewer) applyFilter() {
	if v.table == nil || v.status == nil {
		return
	}
	level, mode := v.level.Text(), v.mode.Text()
	search := strings.ToLower(strings.TrimSpace(v.search.Text()))
	var rows []entry
	for _, e := range v.all {
		if level != "All" && e.Level != level {
			continue
		}
		if mode != "All" && e.Mode != mode {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(e.Text), search) {
			continue
		}
		rows = append(rows, e)
	}
	// Newest first.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].when.After(rows[j].when) })
	v.model.rows = rows
	v.model.PublishRowsReset()
	v.status.SetText(fmt.Sprintf("%d of %d entries  -  %s", len(rows), len(v.all), v.path))
}

func (v *viewer) openFile() {
	dlg := walk.FileDialog{Title: "Open log", Filter: "XML log (*.xml)|*.xml", FilePath: v.path}
	if ok, _ := dlg.ShowOpen(v.mw); ok {
		v.path = dlg.FilePath
		v.all = nil
		v.reload()
	}
}
