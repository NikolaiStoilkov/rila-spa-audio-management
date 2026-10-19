//go:build windows

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const renderKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\Render`

const (
	propFriendlyName = "{a45c254e-df1c-4efd-8020-67d146a850e0},2"  // "Speakers"
	propAdapterName  = "{b3f8fa53-0004-438e-9003-51a46e139bfc},6"  // "Realtek(R) Audio"
	renderPrefix     = "{0.0.0.00000000}."
	deviceActive     = 1
)

type Device struct {
	ID      string // {0.0.0.00000000}.{guid}
	Name    string
	Adapter string
	State   uint64
}

func (d Device) Label() string {
	s := d.Name
	if d.Adapter != "" {
		s += " (" + d.Adapter + ")"
	}
	return s
}

func stateText(state uint64) string {
	switch state {
	case 1:
		return "active"
	case 2:
		return "disabled"
	case 4:
		return "not present"
	case 8:
		return "unplugged"
	}
	return fmt.Sprintf("state %d", state)
}

// ListRenderDevices reads all output endpoints Windows knows about from the registry.
func ListRenderDevices() ([]Device, error) {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, renderKey, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}

	var devices []Device
	for _, guid := range names {
		d := Device{ID: renderPrefix + strings.ToLower(guid)}
		if k, err := registry.OpenKey(root, guid, registry.QUERY_VALUE); err == nil {
			d.State, _, _ = k.GetIntegerValue("DeviceState")
			k.Close()
		}
		if k, err := registry.OpenKey(root, guid+`\Properties`, registry.QUERY_VALUE); err == nil {
			d.Name, _, _ = k.GetStringValue(propFriendlyName)
			d.Adapter, _, _ = k.GetStringValue(propAdapterName)
			k.Close()
		}
		devices = append(devices, d)
	}

	sort.Slice(devices, func(i, j int) bool {
		if (devices[i].State == deviceActive) != (devices[j].State == deviceActive) {
			return devices[i].State == deviceActive
		}
		return devices[i].Label() < devices[j].Label()
	})
	return devices, nil
}

func ActiveDevices() ([]Device, error) {
	all, err := ListRenderDevices()
	var active []Device
	for _, d := range all {
		if d.State == deviceActive {
			active = append(active, d)
		}
	}
	return active, err
}

var (
	fullIDRe = regexp.MustCompile(`(?i)\{0\.0\.0\.00000000\}\.\{[0-9a-f-]{36}\}`)
	guidRe   = regexp.MustCompile(`(?i)\{?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\}?`)
)

// NormalizeID accepts a full endpoint ID, a bare GUID, or a SWD\MMDEVAPI path
// and returns the canonical {0.0.0.00000000}.{guid} form.
func NormalizeID(s string) string {
	s = strings.TrimSpace(s)
	if m := fullIDRe.FindString(s); m != "" {
		return strings.ToLower(m)
	}
	if m := guidRe.FindStringSubmatch(s); m != nil {
		return renderPrefix + "{" + strings.ToLower(m[1]) + "}"
	}
	return s
}

func FindDevice(devices []Device, id string) (Device, bool) {
	id = NormalizeID(id)
	for _, d := range devices {
		if strings.EqualFold(d.ID, id) {
			return d, true
		}
	}
	return Device{}, false
}
