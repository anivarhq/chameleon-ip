package core

import (
	"bytes"
	"testing"
)

func TestSplitAnnexB(t *testing.T) {
	sps, pps, idr := []byte{0x67, 1, 2}, []byte{0x68, 3}, []byte{0x65, 4, 5, 6}
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write(sps)
	buf.Write([]byte{0, 0, 1}) // three-byte start codes are legal too
	buf.Write(pps)
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write(idr)

	got := splitAnnexB(buf.Bytes())
	want := [][]byte{sps, pps, idr}
	if len(got) != len(want) {
		t.Fatalf("got %d NAL units, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("unit %d = % x, want % x", i, got[i], want[i])
		}
	}
}

func TestPrepareAU(t *testing.T) {
	sps, pps := []byte{0x67, 1}, []byte{0x68, 2}
	idr := []byte{0x65, 9}
	nonIDR := []byte{0x41, 9}

	// A keyframe without parameter sets gets them, or players joining
	// between keyframes never decode anything.
	got := prepareAU([][]byte{idr}, sps, pps)
	if len(got) != 3 || !bytes.Equal(got[0], sps) || !bytes.Equal(got[1], pps) {
		t.Errorf("keyframe did not get parameter sets: %v", got)
	}

	// Already carries them: left alone, no duplicates.
	full := [][]byte{sps, pps, idr}
	if got := prepareAU(full, sps, pps); len(got) != 3 {
		t.Errorf("parameter sets duplicated: %v", got)
	}

	// Ordinary frames are untouched.
	if got := prepareAU([][]byte{nonIDR}, sps, pps); len(got) != 1 {
		t.Errorf("non-keyframe was modified: %v", got)
	}

	// Nothing to prepend yet: must not send a nil NAL unit.
	if got := prepareAU([][]byte{idr}, nil, nil); len(got) != 1 {
		t.Errorf("prepended unknown parameter sets: %v", got)
	}
}

func TestStartRefusesWithoutCredentials(t *testing.T) {
	if err := New(Config{Address: "127.0.0.1:0"}).Start(); err == nil {
		t.Fatal("served with no password set")
	}
}
