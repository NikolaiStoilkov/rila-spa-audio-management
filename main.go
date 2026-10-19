//go:build windows

package main

import (
	"flag"
	"fmt"
	"log"
	"os"

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
	log.SetFlags(0) // the XML entry carries the timestamp
	log.SetOutput(auditLog)
	defer logPanics()

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
