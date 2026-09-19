package core

import "bytes"

// H.264 NAL unit types we care about.
const (
	naluIDR = 5
	naluSPS = 7
	naluPPS = 8
)

func naluType(nalu []byte) byte {
	if len(nalu) == 0 {
		return 0
	}
	return nalu[0] & 0x1F
}

// splitAnnexB splits a buffer of start-code-delimited NAL units.
// Encoders on Android and Apple both hand out this format.
func splitAnnexB(buf []byte) [][]byte {
	var out [][]byte
	for {
		i := bytes.Index(buf, []byte{0, 0, 1})
		if i < 0 {
			break
		}
		buf = buf[i+3:]
		next := bytes.Index(buf, []byte{0, 0, 1})
		end := len(buf)
		if next >= 0 {
			end = next
			// A four-byte start code leaves its extra zero on the tail.
			if end > 0 && buf[end-1] == 0 {
				end--
			}
		}
		if end > 0 {
			out = append(out, buf[:end])
		}
		if next < 0 {
			break
		}
		buf = buf[next:]
	}
	return out
}

// prepareAU returns the access unit to send.
//
// Parameter sets are repeated before every IDR: gortsplib's encoder does not
// add them, and a player that joins between them shows nothing. Missing
// parameter sets are what produce ffmpeg's "non-existing PPS" and a black
// picture.
func prepareAU(au [][]byte, sps, pps []byte) [][]byte {
	idr, hasSPS, hasPPS := false, false, false
	for _, nalu := range au {
		switch naluType(nalu) {
		case naluIDR:
			idr = true
		case naluSPS:
			hasSPS = true
		case naluPPS:
			hasPPS = true
		}
	}
	if !idr || (hasSPS && hasPPS) || sps == nil || pps == nil {
		return au
	}
	out := make([][]byte, 0, len(au)+2)
	if !hasSPS {
		out = append(out, sps)
	}
	if !hasPPS {
		out = append(out, pps)
	}
	return append(out, au...)
}

// SplitAccessUnits groups a whole Annex-B buffer into access units: one frame
// each, carrying any parameter sets that precede it.
//
// ponytail: the boundary rule is "anything following a coded slice starts the
// next frame", which holds for an encoder's own output (one slice per frame)
// but not for arbitrary streams, where first_mb_in_slice must be parsed. It
// feeds the test harness only; live frames arrive already framed.
func SplitAccessUnits(buf []byte) [][][]byte {
	var out [][][]byte
	var cur [][]byte
	sliceSeen := false
	for _, nalu := range splitAnnexB(buf) {
		if sliceSeen {
			out = append(out, cur)
			cur, sliceSeen = nil, false
		}
		cur = append(cur, nalu)
		if t := naluType(nalu); t >= 1 && t <= 5 {
			sliceSeen = true
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// parameterSets picks the SPS and PPS out of an access unit, if present.
func parameterSets(au [][]byte) (sps, pps []byte) {
	for _, nalu := range au {
		switch naluType(nalu) {
		case naluSPS:
			sps = nalu
		case naluPPS:
			pps = nalu
		}
	}
	return
}
