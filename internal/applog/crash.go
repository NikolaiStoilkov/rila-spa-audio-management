package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

// CrashReportFile is the readable crash report file of both programs,
// next to the exe. New reports are appended; the log viewer shows them.
const CrashReportFile = "log-viewer-crash.txt"

const (
	maxReportFile = 2 << 20 // above 2 MB the file is renamed to .old.txt
	reportLine    = "===================================================================="
)

// Kinds of crash report.
const (
	KindCrash     = "CRASH - the program closed"
	KindFatal     = "CRASH - the program closed (fatal error, reported at the next start)"
	KindRecovered = "ERROR - caught, the program kept running"
)

func (l *Logger) reportPath() string {
	return filepath.Join(filepath.Dir(l.path), CrashReportFile)
}

// CrashReport appends a readable report to log-viewer-crash.txt and logs it.
// recent is what happened just before (nil = this program's recent entries).
func (l *Logger) CrashReport(kind, cause, stack string, when time.Time, recent []Entry) {
	if recent == nil {
		recent = l.Recent()
	}
	where := Location(stack)
	host, _ := os.Hostname()

	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-14s %s\r\n", k+":", v)
		}
	}
	b.WriteString(reportLine + "\r\n")
	fmt.Fprintf(&b, "%s\r\n", kind)
	b.WriteString(reportLine + "\r\n")
	line("Time", when.Format("2006-01-02 15:04:05"))
	line("Program", strings.TrimSpace(l.Program()+" "+l.Version))
	line("Started", map[string]string{"gui": "by a user", "run": "with Windows (background)", "viewer": "by a user"}[l.mode])
	line("Computer", host)
	line("Windows user", l.user)
	line("Session", l.session)
	b.WriteString("\r\n")
	line("What happened", Explain(cause))
	line("Error", cause)
	line("Where", where)
	b.WriteString("\r\nWhat happened just before (oldest first):\r\n")
	if len(recent) == 0 {
		b.WriteString("  (nothing recorded)\r\n")
	}
	for _, e := range recent {
		t := e.Time
		if i := strings.Index(t, "T"); i >= 0 && len(t) >= i+13 {
			t = t[i+1 : i+13] // HH:MM:SS.mmm
		}
		fmt.Fprintf(&b, "  %s  %-7s %s", t, e.Event, e.Message())
		if d := e.Details(); d != "" && e.Event != EventMouse {
			if len(d) > 160 {
				d = d[:160] + "..."
			}
			fmt.Fprintf(&b, "  [%s]", d)
		}
		b.WriteString("\r\n")
	}
	b.WriteString("\r\nTechnical details (for the developer):\r\n")
	b.WriteString(strings.ReplaceAll(strings.TrimSpace(stack), "\n", "\r\n"))
	b.WriteString("\r\n\r\n")

	appendReport(l.reportPath(), b.String())

	level := "error"
	l.Log(level, EventApp, kind+": "+Explain(cause),
		"error", cause, "where", where, "report_file", CrashReportFile, "stack", stack)
}

func appendReport(path, text string) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxReportFile {
		old := strings.TrimSuffix(path, ".txt") + ".old.txt"
		os.Remove(old)
		os.Rename(path, old)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(text)
}

// Explain turns a Go/Windows error into plain words.
func Explain(cause string) string {
	c := strings.ToLower(cause)
	switch {
	case strings.Contains(c, "index out of range"), strings.Contains(c, "slice bounds out of range"):
		return "The program tried to use an item from a list that does not have that many items (a programming error)."
	case strings.Contains(c, "nil pointer"), strings.Contains(c, "invalid memory address"):
		return "The program used something that had not been created yet (a programming error)."
	case strings.Contains(c, "concurrent map"):
		return "Two parts of the program changed the same data at the same time (a programming error)."
	case strings.Contains(c, "out of memory"), strings.Contains(c, "cannot allocate memory"):
		return "The computer ran out of memory."
	case strings.Contains(c, "stack overflow"), strings.Contains(c, "stack exceeds"):
		return "The program got stuck repeating the same step (endless loop)."
	case strings.Contains(c, "deadlock"):
		return "Parts of the program waited for each other forever."
	case strings.Contains(c, "exception 0xc0000005"), strings.Contains(c, "access violation"):
		return "Windows stopped the program for using memory it is not allowed to (often inside a Windows or driver call)."
	case strings.Contains(c, "divide by zero"):
		return "The program divided by zero (a programming error)."
	case strings.Contains(c, "close of closed channel"), strings.Contains(c, "send on closed channel"):
		return "The program used an internal channel that was already closed (a programming error)."
	}
	return "An unexpected error stopped this part of the program."
}

