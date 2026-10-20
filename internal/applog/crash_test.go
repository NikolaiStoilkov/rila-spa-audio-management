package applog

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestCrashReport(t *testing.T) {
	dir := t.TempDir()
	l := New(filepath.Join(dir, "spa-audio-log.xml"), "gui")
	l.Version = "1.1.0"
	l.Log("info", EventAction, `Clicked "+ (add device)"`, "control", "+ (add device)")
	l.Log("info", EventMouse, "Mouse left click on Volume slider in 2. Device 2", "cursor_screen", "1,2")

	func() {
		defer Recover(l, "volume slider")
		list := []int{0}
		i := 1
		_ = list[i] // index out of range [1] with length 1, like the real crash
	}()
	l.Flush()

	reports, err := ReadCrashReports(filepath.Join(dir, CrashReportFile))
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports: %v %d", err, len(reports))
	}
	r := reports[0]
	for _, want := range []string{
		KindRecovered + " (volume slider)",
		"spa-audio.exe 1.1.0",
		"a list that does not have that many items",
		"index out of range [1] with length 1",
		"crash_test.go line",                // where: first frame in this project
		`Clicked "+ (add device)"`,          // what happened just before
		"Mouse left click on Volume slider", // mouse entries, without details
		"Technical details",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("report misses %q:\n%s", want, r)
		}
	}

	// The report is also in the XML log.
	entries, _ := ReadFile(l.Path())
	last := entries[len(entries)-1]
	if last.Level != "error" || last.Get("report_file") != CrashReportFile {
		t.Fatalf("log entry: %+v", last)
	}
}

func TestFatalCrashImportedAtNextStart(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "spa-audio-log.xml")
	before := New(logPath, "gui")
	before.Log("info", EventAction, `Clicked "Save & Apply"`)
	before.Flush()

	raw := filepath.Join(dir, "spa-audio-crash-raw.txt")
	legacy := filepath.Join(dir, "spa-audio-crash.txt")
	os.WriteFile(legacy, []byte("panic: runtime error: index out of range [1] with length 1\n\ngoroutine 1 [running]:\nmain.logPanics()\n\tC:/Users/N/Rila SPA Audio Management/audit.go:147 +0x139\npanic({0x1, 0x2})\n\tC:/Program Files/Go/src/runtime/panic.go:860 +0x13a\nmain.(*app).volumeChanged(0x1, 0x2)\n\tC:/Users/N/Rila SPA Audio Management/gui.go:895 +0x1e5\nmain.main()\n\tC:/x/main.go:60 +0x345\n"), 0o644)
	time.Sleep(20 * time.Millisecond)

	next := New(logPath, "gui")
	CaptureCrashes(next, raw, legacy)
	debug.SetCrashOutput(nil, debug.CrashOptions{}) // undo for the test process
	next.Flush()

	reports, _ := ReadCrashReports(filepath.Join(dir, CrashReportFile))
	if len(reports) != 1 {
		t.Fatalf("got %d reports", len(reports))
	}
	r := reports[0]
	for _, want := range []string{KindFatal, "gui.go line 895, in (*app).volumeChanged", `Clicked "Save & Apply"`} {
		if !strings.Contains(r, want) {
			t.Errorf("report misses %q:\n%s", want, r)
		}
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("legacy crash file should be removed after import")
	}
}

func TestReportsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	l := New(filepath.Join(dir, "log.xml"), "viewer")
	l.CrashReport(KindRecovered, "first", "", time.Now(), []Entry{})
	l.CrashReport(KindCrash, "second", "", time.Now(), []Entry{})
	l.Flush()
	reports, _ := ReadCrashReports(filepath.Join(dir, CrashReportFile))
	if len(reports) != 2 || !strings.Contains(reports[0], "second") || !strings.Contains(reports[1], "first") {
		t.Fatalf("order: %q", reports)
	}
	if !strings.Contains(reports[0], "log-viewer.exe") {
		t.Error("program name missing")
	}
}

func TestExplain(t *testing.T) {
	cases := map[string]string{
		"runtime error: invalid memory address or nil pointer dereference": "not been created yet",
		"fatal error: concurrent map writes":                               "same data at the same time",
		"Exception 0xc0000005 0x0 0x0":                                     "memory it is not allowed",
		"something else":                                                   "unexpected error",
	}
	for in, want := range cases {
		if got := Explain(in); !strings.Contains(got, want) {
			t.Errorf("Explain(%q) = %q", in, got)
		}
	}
}
