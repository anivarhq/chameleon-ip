# Chameleon IP

Turn a phone, tablet, Mac or PC into an IP camera.

It speaks standard RTSP and ONVIF, so any NVR can add it. [Anivar](https://github.com/anivarhq/anivar) finds it on the network and adds it in one click.

**Status:** design only. No code yet.

## How it works

```
                  chameleon-core  (Rust, shared via UniFFI)
    RTSP server · ONVIF responder · digest auth · settings/state
         ▲ encoded video + audio      ▲                     ▲
    Android (Kotlin)           Apple (Swift, one app)    Windows (Rust)
    Camera2 → MediaCodec       AVCapture → VideoToolbox  ffmpeg → NVENC/QSV/AMF
    Jetpack Compose            SwiftUI iOS/iPadOS/macOS  Tauri tray app
```

A camera runs all day, often on an old phone, so the video path decides everything. On each platform the camera writes straight into the hardware encoder and the CPU never touches a pixel. That code has to be native.

The network protocols are where the bugs hide, so they are written once, in Rust, and shared by every app.

| Platform | Capture → encode | UI | Keeps running |
|---|---|---|---|
| Android | Camera2 → MediaCodec input surface | Jetpack Compose | Camera foreground service, screen off |
| iOS / iPadOS | AVCaptureSession → VideoToolbox | SwiftUI | App stays open, screen black (Guard) |
| macOS | Same Swift code as iOS | SwiftUI menu bar app | Menu bar, starts at login |
| Windows | ffmpeg capture → hardware encoder | Tauri tray | Tray, starts at login |

## What a device serves

- **RTSP on port 8554.** H.264, 1080p at 15 fps, about 2 Mbps, a keyframe every 2 s. HEVC is optional. Audio is AAC, mono, 16 kHz.
- **ONVIF on port 8000.** WS-Discovery, GetDeviceInformation, GetProfiles and GetStreamUri, with digest auth.
- **A username and password made on first run.** Nothing is served without them.
- **Your network only.** No cloud, no account.

It only encodes while a viewer is connected. Each new viewer gets a keyframe straight away. Timestamps come from the camera clock and never reset, so recordings don't stall at segment edges.

## Screens

The same four on every platform. Dark only.

1. **First run.** "This device is now a camera." One button, which asks for camera and microphone access.
2. **Live.** Full-screen preview for aiming, one status line, and flip / microphone / light. "Add to an NVR" shows the IP, stream URL, login and a QR code.
3. **Guard.** After 30 s without a touch the screen goes black, with one dim status line. Tap to wake.
4. **Settings.** Quality (Auto / 1080p / 720p), microphone, start at login, login details. Codec, port and frame rate sit under Advanced.

Auto quality follows heat: when the device runs hot it drops to 10 fps, then to 720p, and says so.

## Platform limits

- **iOS stops the camera when the app leaves the screen.** Chameleon stays open with a black screen instead. Guided Access can lock the phone to it.
- **iOS needs Apple's multicast entitlement for network discovery.** Until it is granted, add an iPhone by its IP address.
- **Android keeps streaming with the screen off**, but on Android 15 and later it cannot restart itself after a reboot. Tap the notification to resume.
- **A phone on a charger all day can swell.** Turn on the phone's 80% charge limit if it has one.

## Build order

0. `core/`: RTSP and ONVIF, tested with ffprobe and an ONVIF client against a stream read from a file.
1. `android/`
2. `apple/`: iOS, iPadOS and macOS
3. `windows/`

## License

[Apache-2.0](LICENSE)
