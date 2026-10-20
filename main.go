//go:build windows

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"unsafe"

	"rila-spa-audio/internal/applog"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func main() {
	run := flag.Bool("run", false, "start hidden in the system tray, wait for devices, start browsers, keep routing (used at Windows startup)")
	flag.Parse()

	mode := "gui"
	if *run {
		mode = "run"
	}
	auditLog = applog.New(logPath(), mode)
	defer auditLog.Flush()
	log.SetFlags(0) // the XML entry carries the timestamp
	log.SetOutput(auditLog)
	auditLog.Version = appVersion
	// Fatal errors go to a raw file first and become a readable report in
	// log-viewer-crash.txt at the next start (older versions used spa-audio-crash.txt).
	applog.CaptureCrashes(auditLog, filepath.Join(exeDir(), "spa-audio-crash-raw.txt"),
		filepath.Join(exeDir(), "spa-audio-crash.txt"))
	defer logPanics()

	// Dedicated audio PC: keep routing and the sound meter responsive even
	// when the browsers keep the CPU busy, and trade some memory for less
	// garbage-collection work.
	windows.SetPriorityClass(windows.CurrentProcess(), windows.ABOVE_NORMAL_PRIORITY_CLASS)
	debug.SetGCPercent(200)

	cfg, err := LoadConfig()
	if err != nil {
		fatal("Cannot read config.json: " + err.Error())
	}

	// One instance only. Starting the exe again brings the running one to the front.
	name, _ := windows.UTF16PtrFromString(`Local\RilaSpaAudioApp`)
	if _, err := windows.CreateMutex(nil, false, name); err == windows.ERROR_ALREADY_EXISTS {
		if !*run {
			showRunningInstance()
			audit("info", applog.EventApp, "Program was started again while already running - showed the open window")
		} else {
			audit("info", applog.EventApp, "Windows startup skipped - program already running")
		}
		return
	}

	startSession(mode, cfg)
	registerCrashRestart()
	runGUI(cfg, *run)
}

func showRunningInstance() {
	title, _ := windows.UTF16PtrFromString(appTitle)
	if h := win.FindWindow(nil, title); h != 0 {
		win.ShowWindow(h, win.SW_SHOW)
		if win.IsIconic(h) {
			win.ShowWindow(h, win.SW_RESTORE)
		}
		win.SetForegroundWindow(h)
	}
}

func fatal(msg string) {
	log.Println(msg)
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("Rila Spa Audio")
	windows.MessageBox(0, text, title, windows.MB_ICONERROR)
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// registerCrashRestart asks Windows to start the program again (hidden in
// the tray, --run) if it crashes or hangs after running for at least 60 s.
// A normal "Quit the program" is not restarted.
func registerCrashRestart() {
	const restartNoReboot = 0x8 // after a reboot the autostart entry handles it
	args, _ := windows.UTF16PtrFromString("--run")
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("RegisterApplicationRestart")
	if hr, _, _ := proc.Call(uintptr(unsafe.Pointer(args)), restartNoReboot); hr != 0 {
		log.Printf("Automatic restart after a crash is not available (code %#x)", hr)
	}
}
