package core

import (
	"strings"
	"testing"
	"time"
)

// Anyone on the network can post to the ONVIF endpoint, and some operations
// are answered before any password is checked. So a request has to stay cheap
// to answer, or one machine on the Wi-Fi can stop the camera serving video
// just by keeping it busy.
//
// The bound is deliberately loose and absolute rather than a ratio against a
// smaller request: measured, the cost is linear in the size of the body
// (four times the input, 3.7 times the work), and the earlier ratio version
// of this test mostly measured its own timer. What matters is that the
// biggest body the server will read cannot cost meaningful time.
func TestTheLargestRequestStaysCheap(t *testing.T) {
	cam := New(Config{User: "admin", Pass: "test1234"})
	server := &onvifServer{cam: cam, nonces: map[string]time.Time{}}

	// maxBodyBytes is what handle() reads before giving up, so nothing larger
	// ever reaches this code.
	const maxBodyBytes = 1 << 20

	shapes := map[string]string{
		"deep nesting":       strings.Repeat("<a>", maxBodyBytes/3),
		"many attributes":    `<s:Body><x ` + strings.Repeat(`a="1" `, maxBodyBytes/6) + `/></s:Body>`,
		"huge tag name":      `<s:Body><` + strings.Repeat("x", maxBodyBytes) + `/></s:Body>`,
		"unclosed elements":  strings.Repeat(`<s:Body>`, maxBodyBytes/8),
		"not xml at all":     strings.Repeat("x", maxBodyBytes),
		"credential-looking": `<wsse:Nonce>` + strings.Repeat("A", maxBodyBytes) + `</wsse:Nonce>`,
		// A real operation with a megabyte of junk trailing it.
		"buried operation": `<s:Body><trt:GetProfiles/></s:Body>` + strings.Repeat("<!-- x -->", maxBodyBytes/10),
	}

	for name, body := range shapes {
		start := time.Now()

		op := ""
		if m := bodyFirstChild.FindStringSubmatch(body); m != nil {
			op = m[1]
		}
		if server.authenticated(body) {
			t.Errorf("%s: junk was accepted as credentials", name)
		}
		if preAuth[op] {
			server.respond(op, body, "192.168.1.5")
		}

		// Generous on purpose: this runs under the race detector in CI, which
		// is five to twenty times slower, and it still only fires on something
		// pathological.
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("%s: a %d byte request took %v", name, len(body), took)
		}
	}
}
