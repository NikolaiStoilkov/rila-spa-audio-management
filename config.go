//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Output is one audio output (room) and the browser that should play on it.
type Output struct {
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	DeviceID string `json:"deviceId"` // e.g. {0.0.0.00000000}.{b3f8a1c2-...}
	Browser  string `json:"browser"`  // full path to the browser .exe
	URL      string `json:"url"`
	Args     string `json:"args"` // extra command-line arguments
}

type Config struct {
	Outputs []Output `json:"outputs"`

	// Path to NirSoft svcl.exe. Empty = svcl.exe next to this program.
	SvclPath string `json:"svclPath"`

	// How long --run waits for all audio devices to appear after logon.
	DeviceWaitSec int `json:"deviceWaitSec"`

	// Relaunch a browser if it was closed (only in --run mode).
	RestartIfClosed bool `json:"restartIfClosed"`
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func configPath() string { return filepath.Join(exeDir(), "config.json") }

func defaultConfig() *Config {
	browsers := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Mozilla Firefox\firefox.exe`,
		`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
	}
	cfg := &Config{DeviceWaitSec: 120, RestartIfClosed: true}
	for i, b := range browsers {
		cfg.Outputs = append(cfg.Outputs, Output{
			Enabled: true,
			Name:    "Room " + string(rune('1'+i)),
			Browser: b,
		})
	}
	return cfg
}

func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	for len(cfg.Outputs) < 4 {
		cfg.Outputs = append(cfg.Outputs, Output{Name: "Room " + string(rune('1'+len(cfg.Outputs)))})
	}
	return cfg, nil
}

func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0o644)
}

func (c *Config) svcl() string {
	if c.SvclPath != "" {
		return c.SvclPath
	}
	return filepath.Join(exeDir(), "svcl.exe")
}
