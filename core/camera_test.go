package core

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
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

// Apple's VideoToolbox calls its output handler on a thread of its choosing,
// and Android's MediaCodec callback runs on its own handler thread. If two
// frames ever arrive at once, the RTP encoder must not be shared unguarded —
// this is the test that says so, and it is meant to be run with -race.
func TestPushIsSafeFromSeveralThreads(t *testing.T) {
	cam := New(Config{
		Address: "127.0.0.1:0", ONVIFAddress: "127.0.0.1:0",
		User: "admin", Pass: "test1234",
	})
	cam.cfg.Address = "127.0.0.1:" + freePort(t)
	cam.cfg.ONVIFAddress = "127.0.0.1:" + freePort(t)
	if err := cam.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cam.Stop()

	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	frame := [][]byte{sps, pps, {0x65, 0x01, 0x02, 0x03}}

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = cam.PushAU(frame, time.Duration(i)*time.Millisecond)
			}
		}(worker)
	}
	// And viewers coming and going at the same time, which is the other half
	// of the shared state.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			cam.OnPlay(&gortsplib.ServerHandlerOnPlayCtx{})
			cam.OnSessionClose(&gortsplib.ServerHandlerOnSessionCloseCtx{})
			_ = cam.Viewers()
			_ = cam.Notes()
		}
	}()
	wg.Wait()
}

// Android's software encoder sends SPS and PPS in separate buffers. Both must
// be kept, published in the stream description, and put before each keyframe,
// or nothing can decode the stream.
func TestParameterSetsArrivingSeparately(t *testing.T) {
	cam := New(Config{User: "admin", Pass: "test1234"})
	cam.cfg.Address = "127.0.0.1:" + freePort(t)
	cam.cfg.ONVIFAddress = "127.0.0.1:" + freePort(t)
	if err := cam.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cam.Stop()

	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	idr := []byte{0x65, 0x01, 0x02, 0x03}
	for i, au := range [][][]byte{{sps}, {pps}, {idr}} {
		if err := cam.PushAU(au, time.Duration(i)*time.Millisecond); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}

	cam.mu.Lock()
	gotSPS, gotPPS := cam.sps, cam.pps
	cam.mu.Unlock()
	if !bytes.Equal(gotSPS, sps) || !bytes.Equal(gotPPS, pps) {
		t.Fatalf("kept sps=% x pps=% x; want both", gotSPS, gotPPS)
	}
	if !bytes.Equal(cam.forma.SPS, sps) || !bytes.Equal(cam.forma.PPS, pps) {
		t.Fatal("the stream description does not carry the parameter sets")
	}
	if got := prepareAU([][]byte{idr}, gotSPS, gotPPS); len(got) != 3 {
		t.Fatalf("a keyframe goes out as %d NAL units; want SPS, PPS, IDR", len(got))
	}
}

// The phone apps' buffers are theirs: gomobile passes a byte slice by
// reference, valid only during the call. What the camera keeps must survive
// the caller reusing that memory the moment PushAnnexB returns.
func TestPushAnnexBOwnsWhatItKeeps(t *testing.T) {
	cam := New(Config{User: "admin", Pass: "test1234"})
	cam.cfg.Address = "127.0.0.1:" + freePort(t)
	cam.cfg.ONVIFAddress = "127.0.0.1:" + freePort(t)
	if err := cam.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cam.Stop()

	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	config := append(append(append([]byte{0, 0, 0, 1}, sps...), 0, 0, 0, 1), pps...)
	if err := cam.PushAnnexB(config, 0); err != nil {
		t.Fatal(err)
	}
	clear(config) // what the app does with its buffer next

	cam.mu.Lock()
	gotSPS, gotPPS := cam.sps, cam.pps
	cam.mu.Unlock()
	if !bytes.Equal(gotSPS, sps) || !bytes.Equal(gotPPS, pps) {
		t.Fatalf("kept sps=% x pps=% x after the caller reused its buffer", gotSPS, gotPPS)
	}
}
