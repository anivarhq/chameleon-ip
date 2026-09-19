#!/bin/sh
# Builds the shared core into the Android app's libs/, for every ABI a phone
# might have. Needs Go and the Android NDK (ANDROID_NDK_HOME).
#
# The link flag is not optional: Go does not align to 16 KB pages by default,
# and Google Play has required that since November 2025.
set -eu

cd "$(dirname "$0")/../core"
mkdir -p ../android/app/libs

go tool gomobile init
go tool gomobile bind -v \
  -target=android/arm64,android/arm,android/amd64 \
  -androidapi 24 \
  -ldflags="-s -w -extldflags=-Wl,-z,max-page-size=16384" \
  -o ../android/app/libs/chameleon.aar \
  ./mobile

echo "built android/app/libs/chameleon.aar"
