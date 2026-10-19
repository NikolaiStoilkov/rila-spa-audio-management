//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Output is one audio output and the browser that should play on it.
type Output struct {
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	DeviceID string `json:"deviceId"` // optional, e.g. {0.0.0.00000000}.{b3f8a1c2-...}; empty = Windows default
	Browser  string `json:"browser"`  // full path to the browser .exe
	URL      string `json:"url"`
	Args     string `json:"args"` // extra command-line arguments

	Description string `json:"description,omitempty"` // free text notes about this device
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

func logPath() string { return filepath.Join(exeDir(), "spa-audio-log.xml") }

func defaultOutput() Output {
	o := Output{Enabled: true, Name: "Device 1"}
	if list := FindBrowsers(); len(list) > 0 {
		o.Browser = list[0].Path
	}
	return o
}

func defaultConfig() *Config {
	return &Config{DeviceWaitSec: 120, RestartIfClosed: true, Outputs: []Output{defaultOutput()}}
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
	cfg.Outputs = nil
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if len(cfg.Outputs) == 0 {
		cfg.Outputs = []Output{defaultOutput()}
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

// Clone returns a copy that is safe to use from another goroutine.
func (c *Config) Clone() *Config {
	cp := *c
	cp.Outputs = append([]Output(nil), c.Outputs...)
	return &cp
}