// frameRe matches a stack frame's function line followed by its file:line.
// The file path may contain spaces (e.g. C:/Users/x/Rila SPA Audio/gui.go).
var frameRe = regexp.MustCompile(`(?m)^(\S+)\(.*\)\r?\n\t(.+\.go):(\d+)`)

// Location finds the first frame in this project's code (package main or
// internal), skipping the crash-handling helpers themselves.
func Location(stack string) string {
	skip := []string{"logPanics", "recoverUI", "recoverGoroutine", "safeGo", "applog.Recover", "applog.(*Logger)", "debug.Stack", "runtime."}
	for _, m := range frameRe.FindAllStringSubmatch(stack, -1) {
		fn, file, ln := m[1], m[2], m[3]
		if !(strings.HasPrefix(fn, "main.") || strings.Contains(fn, "rila-spa-audio/")) {
			continue
		}
		skipped := false
		for _, s := range skip {
			if strings.Contains(fn, s) {
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		fn = strings.TrimPrefix(fn, "main.")
		return fmt.Sprintf("%s line %s, in %s", filepath.Base(filepath.FromSlash(file)), ln, fn)
	}
	return ""
}

// CaptureCrashes makes Go write any fatal crash (including ones recover()
// cannot catch, like concurrent map writes) to a raw file, since a GUI program
// has no console. A crash left there by the previous run is turned into a
// readable report at this start. legacy are older raw crash files to import.
func CaptureCrashes(l *Logger, rawFile string, legacy ...string) {
	for _, f := range append(legacy, rawFile) {
		data, err := os.ReadFile(f)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		when := time.Now()
		if fi, err := os.Stat(f); err == nil {
			when = fi.ModTime()
		}
		text := string(data)
		l.CrashReport(KindFatal, firstLine(text), text, when, recentBefore(l.path, when))
		if f == rawFile {
			os.Truncate(f, 0)
		} else {
			os.Remove(f)
		}
	}
	if f, err := os.OpenFile(rawFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close() // SetCrashOutput keeps its own copy of the handle
	}
}

// recentBefore returns the last log entries written before t.
func recentBefore(logPath string, t time.Time) []Entry {
	all, err := ReadFile(logPath)
	if err != nil {
		return []Entry{}
	}
	end := len(all)
	for end > 0 && !all[end-1].When.IsZero() && all[end-1].When.After(t) {
		end--
	}
	start := max(0, end-recentKeep)
	return all[start:end]
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "unknown error"
}

// Recover is deferred directly (defer applog.Recover(l, "...")): a panic gets
// a crash report and only that action stops - not the program.
// recover() only works when called by the deferred function itself, so
// helpers that wrap it must call recover() themselves and use Recovered.
func Recover(l *Logger, what string) {
	if r := recover(); r != nil {
		l.Recovered(what, r)
	}
}

// Recovered writes the report for a panic value already recovered.
func (l *Logger) Recovered(what string, r any) {
	l.CrashReport(KindRecovered+" ("+what+")", fmt.Sprint(r), string(debug.Stack()), time.Now(), nil)
}

// Crashed writes the report for a panic that will close the program and
// waits until it is on disk.
func (l *Logger) Crashed(r any) {
	l.CrashReport(KindCrash, fmt.Sprint(r), string(debug.Stack()), time.Now(), nil)
	l.Flush()
}

// ReadCrashReports returns the reports in a crash report file, newest first.
func ReadCrashReports(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Each report is: line, kind, line, body.
	parts := strings.Split(string(data), reportLine+"\r\n")
	var reports []string
	for i := 1; i+1 < len(parts); i += 2 {
		reports = append(reports, reportLine+"\r\n"+parts[i]+reportLine+"\r\n"+strings.TrimRight(parts[i+1], "\r\n")+"\r\n")
	}
	for i, j := 0, len(reports)-1; i < j; i, j = i+1, j-1 {
		reports[i], reports[j] = reports[j], reports[i]
	}
	return reports, nil
}
