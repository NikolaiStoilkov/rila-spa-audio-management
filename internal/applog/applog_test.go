package applog

import (
	"fmt"
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
	l.Flush()

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
	l.Flush()
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
	fresh := New(path, "gui")
	fresh.Log("info", EventApp, "fresh")
	fresh.Flush()
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

func TestIncrementalAndParallelRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.xml")
	l := New(path, "gui")
	for i := 0; i < 3000; i++ { // > 256 KB, so it is parsed on several cores
		l.Log("info", EventMouse, fmt.Sprintf("click %d", i), "cursor_screen", "1,2")
	}
	l.Flush()

	all, off, reset, err := ReadNew(path, 0)
	if err != nil || reset || len(all) != 3000 {
		t.Fatalf("first read: %v reset=%v n=%d", err, reset, len(all))
	}
	for i, e := range all {
		if e.Message() != fmt.Sprintf("click %d", i) {
			t.Fatalf("entry %d out of order: %q", i, e.Message())
		}
	}
	if !strings.Contains(all[5].Haystack, "click 5") || !strings.Contains(all[5].Haystack, "cursor_screen=1,2") {
		t.Fatalf("haystack: %q", all[5].Haystack)
	}

	// Nothing new.
	if more, off2, _, _ := ReadNew(path, off); len(more) != 0 || off2 != off {
		t.Fatalf("expected nothing new, got %d", len(more))
	}

	// Only the new entries are returned.
	l.Log("warning", EventSound, "Device 1: no sound")
	l.Flush()
	more, _, reset, err := ReadNew(path, off)
	if err != nil || reset || len(more) != 1 || more[0].Event != EventSound {
		t.Fatalf("incremental: %v %v %+v", err, reset, more)
	}

	// A smaller (rotated) file resets.
	os.Remove(path)
	l2 := New(path, "gui")
	l2.Log("info", EventApp, "new file")
	l2.Flush()
	more, _, reset, err = ReadNew(path, off)
	if err != nil || !reset || len(more) != 1 {
		t.Fatalf("rotation: %v reset=%v n=%d", err, reset, len(more))
	}
}
