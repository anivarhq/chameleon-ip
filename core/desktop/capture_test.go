package desktop

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// annexB builds a stream the way an encoder emits one: four-byte start codes
// for parameter sets and keyframes, three-byte ones between slices.
func annexB(units ...[]byte) []byte {
	var out bytes.Buffer
	for i, unit := range units {
		if i%2 == 0 {
			out.Write([]byte{0, 0, 0, 1})
		} else {
			out.Write([]byte{0, 0, 1})
		}
		out.Write(unit)
	}
	return out.Bytes()
}

func TestReadAccessUnitKeepsEveryNALU(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	idr := []byte{0x65, 0x11, 0x22, 0x33, 0x44}
	p1 := []byte{0x41, 0x9a, 0x01}
	p2 := []byte{0x41, 0x9a, 0x02}

	stream := annexB(sps, pps, idr, p1, p2)
	r := &nalScanner{r: bufio.NewReader(bytes.NewReader(stream))}

	// First frame: the parameter sets, then the keyframe.
	au, err := readAccessUnit(r)
	if err != nil {
		t.Fatalf("first access unit: %v", err)
	}
	if got, want := describe(au), "67,68,65"; got != want {
		t.Errorf("first access unit = %s, want %s", got, want)
	}

	// Then one frame per slice, in order, none skipped.
	for _, want := range []string{"41", "41"} {
		au, err = readAccessUnit(r)
		if err != nil {
			t.Fatalf("next access unit: %v", err)
		}
		if got := describe(au); got != want {
			t.Errorf("access unit = %s, want %s", got, want)
		}
	}

	if _, err := readAccessUnit(r); err == nil {
		t.Error("expected the stream to end")
	}
}

// TestNALUsSurviveIntact is the case that matters: the bytes that go in are
// the bytes that come out, with nothing dropped between start codes.
func TestNALUsSurviveIntact(t *testing.T) {
	units := [][]byte{
		{0x67, 0x42, 0x1f},
		{0x68, 0xce},
		{0x65, 0x01, 0x02, 0x03},
		{0x41, 0x7f},
		{0x41, 0xff, 0xfe},
	}
	r := &nalScanner{r: bufio.NewReader(bytes.NewReader(annexB(units...)))}

	var got [][]byte
	for {
		au, err := readAccessUnit(r)
		if err != nil {
			break
		}
		got = append(got, au...)
	}

	if len(got) != len(units) {
		t.Fatalf("got %d NAL units, want %d: %s", len(got), len(units), describe(got))
	}
	for i := range units {
		if !bytes.Equal(got[i], units[i]) {
			t.Errorf("unit %d = % x, want % x", i, got[i], units[i])
		}
	}
}

// A payload's trailing zeros cannot be told apart from the start code that
// follows: 41 00 | 00 00 01 and 41 | 00 00 00 01 are the same bytes. Both
// readings are legal H.264 and decoders ignore trailing zeros, so the scanner
// gives them to the start code. This test pins that down so the behaviour is
// a decision rather than a surprise.
func TestTrailingZerosGoToTheStartCode(t *testing.T) {
	stream := append([]byte{0, 0, 0, 1, 0x41, 0x9a, 0x00}, 0, 0, 1, 0x41, 0x9b)
	r := &nalScanner{r: bufio.NewReader(bytes.NewReader(stream))}

	first, err := r.next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, []byte{0x41, 0x9a}) {
		t.Errorf("first = % x, want 41 9a", first)
	}
	second, err := r.next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, []byte{0x41, 0x9b}) {
		t.Errorf("second = % x, want 41 9b", second)
	}
}

func describe(au [][]byte) string {
	var parts []string
	for _, nalu := range au {
		parts = append(parts, string([]byte{hexDigit(nalu[0] >> 4), hexDigit(nalu[0] & 0xf)}))
	}
	return strings.Join(parts, ",")
}

func hexDigit(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}
