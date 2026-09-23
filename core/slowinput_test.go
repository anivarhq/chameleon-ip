package core

import (
	"strings"
	"testing"
	"time"
)

// Anyone on the network can post to the ONVIF endpoint, and some operations
// are answered before any password is checked. So a request that is cheap to
// send must stay cheap to answer: otherwise one machine on the Wi-Fi can stop
// the camera serving video simply by keeping it busy.
func TestAwkwardRequestsStayCheap(t *testing.T) {
	cam := New(Config{User: "admin", Pass: "test1234"})
	server := &onvifServer{cam: cam, nonces: map[string]time.Time{}}

	cases := map[string]string{
		"deep nesting":       strings.Repeat("<a>", 50_000),
		"many attributes":    `<s:Body><x ` + strings.Repeat(`a="1" `, 50_000) + `/></s:Body>`,
		"huge tag name":      `<s:Body><` + strings.Repeat("x", 200_000) + `/></s:Body>`,
		"unclosed elements":  strings.Repeat(`<s:Body>`, 50_000),
		"no body at all":     strings.Repeat("x", 500_000),
		"credential-looking": `<wsse:Nonce>` + strings.Repeat("A", 200_000) + `</wsse:Nonce>`,
	}

	for name, body := range cases {
		start := time.Now()

		op := ""
		if m := bodyFirstChild.FindStringSubmatch(body); m != nil {
			op = m[1]
		}
		server.authenticated(body)
		if preAuth[op] {
			server.respond(op, body, "192.168.1.5")
		}

		// A request of this size should be microseconds of work. The limit is
		// generous on purpose: it only fires on something pathological.
		if took := time.Since(start); took > 250*time.Millisecond {
			t.Errorf("%s took %v", name, took)
		}
	}
}
