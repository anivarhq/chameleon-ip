#!/usr/bin/env bash
# The shipping APK on an emulator: install, launch, press "Start camera", and
# check that the RTSP port opens and nothing crashes. Not a phone, but the same
# file a phone would get, through the same camera and encoder APIs.
#
#   android-smoke.sh <apk>
set -euo pipefail
apk="$1"
pkg=com.anivarhq.chameleon

fail() {
  echo "::error::$1"
  adb logcat -d | grep -E "$pkg|AndroidRuntime|FATAL|chameleon" | tail -80 || true
  exit 1
}

adb install -r "$apk"
adb shell pm grant "$pkg" android.permission.CAMERA
adb shell pm grant "$pkg" android.permission.POST_NOTIFICATIONS
adb logcat -c
adb shell monkey -p "$pkg" -c android.intent.category.LAUNCHER 1 > /dev/null
sleep 10
adb shell pidof "$pkg" > /dev/null || fail "the app is not running 10 s after launch"

# Press "Turn on camera" wherever the layout put it.
adb shell uiautomator dump /sdcard/ui.xml > /dev/null
bounds=$(adb shell cat /sdcard/ui.xml \
  | grep -io 'text="Turn on camera"[^>]*bounds="\[[0-9]*,[0-9]*\]\[[0-9]*,[0-9]*\]"' \
  | grep -o '\[[0-9]*,[0-9]*\]\[[0-9]*,[0-9]*\]' | head -1 || true)
[ -n "$bounds" ] || fail "no Turn on camera button on screen"
read -r x1 y1 x2 y2 <<< "$(echo "$bounds" | tr -c '0-9' ' ')"
adb shell input tap $(( (x1 + x2) / 2 )) $(( (y1 + y2) / 2 ))

# RTSP listens on 8554 (0x216A in /proc/net) once the camera is running.
for i in $(seq 1 30); do
  if { adb shell netstat -tln 2>/dev/null; adb shell cat /proc/net/tcp /proc/net/tcp6 2>/dev/null; } \
       | grep -qiE ':8554[[:space:]]|:216A[[:space:]]'; then
    echo "RTSP port 8554 is listening"
    break
  fi
  [ "$i" -eq 30 ] && fail "RTSP port 8554 never opened after Turn on camera"
  sleep 1
done

# Let the camera and encoder run a while before judging, then keep a picture
# of the screen: the preview's shape and orientation are for a person to see.
sleep 15
adb exec-out screencap -p > emulator-screen.png || true
adb shell pidof "$pkg" > /dev/null || fail "the app died after Turn on camera"

# Then the stream itself, as a recorder gets it: open "Use with a recorder",
# read the address off the screen, and play it through a forwarded port
# (loopback is an allowed source), Digest auth and all.
tap_text() {
  adb shell uiautomator dump /sdcard/ui.xml > /dev/null
  local b
  b=$(adb shell cat /sdcard/ui.xml     | grep -io "text=\"$1\"[^>]*bounds=\"\[[0-9]*,[0-9]*\]\[[0-9]*,[0-9]*\]\""     | grep -o '\[[0-9]*,[0-9]*\]\[[0-9]*,[0-9]*\]' | head -1 || true)
  [ -n "$b" ] || fail "no \"$1\" on screen"
  read -r x1 y1 x2 y2 <<< "$(echo "$b" | tr -c '0-9' ' ')"
  adb shell input tap $(( (x1 + x2) / 2 )) $(( (y1 + y2) / 2 ))
}
tap_text "Use with a recorder"
sleep 2
adb shell uiautomator dump /sdcard/ui.xml > /dev/null
url=$(adb shell cat /sdcard/ui.xml | grep -o 'rtsp://[^@"]*@[^"/]*:8554/main' | head -1 || true)
[ -n "$url" ] || fail "no address shown after Use with a recorder"
local_url=$(echo "$url" | sed -E 's#@[^/]*:8554/#@127.0.0.1:8554/#')
adb forward tcp:8554 tcp:8554
probe=$(ffprobe -v error -rtsp_transport tcp -select_streams v:0   -show_entries stream=codec_name,width,height -of csv=p=0 "$local_url" || true)
echo "stream: $probe"
[ "$probe" = "h264,1280,720" ] || fail "the stream is not H.264 1280x720 (got: $probe)"
ffmpeg -v error -rtsp_transport tcp -i "$local_url" -frames:v 1 -y stream-frame.png || true
if adb logcat -d | grep -q "FATAL EXCEPTION"; then
  fail "a crash was logged"
fi
echo "smoke test passed: installed, launched, camera on, stream plays as H.264 1280x720, no crash"
