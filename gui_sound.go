//go:build windows

package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"sync/atomic"
	"time"
	"unsafe"

	"rila-spa-audio/internal/applog"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

const (
	soundTickEvery = 150 * time.Millisecond
	loudThreshold  = 0.003                   // peak above this counts as sound
	playingHold    = 1500 * time.Millisecond // keep "playing" through short pauses
	silentAfter    = 10 * time.Second        // log "no sound" after this long
)

// Tab icon indexes in the tab control's image list (-1 = no icon).
const (
	iconNone    int32 = -1
	iconPlaying int32 = 0
	iconIdle    int32 = 1
)

// startSoundDisplay starts the meter thread and the level/icon updates.
func (a *app) startSoundDisplay() {
	a.initTabIcons()
	go meter.run()
	var pending atomic.Bool
	safeGo("sound display", func() {
		for range time.Tick(soundTickEvery) {
			if pending.Swap(true) {
				continue // the window thread has not caught up; skip a frame
			}
			a.mw.Synchronize(func() {
				defer pending.Store(false)
				a.soundTick()
			})
		}
	})
}

func (a *app) soundTick() {
	now := time.Now()
	h := a.mw.Handle()
	visible := win.IsWindowVisible(h) && !win.IsIconic(h)
	procs := processesByPath()

	for i, s := range a.slots {
		browser := a.selectedBrowser(s)
		level := meter.Level(browser)
		running := browser != "" && len(procs[pathKey(browser)]) > 0
		enabled := s.enabled.Checked()
		if level.Peak > loudThreshold {
			s.lastLoud = now
		}
		playing := now.Sub(s.lastLoud) < playingHold

		// Log when a device starts playing and when it goes quiet.
		if enabled && browser != "" {
			dev := a.deviceLabel(s)
			switch {
			case playing && !s.soundOn:
				s.soundOn = true
				audit("info", applog.EventSound, dev+": sound started", "device", dev, "browser", filepath.Base(browser))
			case s.soundOn && now.Sub(s.lastLoud) > silentAfter:
				s.soundOn = false
				why := "browser is open but silent"
				if !running {
					why = "browser is not running"
				}
				audit("warning", applog.EventSound, fmt.Sprintf("%s: no sound for %s (%s)", dev, silentAfter, why),
					"device", dev, "browser", filepath.Base(browser), "browser_running", fmt.Sprint(running))
			}
		}

		if !visible {
			continue
		}
		icon, text := iconNone, "Browser is not running"
		switch {
		case !enabled:
			text = "Disabled"
		case browser == "":
			text = "No browser selected"
		case playing:
			icon, text = iconPlaying, "Playing"
		case running && level.Session:
			icon, text = iconIdle, "Silent - browser is open, nothing is playing"
		case running:
			icon, text = iconIdle, "Silent - browser is open"
		}
		a.setTabIcon(i, icon)

		pct := 0
		if enabled {
			pct = int(math.Sqrt(float64(level.Peak)) * 100) // sqrt: closer to how loud it sounds
		}
		if pct != s.lastPct {
			setBarInstant(s.level, pct)
			s.lastPct = pct
		}
		if text != s.lastText {
			s.soundText.SetText(text)
			color := walk.RGB(110, 110, 110)
			if icon == iconPlaying {
				color = walk.RGB(0, 140, 60)
			}
			s.soundText.SetTextColor(color)
			s.lastText = text
		}
	}
}

// setBarInstant skips the progress bar's slow "growing" animation, which
// would make a sound meter lag behind the music.
func setBarInstant(bar *walk.ProgressBar, v int) {
	if v >= 100 {
		bar.SetValue(100)
		bar.SetValue(99)
		bar.SetValue(100)
		return
	}
	bar.SetValue(v + 1)
	bar.SetValue(v)
}

// ---------- tab icons ----------

// initTabIcons puts the two speaker icons into the tab control's image list
// once. walk adds a new copy to that list on every SetImage call, so the
// icons are then switched with TCM_SETITEM instead of SetImage.
func (a *app) initTabIcons() {
	dpi := 192 // icons are drawn at 32 px = 16 px at 200 % scaling
	on, err1 := walk.NewBitmapFromImageForDPI(speakerImage(true, color.RGBA{0, 150, 70, 255}), dpi)
	idle, err2 := walk.NewBitmapFromImageForDPI(speakerImage(false, color.RGBA{140, 140, 140, 255}), dpi)
	if err1 != nil || err2 != nil {
		return
	}
	a.plusPage.SetImage(on)   // index 0
	a.plusPage.SetImage(idle) // index 1
	a.plusPage.SetImage(nil)
	a.tabHwnd = findChild(a.tabs.Handle(), "SysTabControl32")
}

func (a *app) setTabIcon(index int, icon int32) {
	if a.tabHwnd == 0 {
		return
	}
	item := win.TCITEM{Mask: win.TCIF_IMAGE}
	win.SendMessage(a.tabHwnd, win.TCM_GETITEM, uintptr(index), uintptr(unsafe.Pointer(&item)))
	if item.IImage == icon {
		return
	}
	item = win.TCITEM{Mask: win.TCIF_IMAGE, IImage: icon}
	win.SendMessage(a.tabHwnd, win.TCM_SETITEM, uintptr(index), uintptr(unsafe.Pointer(&item)))
}

func findChild(parent win.HWND, class string) win.HWND {
	for h := win.GetWindow(parent, win.GW_CHILD); h != 0; h = win.GetWindow(h, win.GW_HWNDNEXT) {
		if className(h) == class {
			return h
		}
	}
	return 0
}

// speakerImage draws a 32x32 speaker, with sound waves when waves is true.
func speakerImage(waves bool, c color.RGBA) *image.RGBA {
	const size, ss = 32, 4 // 4x4 supersampling for smooth edges
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	inside := func(x, y float64) bool {
		if x >= 3 && x <= 11 && y >= 11 && y <= 21 { // box
			return true
		}
		if x >= 10 && x <= 18 { // cone: widens from the box to the right
			half := 5 + (x-10)*(8.0/8)
			if math.Abs(y-16) <= half {
				return true
			}
		}
		if waves {
			dx, dy := x-17, y-16
			r := math.Hypot(dx, dy)
			ang := math.Atan2(dy, dx)
			if math.Abs(ang) < 0.95 && dx > 0 && (math.Abs(r-7) < 1.3 || math.Abs(r-12) < 1.3) {
				return true
			}
		}
		return false
	}
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					if inside(float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss) {
						hits++
					}
				}
			}
			if hits > 0 {
				a := uint8(int(c.A) * hits / (ss * ss))
				// premultiplied alpha, as image.RGBA expects
				img.SetRGBA(px, py, color.RGBA{
					uint8(int(c.R) * int(a) / 255), uint8(int(c.G) * int(a) / 255), uint8(int(c.B) * int(a) / 255), a,
				})
			}
		}
	}
	return img
}
