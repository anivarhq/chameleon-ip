# Changelog

All notable changes to Chameleon IP. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.1.0] - 2026-09-28

The first release. Chameleon IP turns a phone, tablet, Mac or PC into a
camera. Turn it on and the picture is right there on the device. If you want
to record it or watch it somewhere else, it speaks standard RTSP and ONVIF, so
Anivar, Frigate, Blue Iris, VLC or any recorder can add it.

### What has been tested, and where

- **Windows:** the engine and its live picture, on a Windows 11 laptop's
  webcam; the window through its automated tests on every change.
- **Android:** built, signed and run on an Android 14 emulator on every
  change: the camera turns on, the picture shows, and the stream plays as
  H.264 1280×720 with its password. **Not yet run on a phone.** That test
  found, and this release fixes, a stream no player could decode (frames
  were read after the app had reused their memory) and discovery that never
  reached the phone.
- **macOS and Linux desktop:** built and packaged on every change, **not yet
  run** on a Mac or a Linux desktop.
- **iPhone and iPad:** built on every change, but not offered as a download:
  that needs an Apple developer account. Build it from source.

### In this release

- **The camera's live picture in the app**, on every platform. Sharing it is
  a separate, optional "Use with a recorder" step.
- **Android runs in landscape**, so the picture on the screen is the one
  recorders get.
- **RTSP** on port 8554: H.264, 1280×720 at 15 fps, about 2 Mbps, with Digest
  authentication. **ONVIF** on port 8000, with discovery, so recorders can find
  it and read its real settings.
- **A username and password made on the device**, kept in the Android
  keystore, the Apple keychain, or a DPAPI-sealed file on Windows.
- **Your network only:** connections from outside the local network (and
  Tailscale) are refused before any protocol runs. Password guessing is slowed
  per address, never by locking the account.
- **Desktop uses the hardware encoder** (NVENC, Quick Sync, AMF) through
  ffmpeg, which you install once. Without it, the app says so and tells you
  how.

### Installing

- **Windows:** unzip, then run `Chameleon IP\chameleon_ip.exe`. It is not
  code-signed yet, so Windows says "Windows protected your PC": **More info →
  Run anyway**. Needs ffmpeg: `winget install Gyan.FFmpeg`.
- **macOS** (Apple Silicon and Intel): unzip and open **Chameleon IP**. It is
  not notarised: try once, then **System Settings → Privacy & Security → Open
  Anyway**. Needs ffmpeg: `brew install ffmpeg`.
- **Linux:** `tar xzf ChameleonIP-linux-x64.tar.gz`, then run
  `ChameleonIP/chameleon_ip`. Needs ffmpeg: `sudo apt install ffmpeg`.
- **Android 7 or later:** open the APK and allow your browser or files app to
  install it.

Every file has a `.sha256` beside it to check the download against.
