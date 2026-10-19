package applog

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// CaptureCrashes makes Go write any fatal crash (including ones recover()
// cannot catch, like concurrent map writes) to crashFile. A GUI program has no
// console, so without this the reason is lost. A crash left there by the
// previous run is logged as an error and the file is emptied.
func CaptureCrashes(l *Logger, crashFile string) {
	if data, err := os.ReadFile(crashFile); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		fi, _ := os.Stat(crashFile)
		text := string(data)
		if len(text) > 32<<10 {
			text = text[:32<<10] + "\n... (truncated, full text in the crash file)"
		}
		kv := []string{"crash_report", text, "crash_file", crashFile}
		if fi != nil {
			kv = append(kv, "crashed_at", fi.ModTime().Format(time.DateTime))
		}
		l.Log("error", EventApp, "The program crashed last time: "+firstLine(text), kv...)
		os.Rename(crashFile, strings.TrimSuffix(crashFile, ".txt")+"-"+time.Now().Format("20060102-150405")+".txt")
	}
	if f, err := os.OpenFile(crashFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close() // SetCrashOutput keeps its own copy of the handle
	}
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "unknown error"
}

// Recover is deferred in background goroutines: a panic there is logged with
// its stack trace and that goroutine stops, instead of the whole program
// crashing.
func Recover(l *Logger, what string) {
	if r := recover(); r != nil {
		l.Log("error", EventApp, fmt.Sprintf("Error in %s (recovered, program keeps running): %v", what, r),
			"stack", string(debug.Stack()))
	}
}
