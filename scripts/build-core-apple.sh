#!/bin/sh
# Builds the shared core as an xcframework for iOS, the simulator and macOS,
# into apple/Frameworks/. Needs Go and Xcode.
#
# The framework is named Mobile so the Swift side reads `import Mobile` and
# calls MobileStart, MobilePushFrame and friends, which is how gomobile names
# what it generates from the mobile package.
set -eu

cd "$(dirname "$0")/../core"
mkdir -p ../apple/Frameworks

go tool gomobile init
go tool gomobile bind -v \
  -target=ios,iossimulator,macos \
  -ldflags="-s -w" \
  -o ../apple/Frameworks/Mobile.xcframework \
  ./mobile

echo "built apple/Frameworks/Mobile.xcframework"
