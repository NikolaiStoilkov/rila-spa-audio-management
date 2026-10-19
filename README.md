# rila-spa-audio-management

A Windows tool that plays a different browser on each audio output (for example 4 rooms with 4 speakers).
It has a GUI for entering the hardware (endpoint) IDs, and a background mode that runs when Windows starts.

## How it works

- Windows identifies every output by an **endpoint ID**: `{0.0.0.00000000}.{GUID}`.
  These are stored under `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\Render\{GUID}`.
  The ID stays the same across reboots, but it **changes if a USB sound card is moved to another USB port**.
- Windows 10 (1803+) and Windows 11 can route audio **per application (per .exe path)**.
  The program sets this with NirSoft `svcl.exe /SetAppDefault <deviceId> all <pid>` for every process of each browser.
  It keeps checking every 5 s, because browsers start new helper processes later.
- **Each output needs its own browser .exe.** Two outputs that use the same `chrome.exe` will play on the same device.
  Use different browsers (Chrome, Edge, Firefox, Brave) or a portable copy in another folder.

## Install on the Windows PC

1. Create `C:\SpaAudio\` and copy `dist\spa-audio.exe` into it.
2. Download **SoundVolumeCommandLine** (`svcl.exe`, 64-bit) from https://www.nirsoft.net/utils/sound_volume_command_line.html
   and put `svcl.exe` in the same folder.
3. (Recommended) In *Settings → System → Sound*, rename each output, e.g. "Room 1".
4. Start `spa-audio.exe`:
   - Click **Refresh devices**.
   - Each output has its own tab. Pick the device in **Detected device**.
     **Device ID** is read-only and is filled in from that list (use **Clear** to empty it).
     The status line shows whether the device is active.
     To enter an ID by hand, edit `deviceId` in `config.json`. A bare GUID, the full ID, or a `SWD\MMDEVAPI\...` path are all accepted.
   - Pick the browser .exe, the URL, and any extra args.
   - Click **Test this output** on each tab to check it, then **Save & Launch all**.
5. Tick **Start automatically when Windows starts**.
   This adds `"C:\SpaAudio\spa-audio.exe" --run` to `HKCU\...\CurrentVersion\Run`. No admin rights are needed.
6. Enable auto-login for the PC (`netplwiz`), so it starts after a power cut without anyone touching it.

At logon, `--run` waits up to `deviceWaitSec` (120 s) for all devices, starts the browsers,
keeps them routed, and restarts any browser that gets closed. Everything is logged to `spa-audio.log` next to the exe.

### Useful extra args

| Browser | Args |
|---|---|
| Chrome / Edge / Brave | `--autoplay-policy=no-user-gesture-required --new-window` |
| Firefox | `-new-window` (set `media.autoplay.default = 0` in `about:config` to allow autoplay) |

## Files

| File | Purpose |
|---|---|
| `config.json` | Saved settings, next to the exe (you can edit it by hand) |
| `spa-audio.log` | Log |
| `svcl.exe` | NirSoft tool used for per-app routing (download it separately) |

## Build

```bash
./build.sh
```

This cross-compiles `dist/spa-audio.exe` for 64-bit Windows from macOS, Linux or Windows (Git Bash). It needs Go 1.26+.
