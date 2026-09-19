# Chameleon IP

Turn a phone, tablet, Mac or PC into an IP camera.

It speaks standard RTSP and ONVIF, so any NVR can add it: [Anivar](https://github.com/anivarhq/anivar), Frigate, Blue Iris, Synology, Home Assistant.

**Status:** design and research done, no app code yet. The research, with sources, is in [docs/research.md](docs/research.md).

## How it is built

| Platform | Capture → hardware encode | Serves | Keeps running |
|---|---|---|---|
| Android | Kotlin, CameraX + MediaCodec | [RootEncoder / RTSP-Server](https://github.com/pedroSG94/RTSP-Server) | Camera foreground service, screen off |
| iOS / iPadOS | Swift, AVFoundation + VideoToolbox | a custom RTSP module | App open, screen black (Guard) |
| macOS | the same Swift code | the same module | Menu bar, starts at login |
| Windows / Linux | GStreamer or FFmpeg | `gst-rtsp-server` | Tray, starts at login |

The UI is **Flutter**, one codebase for every platform. It talks to each platform's native code through platform channels to start, stop and configure the stream, and shows the preview through a native texture, so camera frames never travel through Dart.

Capture and encoding stay native because the camera must write straight into the hardware encoder; no cross-platform framework exposes that. See [docs/research.md](docs/research.md#1-is-there-a-universal-framework).

### Known hard parts

- **iOS has no RTSP server to reuse.** HaishinKit does RTMP, SRT and WHIP, not RTSP, and no maintained open-source iOS RTSP server exists. That module is written from scratch.
- **RootEncoder's RTSP server authenticates with Basic only**, which puts the password on the wire in readable form, and it has no ONVIF.
- **FFmpeg cannot serve RTSP.** Its listen mode accepts one publisher and cannot serve players.
- **ONVIF, WS-Discovery and Digest auth would be written once per platform.** A single shared server core, called from the same Flutter app, would avoid that; the research covers that option in full.

## What a device serves

- **RTSP on port 8554.** H.264, 1080p at 15 fps, about 2 Mbps, a keyframe every 2 s. HEVC optional. AAC audio, mono, 16 kHz.
- **ONVIF on port 8000.** Discovery plus the handful of calls NVRs actually make.
- **A username and password generated on first run.** Nothing is served without them.
- **Your network only.** No cloud, no account.

It encodes only while a viewer is connected, gives each new viewer a keyframe at once, and never resets timestamps, so recordings don't stall at segment edges.

## Screens

The same four on every platform, dark only.

1. **First run.** "This device is now a camera", one button, then the camera and microphone prompts.
2. **Live.** Full-screen preview for aiming, one status line, and flip / microphone / light. "Add to an NVR" shows the address, stream URL, login and a QR code.
3. **Guard.** After 30 s untouched the screen goes black with one dim status line. Tap to wake.
4. **Settings.** Quality (Auto / 1080p / 720p), microphone, start at login, login details. Codec, port and frame rate under Advanced.

Auto quality follows temperature: when the device runs hot it drops to 10 fps, then 720p, and says so.

## Platform limits

- **iOS stops the camera when the app leaves the screen**, so Chameleon stays open with a black screen. Guided Access can lock the phone to it.
- **iOS needs Apple's multicast entitlement** to be found automatically. Until it is granted, add the iPhone by its address; Anivar also probes each address directly.
- **Android keeps streaming with the screen off**, but from Android 15 it cannot restart itself after a reboot, and from Android 17 it needs the local network permission even to accept connections.
- **Windows** needs a firewall rule, which the installer adds.
- **A phone charging all day can swell.** Turn on the phone's 80% charge limit if it has one.

## Build order

1. `android/` — the first working camera
2. `apple/` — iOS, iPadOS and macOS
3. `windows/`
4. `flutter/` — the shared UI

## License

[Apache-2.0](LICENSE)
