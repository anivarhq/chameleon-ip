package core

import (
	"encoding/json"
	"testing"
	"time"
)

// A camera runs for weeks. RTP timestamps are 32 bits at 90 kHz, so they wrap
// about every 13 hours 15 minutes, and wrapping is normal — the receiver
// expects it. What must not happen is the conversion going undefined at the
// boundary and the stream jumping somewhere random.
func TestRTPTimestampWrapsCleanly(t *testing.T) {
	const hz = 90000
	cases := []struct {
		name string
		pts  time.Duration
		want uint32
	}{
		{"start", 0, 0},
		{"one second", time.Second, hz},
		{"an hour", time.Hour, uint32(3600 * hz)},
		// Just before and just after the 32-bit wrap.
		{"before the wrap", 47721 * time.Second, uint32(47721 * hz)},
		{"after the wrap", 47722 * time.Second, uint32((47722 * hz) % (1 << 32))},
		{"a week", 7 * 24 * time.Hour, uint32((7 * 24 * 3600 * hz) % (1 << 32))},
	}
	for _, c := range cases {
		if got := rtpTimestamp(c.pts); got != c.want {
			t.Errorf("%s: rtpTimestamp(%v) = %d, want %d", c.name, c.pts, got, c.want)
		}
	}

	// And it keeps advancing by the right amount across the wrap, which is
	// what a receiver actually reconstructs from.
	before := rtpTimestamp(47721 * time.Second)
	after := rtpTimestamp(47721*time.Second + time.Second)
	if after-before != hz {
		t.Errorf("across the wrap the step was %d, want %d", after-before, hz)
	}
}

// The platform apps configure the core with a JSON string. Go matches field
// names case-insensitively, so "uuid" finds UUID by luck, but a two-word key
// like onvif_address silently does nothing without a tag — and the camera
// then listens on a port nobody was told about.
func TestConfigReadsEveryKeyTheAppsSend(t *testing.T) {
	const sent = `{
		"address": ":9554",
		"onvif_address": ":9000",
		"path": "sub",
		"user": "admin",
		"pass": "secret",
		"uuid": "urn:uuid:abc",
		"name": "Back door",
		"model": "Pixel 4a",
		"serial": "SER-1",
		"width": 1920, "height": 1080, "fps": 20, "bitrate": 4000000
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(sent), &cfg); err != nil {
		t.Fatal(err)
	}
	want := Config{
		Address: ":9554", ONVIFAddress: ":9000", Path: "sub",
		User: "admin", Pass: "secret",
		UUID: "urn:uuid:abc", Name: "Back door", Model: "Pixel 4a", Serial: "SER-1",
		Width: 1920, Height: 1080, FPS: 20, Bitrate: 4_000_000,
	}
	if cfg != want {
		t.Errorf("config = %+v\nwant     %+v", cfg, want)
	}
}
