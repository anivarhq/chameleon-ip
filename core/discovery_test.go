package core

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// TestAnivarFindsUs replays Anivar's WS-Discovery probe (hw_onvif.rs) and
// checks the reply carries what its parser reads back out.
func TestAnivarFindsUs(t *testing.T) {
	cam, _ := startTestCamera(t)

	// The camera's own responder holds the real port; this one is private to
	// the test, so it never competes with the system's discovery service.
	d := &discovery{cam: cam, port: 0}
	ln, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	probeID := "uuid:9f7c4e2a-0000-4000-8000-abcdefabcdef"
	probe := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <e:Header><w:MessageID>%s</w:MessageID><w:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To><w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>
  <e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body>
</e:Envelope>`, probeID)

	reply := d.probeMatch(tagValue(probe, "MessageID"), net.IPv4(127, 0, 0, 1))

	// Anivar reads XAddrs, whatever prefix it carries, and dials it.
	xaddr := tagValue(reply, "XAddrs")
	if !strings.HasSuffix(xaddr, DeviceServicePath) {
		t.Errorf("XAddrs = %q, want an ONVIF device service URL", xaddr)
	}
	if !strings.Contains(reply, "NetworkVideoTransmitter") {
		t.Error("reply does not announce itself as a video transmitter")
	}
	if got := tagValue(reply, "RelatesTo"); got != probeID {
		t.Errorf("RelatesTo = %q, want the probe's MessageID %q", got, probeID)
	}
	if got := tagValue(reply, "Address"); got != cam.cfg.UUID {
		t.Errorf("endpoint = %q, want the stored UUID %q", got, cam.cfg.UUID)
	}
	// Home Assistant reads the name and hardware out of the scopes.
	if scopes := tagValue(reply, "Scopes"); !strings.Contains(scopes, "name/Back_door") ||
		!strings.Contains(scopes, "Profile/Streaming") {
		t.Errorf("scopes = %q", scopes)
	}
}

// TestProbeIsAnsweredOverTheWire runs the real responder on a private port.
func TestProbeIsAnsweredOverTheWire(t *testing.T) {
	cam, _ := startTestCamera(t)

	port := freeUDPPort(t)
	d := &discovery{cam: cam, port: port, host: "127.0.0.1"}
	if err := d.start(); err != nil {
		t.Skipf("cannot listen for discovery here: %v", err)
	}
	defer d.close()

	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	probe := `<?xml version="1.0"?><e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl"><e:Header><w:MessageID>uuid:probe-1</w:MessageID></e:Header><e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`
	if _, err := client.Write([]byte(probe)); err != nil {
		t.Fatal(err)
	}

	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 65536)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("no ProbeMatch came back: %v", err)
	}
	if reply := string(buf[:n]); !strings.Contains(reply, "ProbeMatches") ||
		!strings.Contains(reply, DeviceServicePath) {
		t.Errorf("unexpected reply: %s", reply)
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}
