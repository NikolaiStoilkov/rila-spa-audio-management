//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// Autostart uses the per-user Run key: no admin rights needed, and it runs
// in the logged-on user's desktop session (a Windows service could not open
// browser windows or change that user's audio routing).
const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "RilaSpaAudio"
)

func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `" --run`, nil
}

func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func SetAutostart(enabled bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	cmd, err := autostartCommand()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, cmd)
}
