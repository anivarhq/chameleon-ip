# Chameleon IP

Turn a phone, tablet, Mac or PC into an IP camera.

It speaks standard RTSP and ONVIF, so any NVR can add it — [Anivar](https://github.com/anivarhq/anivar), Frigate, Blue Iris, Synology, Home Assistant — and most of them find it on the network by themselves.

## Downloads

Every platform is built by GitHub Actions on each change. Open the latest run and take the artifact:

| Platform | Workflow | What you get |
|---|---|---|
| Android | [android](../../actions/workflows/android.yml) | `.apk` to install |
| iOS, iPadOS, macOS | [apple](../../actions/workflows/apple.yml) | app built for the simulator and for macOS |
| Windows, macOS, Linux desktop | [flutter](../../actions/workflows/flutter.yml) | the app, with its engine beside it |
| Headless engine | [desktop](../../actions/workflows/desktop.yml) | one binary per OS and architecture, no interface |

## How it is built

```
                      core/  (Go, shared by everything)
        RTSP server · ONVIF service · WS-Discovery · Digest auth
         ▲                     ▲                      ▲
   Android (Kotlin)      Apple (Swift)          Desktop (Go)
   Camera2 → MediaCodec  AVCapture → VideoToolbox  ffmpeg → NVENC/QSV/AMF
                                                   with a Flutter window
```

Capture and encoding are native on each platform, because the camera has to write straight into the hardware encoder and no cross-platform framework exposes that. Everything after the encoder — the protocols, where the bugs live — is one Go package that every platform links.

That decision was made with evidence, not taste: the research is in [docs/research.md](docs/research.md), and the measurements that settled it are in [§7](docs/research.md#7-the-shared-core-option). Without it, the login and discovery code would exist three times, and iOS would need an RTSP server written from scratch, since no maintained open-source one exists.

## What a device serves

- **RTSP on port 8554.** H.264, 1280×720 at 15 fps, about 2 Mbps, a keyframe every 2 s. Digest authentication.
- **ONVIF on port 8000**, plus discovery, so NVRs find it and read its real resolution and frame rate.
- **A username and password generated on the device.** Nothing is served without them. They live in the Android keystore, the Apple keychain, or a DPAPI-sealed file on Windows.
- **Your network only.** No cloud, no account, and only private addresses are accepted.

It encodes only while someone is connected, gives each new viewer a keyframe immediately, and stamps frames with the time they were captured, so recordings don't drift.

## Running it

**Android:** install the APK, tap Start camera, and it shows an address like `rtsp://admin:…@192.168.1.20:8554/main`. Type that into your NVR, or let the NVR find it. The screen can go off.

**iPhone or iPad:** the app has to stay open — iOS stops the camera the moment it leaves the screen — so it goes almost black instead. Guided Access locks the phone to it.

**Desktop:** run the app, pick a camera, press start. The headless engine is the same thing without a window:

```
chameleon -list          # cameras on this machine
chameleon                # start streaming, print the address
chameleon -rotate-password
```

## Known limits

- **iOS needs Apple's multicast entitlement** before NVRs can discover it automatically; until then, add it by address.
- **Windows already runs its own discovery service** on the same port, so a Windows desktop camera answers direct probes but not broadcast ones. Anivar sends direct probes; other NVRs may need the address typed.
- **Android cannot restart itself after a reboot** (Android 15 and later). Tap the notification to resume.
- **A webcam's own limits apply.** The test machine's built-in camera only offers raw formats at 720p, so it delivers about 11 fps, not 15.
- **Audio is not wired up yet**, though the core is ready for it.

## Layout

- `core/` — the shared Go core, plus the desktop engine and a file-fed test harness
- `android/` — Kotlin app
- `apple/` — iOS, iPadOS and macOS app; the Xcode project is generated from `project.yml`
- `flutter/` — the desktop window
- `scripts/` — build the core for Android and Apple

## License

[Apache-2.0](LICENSE)
