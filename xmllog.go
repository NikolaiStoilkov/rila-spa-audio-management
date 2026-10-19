//go:build windows

package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// xmlLog is an io.Writer for the log package that keeps spa-audio-log.xml a
// valid XML document: every entry is inserted just before the closing tag.
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<log>
//	  <entry time="2026-10-19T13:40:01+03:00" level="info" mode="gui">Saved ...</entry>
//	</log>
type xmlLog struct {
	path string
	mode string // "gui" or "run"

	mu    sync.Mutex
	mutex windows.Handle // shared with the other process (GUI and --run both log)
}

type xmlEntry struct {
	XMLName xml.Name `xml:"entry"`
	Time    string   `xml:"time,attr"`
	Level   string   `xml:"level,attr"`
	Mode    string   `xml:"mode,attr"`
	Text    string   `xml:",chardata"`
}

const (
	xmlHeader  = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\r\n<log>\r\n"
	xmlFooter  = "</log>\r\n"
	maxLogSize = 5 << 20 // rotate to .old.xml above 5 MB
)

func newXMLLog(path, mode string) *xmlLog {
	l := &xmlLog{path: path, mode: mode}
	name, _ := windows.UTF16PtrFromString(`Local\RilaSpaAudioLog`)
	l.mutex, _ = windows.CreateMutex(nil, false, name)
	return l
}

func levelOf(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.HasPrefix(m, "warning"):
		return "warning"
	case strings.Contains(m, "fail") || strings.Contains(m, "error") || strings.Contains(m, "cannot"):
		return "error"
	}
	return "info"
}

func (l *xmlLog) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\r\n")
	entry, err := xml.Marshal(xmlEntry{
		Time:  time.Now().Format(time.RFC3339),
		Level: levelOf(msg),
		Mode:  l.mode,
		Text:  msg,
	})
	if err != nil {
		return 0, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mutex != 0 {
		windows.WaitForSingleObject(l.mutex, 2000)
		defer windows.ReleaseMutex(l.mutex)
	}
	return len(p), l.append(append(append([]byte("  "), entry...), "\r\n"...))
}

func (l *xmlLog) append(entry []byte) error {
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
