package core

import (
	"strings"
	"testing"
	"time"
)

// The ONVIF endpoint takes XML from anything that can reach the port, and it
// answers some of it before any password is checked. It must never panic on
// input, whatever shape it arrives in.
func FuzzONVIFRequest(f *testing.F) {
	f.Add(`<s:Envelope><s:Body><trt:GetProfiles/></s:Body></s:Envelope>`)
	f.Add(`<s:Envelope><s:Body><tds:GetSystemDateAndTime/></s:Body></s:Envelope>`)
	f.Add(`<s:Envelope><s:Header><wsse:Security><wsse:UsernameToken>` +
		`<wsse:Username>admin</wsse:Username><wsse:Password>x</wsse:Password>` +
		`<wsse:Nonce>!!!not base64!!!</wsse:Nonce><wsu:Created>nonsense</wsu:Created>` +
		`</wsse:UsernameToken></wsse:Security></s:Header><s:Body><trt:GetStreamUri/></s:Body></s:Envelope>`)
	f.Add(`<s:Body><`)
	f.Add("")
	f.Add(strings.Repeat("<a>", 5000))

	cam := New(Config{User: "admin", Pass: "test1234", Address: "127.0.0.1:0"})
	server := &onvifServer{cam: cam, nonces: map[string]time.Time{}}

	f.Fuzz(func(t *testing.T, body string) {
		// Neither the parser nor the authentication may panic, and an
		// unauthenticated caller must never reach a protected answer.
		op := ""
		if m := bodyFirstChild.FindStringSubmatch(body); m != nil {
			op = m[1]
		}
		authed := server.authenticated(body)
		if authed {
			t.Fatalf("empty or malformed credentials were accepted: %q", body)
		}
		if !preAuth[op] {
			return
		}
		resp, _ := server.respond(op, body, "192.168.1.5")
		if !strings.Contains(resp, "Envelope") {
			t.Fatalf("op %q produced something that is not a SOAP envelope", op)
		}
	})
}

// The same for the discovery responder, which answers UDP from anyone.
func FuzzProbe(f *testing.F) {
	f.Add(`<e:Envelope><e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`)
	f.Add(`<Probe>`)
	f.Add("\x00\x01\x02")

	cam := New(Config{User: "admin", Pass: "test1234"})
	d := &discovery{cam: cam}

	f.Fuzz(func(t *testing.T, probe string) {
		reply := d.probeMatch(tagValue(probe, "MessageID"), nil)
		if !strings.Contains(reply, "ProbeMatches") {
			t.Fatal("the responder stopped producing a ProbeMatch")
		}
	})
}
