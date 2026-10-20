# rila-spa-audio-management

A Windows tool that switches the sound of each (already open) browser to its own audio output.

📖 User guide in Bulgarian: [docs/user-guide-bg.md](docs/user-guide-bg.md) (copied into `dist/` by the build).
It has two programs:

| Program | Purpose |
|---|---|
| `spa-audio.exe` | Settings window plus a system tray icon. It keeps each open browser routed to its audio output (it never starts browsers). |
| `log-viewer.exe` | Shows `spa-audio-log.xml`. It requires a login (default **admin / admin123**). |

## How it works

- Windows identifies every output by an **endpoint (hardware) ID**: `{0.0.0.00000000}.{GUID}`.
  These are stored under `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\Render\{GUID}`.
  The ID stays the same across reboots, but it **changes if a USB sound card is moved to another USB port**.
- Windows 10 (1803+) and Windows 11 can route audio **per application (per .exe path)**.
  The program sets this with NirSoft `svcl.exe /SetAppDefault <deviceId> all <pid>` for every process of each browser.
  It keeps checking every 5 s, because browsers start new helper processes later.
- **The audio output is optional.** A device without one plays on the Windows default output.
- **Each device needs its own browser .exe.** Two devices that use the same `chrome.exe` will play on the same output.
  Use different browsers (Chrome, Edge, Firefox, Brave) or a portable copy in another folder.

## Install on the Windows PC

1. Create `C:\SpaAudio\` and copy `dist\spa-audio.exe` and `dist\log-viewer.exe` into it.
2. Download **SoundVolumeCommandLine** (`svcl.exe`, 64-bit) from https://www.nirsoft.net/utils/sound_volume_command_line.html
   and put `svcl.exe` in the same folder.
3. Start `spa-audio.exe`. It opens with one device tab. On later starts, the first device with an active audio output is selected:
   - Click the **+** tab to add another device. Use **Remove this output** inside a tab to delete it.
   - Pick an **Audio output** (optional). **It is saved to config.json immediately** (only that field) and applied to the running browser.
     There's no need to press Save. **Device ID** is read-only and is filled in from that list.
     To see every output with its hardware ID, click **List all devices**.
   - Pick a **Browser** from the dropdown. The program scans the PC for installed browsers: the ones registered with Windows,
     App Paths, and the usual install folders. Use **Browse...** to add a portable copy, and **Rescan browsers** after installing a new one.
   - **Sound** shows a live level meter for that browser (the same as the green bar in the Windows volume mixer).
     The tab shows a **green speaker** while that device is playing, and a gray one when the browser is open but silent.
   - **Volume** sets that browser's volume (the same as its slider in the Windows volume mixer). Changes apply right away.
     The program reapplies it every 30 s and at startup, so it holds even when the page starts playing later.
   - **Description** is free text for notes about the device (location, speaker model, ...).
   - Click **Apply to this browser** on each tab to check it, then **Save & Apply**.
4. Tick **Start automatically when Windows starts**.
   This adds `"C:\SpaAudio\spa-audio.exe" --run` to `HKCU\...\CurrentVersion\Run`. No admin rights are needed.
5. Enable auto-login for the PC (`netplwiz`), so it starts after a power cut without anyone touching it.

## Performance and crash reports

- **Background work:**
  - The sound meter ([meter.go](meter.go)) reads the Windows audio session meters (WASAPI via go-wca) on its own thread, 10 times a second.
  - The window redraws levels about 7 times a second, and skips frames if it is busy.
- **Shared process list:** one snapshot of running processes per second, shared by routing, the meter and the GUI.
- **Parallel work:** devices are routed in parallel, and log writes are batched on a background goroutine.
- **Priority:** the program runs at *above normal* priority, so routing and the meter stay responsive while browsers use the CPU.
- **Log viewer:** reads only the new part of the log, in the background. Large logs are parsed on all CPU cores.
- **Crash reports:** both programs append readable reports to **`log-viewer-crash.txt`** next to the exe ([internal/applog/crash.go](internal/applog/crash.go)).
  The log viewer shows them in its **Crash reports** tab, newest first.
  - Each report has the time, program and version, Windows user, the cause in plain words, the raw error, and **where** (file, line, function).
  - It also lists the last 25 log entries (**what happened just before**) and the full stack.
  - Crashes on the window thread are reported, then the program closes, and Windows restarts it.
  - Errors in window handlers (buttons, slider, lists) and background goroutines are reported, and the program keeps running.
  - Fatal errors that `recover` cannot catch go to `spa-audio-crash-raw.txt` / `log-viewer-crash-raw.txt` (`debug.SetCrashOutput`) and become a report at the next start. The older `spa-audio-crash.txt` is imported too.
- **Tab icons:** set with `TCM_SETITEM` on two images added once. walk's `TabPage.SetImage` adds a new copy to the image list on every call, which would leak.

## Running in the background

- Closing the window (X) **hides it to the system tray**. The program keeps running.
- Tray icon: left-click opens the window. The right-click menu has **Open**, **Apply audio routing now**, **Open log viewer**,
  and **Quit the program** (the only way to exit).
- At Windows startup (`--run`), the program starts hidden in the tray. It waits up to `deviceWaitSec` (120 s) for the devices,
  then switches the open browsers to their outputs.
- **The program never starts, closes or restarts browsers.** People open them as usual. While the program runs (window or tray), it switches every open browser, and any browser opened later, to its output within about 5 s.
- **Save & Apply** saves, applies the routing immediately, and shows a summary of which browser went to which output.
- If the program crashes or hangs, Windows restarts it in the tray (`RegisterApplicationRestart`, after at least 60 s of running).
- Only one copy runs at a time. Starting the exe again brings the running window to the front.

## Logs

Logs are **not shown in spa-audio.exe**. They are written to `spa-audio-log.xml`, next to the exe, and read with `log-viewer.exe`.
The code is in [internal/applog](internal/applog) (shared by both programs) and [audit.go](audit.go) / [gui_audit.go](gui_audit.go).

```xml
<entry time="2026-10-19T13:40:01.123+03:00" level="info" event="action" mode="gui" session="3f9a1c2e" user="SPA-PC\reception">
  <msg>Clicked "Apply to this browser" on 2. Pool</msg>
  <data name="count_session">3</data>
  <data name="count_total">41</data>
  <data name="cursor_screen">812,644</data>
  <data name="cursor_window">804,612</data>
