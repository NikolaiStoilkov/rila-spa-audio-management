# rila-spa-audio-management

A Windows tool that plays a different browser on each audio output.
It has two programs:

| Program | Purpose |
|---|---|
| `spa-audio.exe` | Settings window plus a system tray icon. It starts the browsers and keeps each one routed to its audio output. |
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
3. Start `spa-audio.exe`. It opens with one device tab:
   - Click the **+** tab to add another device. Use **Remove this output** inside a tab to delete it.
   - Pick an **Audio output** (optional). **Device ID** is read-only and is filled in from that list.
     To see every output with its hardware ID, click **List all devices**.
   - Pick a **Browser** from the dropdown. The program scans the PC for installed browsers: the ones registered with Windows,
     App Paths, and the usual install folders. Use **Browse...** to add a portable copy, and **Rescan browsers** after installing a new one.
   - Enter the URL and any extra args. **Description** is free text for notes about the device (location, speaker model, ...).
   - Click **Test this output** on each tab to check it, then **Save & Launch all**.
4. Tick **Start automatically when Windows starts**.
   This adds `"C:\SpaAudio\spa-audio.exe" --run` to `HKCU\...\CurrentVersion\Run`. No admin rights are needed.
5. Enable auto-login for the PC (`netplwiz`), so it starts after a power cut without anyone touching it.

## Running in the background

- Closing the window (X) **hides it to the system tray**. The program keeps running.
- Tray icon: left-click opens the window. The right-click menu has **Open**, **Launch all browsers**, **Open log viewer**,
  and **Quit the program** (the only way to exit).
- At Windows startup (`--run`), the program starts hidden in the tray. It waits up to `deviceWaitSec` (120 s) for the devices,
  starts the browsers, keeps them routed, and restarts any browser that gets closed.
- Only one copy runs at a time. Starting the exe again brings the running window to the front.

## Logs

Logs are written to `spa-audio-log.xml`, next to `spa-audio.exe`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<log>
  <entry time="2026-10-19T13:40:01+03:00" level="info" mode="run">Device 1: started chrome.exe (pid 4120)</entry>
</log>
```

- `level` is `info`, `warning` or `error`.
- `mode` is `gui` when the program was started by hand, and `run` when it was started at Windows startup.
- Above 5 MB the file is renamed to `spa-audio-log.old.xml`.

`log-viewer.exe` must be in the same folder. It can filter by level, mode and text, refreshes every 5 s, and can open other log files.
To change its login, put the SHA-256 of `username:password` in `credentialHash` in
[cmd/logviewer/main.go](cmd/logviewer/main.go), then rebuild.
The login only protects the viewer. The XML file itself can be read by anyone with access to the folder.

### Useful extra args

| Browser | Args |
|---|---|
| Chrome / Edge / Brave | `--autoplay-policy=no-user-gesture-required --new-window` |
| Firefox | `-new-window` (set `media.autoplay.default = 0` in `about:config` to allow autoplay) |

## Build

```bash
./build.sh
```

This builds `dist/spa-audio.exe` and `dist/log-viewer.exe` for 64-bit Windows, from macOS, Linux or Windows (Git Bash). It needs Go 1.26+.
To redesign the icon, edit [winres/make_icon.py](winres/make_icon.py), then run `python3 winres/make_icon.py winres`.
