//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type Browser struct {
	Name string
	Path string
}

func (b Browser) Label() string { return b.Name + "  (" + b.Path + ")" }

// FindBrowsers scans the PC for installed browsers: the browsers registered
// with Windows (StartMenuInternet), App Paths, and common install folders.
func FindBrowsers() []Browser {
	found := map[string]Browser{} // lower-case path -> browser
	add := func(name, path string) {
		path = strings.Trim(strings.TrimSpace(path), `"`)
		if expanded, err := registry.ExpandString(path); err == nil {
			path = expanded // %ProgramFiles%\... style paths
		}
		if path == "" || !strings.EqualFold(filepath.Ext(path), ".exe") {
			return
		}
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			return
		}
		key := strings.ToLower(filepath.Clean(path))
		if old, ok := found[key]; ok && old.Name != "" {
			return
		}
		if name == "" {
			name = browserName(path)
		}
		found[key] = Browser{Name: name, Path: filepath.Clean(path)}
	}

	// 1. Browsers registered with Windows (what "Default apps" lists).
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, base := range []string{`SOFTWARE\Clients\StartMenuInternet`, `SOFTWARE\WOW6432Node\Clients\StartMenuInternet`} {
			k, err := registry.OpenKey(root, base, registry.ENUMERATE_SUB_KEYS)
			if err != nil {
				continue
			}
			subs, _ := k.ReadSubKeyNames(-1)
			k.Close()
			for _, sub := range subs {
				name := readString(root, base+`\`+sub, "")
				cmd := readString(root, base+`\`+sub+`\shell\open\command`, "")
				add(name, exeFromCommand(cmd))
			}
		}
	}

	// 2. App Paths and 3. common install folders.
	exes := map[string]string{
		"chrome.exe":   `Google\Chrome\Application\chrome.exe`,
		"msedge.exe":   `Microsoft\Edge\Application\msedge.exe`,
		"firefox.exe":  `Mozilla Firefox\firefox.exe`,
		"brave.exe":    `BraveSoftware\Brave-Browser\Application\brave.exe`,
		"opera.exe":    `Opera\opera.exe`,
		"vivaldi.exe":  `Vivaldi\Application\vivaldi.exe`,
		"chromium.exe": `Chromium\Application\chrome.exe`,
	}
	for exe, rel := range exes {
		for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
			add("", readString(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+exe, ""))
		}
		for _, dir := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")} {
			if dir != "" {
				add("", filepath.Join(dir, rel))
			}
		}
	}

	var list []Browser
	for _, b := range found {
		list = append(list, b)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].Path < list[j].Path
	})
	return list
}

func readString(root registry.Key, path, value string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	s, _, _ := k.GetStringValue(value)
	return s
}

// exeFromCommand extracts the program from `"C:\...\chrome.exe" --flag`.
func exeFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : end+1]
		}
	}
	if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		return cmd[:i+4]
	}
	return cmd
}

func browserName(path string) string {
	switch strings.ToLower(filepath.Base(path)) {
	case "chrome.exe":
		if strings.Contains(strings.ToLower(path), "chromium") {
			return "Chromium"
		}
		return "Google Chrome"
	case "msedge.exe":
		return "Microsoft Edge"
	case "firefox.exe":
		return "Mozilla Firefox"
	case "brave.exe":
		return "Brave"
	case "opera.exe":
		return "Opera"
	case "vivaldi.exe":
		return "Vivaldi"
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
