# Contributing

Thanks for helping. Bug reports, fixes and testing on real devices are all
welcome, and testing matters most right now: the Android app has only run on an
emulator, and the macOS and Linux desktop apps only in CI. If you try one, an
issue saying what worked and what didn't is a real contribution.

Security problems go through the private form in [SECURITY.md](SECURITY.md).

## How the code is laid out

Everything after the encoder (RTSP, ONVIF, discovery, passwords, the network
guard) is one Go package, `core/`, that every platform links. Capture and
encoding stay native on each platform.

| Part | Needs | Build |
|---|---|---|
| `core/` and the headless engine | Go (see `core/go.mod`) | `cd core && go test ./... && go build ./cmd/chameleon` |
| Desktop window (`flutter/`) | Flutter stable, plus ffmpeg to run | `cd flutter && flutter create --platforms=windows . && flutter run` (the platform folders are generated, not committed; CI shows the exact steps) |
| Android (`android/`) | Go, JDK 17, Android NDK r28, Gradle | `ANDROID_NDK_HOME=… scripts/build-core.sh`, then `cd android && gradle assembleDebug` |
| iPhone, iPad, Mac (`apple/`) | macOS, Xcode, xcodegen | `scripts/build-core-apple.sh`, then `cd apple && xcodegen generate` and open the project |

The workflows in `.github/workflows/` are the reference: each platform's CI
builds from a clean machine with exactly these steps.

## Pull requests

- Keep a change to one thing, and say in the description what you tested it
  on (emulator, phone model, OS version).
- `core/` changes need `go test ./...` and `go vet ./...` passing; the Flutter
  window has widget tests in `flutter/test/`.
- A release build is signed only in CI, with a key held in the repository's
  secrets. Your builds are debug or unsigned, which is expected.

By contributing you agree your work is licensed under [Apache-2.0](LICENSE).