</entry>
```

| event | Logged |
|---|---|
| `app` | Start (user, computer, Windows version, exe, outputs), stop with reason and run time, a second start attempt, crashes with stack trace, and **unclean ends** (killed, crash, power loss). These are detected at the next start from the leftover `spa-audio.session` heartbeat file. |
| `action` | Every button, tray menu and tray icon click: control, device tab, click count this session and all time, cursor position, window state. Also Yes/No answers to questions. |
| `change` | Setting edits with old → new value (text fields when focus leaves them), autostart, removed devices, unsaved changes lost on quit. |
| `save` | Each Save with the list of changes since the last save. |
| `mouse` | Every mouse click inside the program (thread-local hook): button, control under the cursor (including the title bar and the X button), screen and window coordinates. |
| `window` / `tab` | Window opened, hidden to tray, minimized/maximized/restored, focus gained/lost, device list opened/closed, tab switches. |
| `system` | Windows shutdown/restart/logoff, sleep and resume. |
| `runner` | Audio routing per browser process, volume, Save & Apply summaries. |
| `volume` | Volume slider / % box: start of each change, the applied value (from → to, steps, browser processes, svcl result and timing), and invalid states. Invalid states also get a crash report. |
| `sound` | A device started playing, or has been silent for 10 s (e.g. a radio stream dropped). |
| `login` / `viewer` | Log viewer logins (successful or failed, with the username tried, never the password), lockout, opened/closed. |

Keystrokes and passwords are never recorded.
All-time click counts are also kept in `spa-audio-stats.json`, so they survive log rotation. Above 5 MB the log is renamed to `spa-audio-log.old.xml`.

`log-viewer.exe` must be in the same folder. It has two tabs:
- **Log:** filters by level, event and text; double-click a row to see all its fields.
- **Click statistics:** click counts per control.

It refreshes every 5 s. To change its login, put the SHA-256 of `username:password` in `credentialHash` in
[cmd/logviewer/main.go](cmd/logviewer/main.go), then rebuild.
The login only protects the viewer. The XML file itself can be read by anyone with access to the folder.

## Build

```bash
./build.sh
```

This builds `dist/spa-audio.exe` and `dist/log-viewer.exe` for 64-bit Windows, from macOS, Linux or Windows (Git Bash). It needs Go 1.26+.
To redesign the icon, edit [winres/make_icon.py](winres/make_icon.py), then run `python3 winres/make_icon.py winres`.
