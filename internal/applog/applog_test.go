package applog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentWritesStayValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spa-audio-log.xml")
	l := New(path, "gui")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				l.Log("", EventAction, `Clicked "Save" <&>`, "count_session", "1", "cursor_screen", "10,20", "empty", "")
			}
		}(g)
	}
	wg.Wait()

	entries, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 200 {
		t.Fatalf("got %d entries, want 200", len(entries))
	}
	e := entries[0]
	if e.Message() != `Clicked "Save" <&>` || e.Event != EventAction || e.Session != l.Session() || e.When.IsZero() {
		t.Fatalf("bad entry: %+v", e)
	}
	if e.Get("cursor_screen") != "10,20" || e.Get("empty") != "" || len(e.Data) != 2 {
		t.Fatalf("bad fields: %+v", e.Data)
	}
}

func TestStdLogWriterAndLevels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.xml")
	l := New(path, "run")
	l.Write([]byte("WARNING: device missing\n"))
	l.Write([]byte("Device 1: routing pid 5 failed\n"))
	l.Write([]byte("All audio devices are ready\n"))
	entries, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"warning", "error", "info"}
	for i, e := range entries {
		if e.Level != want[i] || e.Event != EventApp || e.Mode != "run" {
			t.Fatalf("entry %d: %+v", i, e)
		}
	}
}

func TestReadsOldFormatAndDamagedFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.xml")
	os.WriteFile(old, []byte("<?xml version=\"1.0\"?>\r\n<log>\r\n  <entry time=\"2026-10-19T13:40:01+03:00\" level=\"info\" mode=\"gui\">Saved config</entry>\r\n"), 0o644)
	entries, err := ReadFile(old) // cut off: no </log>
	if err != nil || len(entries) != 1 || entries[0].Message() != "Saved config" || entries[0].Event != EventApp {
		t.Fatalf("old format: %v %+v", err, entries)
	}

	// A damaged file is moved aside and a new one started.
	path := filepath.Join(dir, "log.xml")
	os.WriteFile(path, []byte("<log><entry"), 0o644)
	New(path, "gui").Log("info", EventApp, "fresh")
	entries, err = ReadFile(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("recovery: %v %d", err, len(entries))
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "log.broken-*.xml"))
	if len(matches) != 1 {
		t.Fatalf("damaged file not kept: %v", matches)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(data), "</log>\r\n") {
		t.Fatal("file does not end with </log>")
	}
}
