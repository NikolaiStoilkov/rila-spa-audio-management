//go:build windows

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func main() {
	run := flag.Bool("run", false, "headless mode: wait for devices, start browsers, keep routing (used at Windows startup)")
	flag.Parse()

	logFile, err := os.OpenFile(filepath.Join(exeDir(), "spa-audio.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	cfg, err := LoadConfig()
	if err != nil {
		fatal("Cannot read config.json: " + err.Error())
	}

	if *run {
		runHeadless(cfg)
		return
	}
	runGUI(cfg)
}

func runHeadless(cfg *Config) {
	// Only one background instance at a time.
	name, _ := windows.UTF16PtrFromString(`Local\RilaSpaAudioRun`)
	if _, err := windows.CreateMutex(nil, false, name); err == windows.ERROR_ALREADY_EXISTS {
		log.Println("Already running, exiting")
		return
	}

	logf := func(format string, args ...any) { log.Printf(format, args...) }
	r := NewRunner(cfg, logf)
	logf("=== Startup ===")
	for _, p := range r.Check() {
		logf("WARNING: %s", p)
	}
	r.WaitForDevices(time.Duration(cfg.DeviceWaitSec) * time.Second)
	r.LaunchAll()
	r.Enforce(cfg.RestartIfClosed) // runs until the user logs off
}

func fatal(msg string) {
	log.Println(msg)
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("Rila Spa Audio")
	windows.MessageBox(0, text, title, windows.MB_ICONERROR)
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
