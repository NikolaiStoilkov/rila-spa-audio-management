// Package applog writes and reads spa-audio-log.xml, the shared log of
// spa-audio.exe and log-viewer.exe.
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<log>
//	  <entry time="2026-10-19T13:40:01+03:00" level="info" event="action" mode="gui" session="3f9a1c2e" user="PC\spa">
//	    <msg>Clicked "Save"</msg>
//	    <data name="count_session">2</data>
//	    <data name="cursor_screen">812,644</data>
//	  </entry>
//	</log>
//
// Every entry is inserted just before </log>, so the file is always valid XML.
package applog

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"io"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"
)

// Event types.
const (
	EventApp    = "app"    // start, stop, crash, shutdown
	EventAction = "action" // button / menu clicks
	EventChange = "change" // a setting was changed
	EventSave   = "save"   // settings saved (with what changed)
	EventMouse  = "mouse"  // every mouse click with cursor position
	EventWindow = "window" // shown, hidden to tray, minimized, ...
	EventTab    = "tab"    // switched device tab
	EventSystem = "system" // Windows shutdown, logoff, sleep, resume
	EventRunner = "runner" // browsers started, audio routed, volume
	EventLogin  = "login"  // log viewer logins
	EventViewer = "viewer" // log viewer opened / closed
)

type Field struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

type Entry struct {
	XMLName xml.Name `xml:"entry"`
	Time    string   `xml:"time,attr"`
	Level   string   `xml:"level,attr"`
	Event   string   `xml:"event,attr,omitempty"`
	Mode    string   `xml:"mode,attr,omitempty"`
	Session string   `xml:"session,attr,omitempty"`
	User    string   `xml:"user,attr,omitempty"`
	Text    string   `xml:",chardata"` // message in logs written before <msg> existed
	Msg     string   `xml:"msg,omitempty"`
	Data    []Field  `xml:"data"`

	When time.Time `xml:"-"`
}

func (e Entry) Message() string {
	if e.Msg != "" {
		return e.Msg
	}
	return strings.TrimSpace(e.Text)
}

func (e Entry) Get(name string) string {
	for _, f := range e.Data {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

// Details renders the data fields as "name=value; ...".
func (e Entry) Details() string {
	parts := make([]string, len(e.Data))
	for i, f := range e.Data {
		parts[i] = f.Name + "=" + f.Value
	}
	return strings.Join(parts, "; ")
}

type Logger struct {
	path    string
	mode    string
	session string
	user    string

	mu   sync.Mutex
	lock crossProcessLock // GUI, background run and viewer can write at once
}

const (
	xmlHeader  = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\r\n<log>\r\n"
	xmlFooter  = "</log>\r\n"
	maxLogSize = 5 << 20 // rotate to .old.xml above 5 MB
)

// New returns a logger for path. mode is "gui", "run" or "viewer".
func New(path, mode string) *Logger {
	l := &Logger{path: path, mode: mode, session: newSessionID(), user: CurrentUser()}
	l.lock = newLock(`Local\RilaSpaAudioLog`)
	return l
}

func (l *Logger) Session() string { return l.session }
func (l *Logger) Path() string    { return l.path }

func newSessionID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CurrentUser is the Windows account, e.g. SPA-PC\reception.
func CurrentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USERNAME")
}

// LevelOf guesses the level of a plain text message.
func LevelOf(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.HasPrefix(m, "warning"):
		return "warning"
	case strings.Contains(m, "fail") || strings.Contains(m, "error") || strings.Contains(m, "cannot"):
		return "error"
	}
	return "info"
}

// Log writes one entry. kv are name, value pairs stored as <data> fields.
func (l *Logger) Log(level, event, msg string, kv ...string) {
	if level == "" {
		level = LevelOf(msg)
	}
	e := Entry{
		Time:    time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level:   level,
		Event:   event,
		Mode:    l.mode,
		Session: l.session,
		User:    l.user,
		Msg:     msg,
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			e.Data = append(e.Data, Field{Name: kv[i], Value: kv[i+1]})
		}
	}
	l.write(e)
}

// Write lets the standard log package write here (event "app").
func (l *Logger) Write(p []byte) (int, error) {
	l.Log("", EventApp, strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

func (l *Logger) write(e Entry) error {
	data, err := xml.MarshalIndent(e, "  ", "  ")
	if err != nil {
		return err
	}
	data = append(append([]byte("  "), data...), "\r\n"...)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.append(data)
}

func (l *Logger) append(entry []byte) error {
	if fi, err := os.Stat(l.path); err == nil && fi.Size() > maxLogSize {
		old := strings.TrimSuffix(l.path, ".xml") + ".old.xml"
		os.Remove(old)
		os.Rename(l.path, old)
	}

	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	footer := []byte(xmlFooter)
	pos := int64(0)
	if size == 0 {
		if _, err := f.WriteString(xmlHeader); err != nil {
			return err
		}
		pos = int64(len(xmlHeader))
	} else {
		tail := make([]byte, len(footer))
		if size >= int64(len(footer)) {
			f.ReadAt(tail, size-int64(len(footer)))
		}
		if !bytes.Equal(tail, footer) {
			// Damaged (e.g. power cut mid-write): keep it aside, start fresh.
			f.Close()
			os.Rename(l.path, strings.TrimSuffix(l.path, ".xml")+".broken-"+time.Now().Format("20060102-150405")+".xml")
			return l.append(entry)
		}
		pos = size - int64(len(footer))
	}
	_, err = f.WriteAt(append(entry, footer...), pos)
	return err
}

// ReadFile parses a log, tolerating a file cut off mid-write.
func ReadFile(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(data, []byte("</log>")) {
		if i := bytes.LastIndex(data, []byte("</entry>")); i >= 0 {
			data = append(data[:i+len("</entry>")], "\r\n</log>"...)
		}
	}
	var doc struct {
		Entries []Entry `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	for i := range doc.Entries {
		e := &doc.Entries[i]
		if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
			e.When = t
		}
		e.Msg = strings.TrimSpace(e.Msg)
		if e.Event == "" {
			e.Event = EventApp
		}
	}
	return doc.Entries, nil
}
