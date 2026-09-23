package core

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The requests below are the ones Anivar's ONVIF client really sends
// (src-tauri/src/onvif.rs and hw_onvif.rs), byte for byte, so these tests
// fail if we drift away from the client that has to talk to us.

const (
	testUser = "admin"
	testPass = "test1234"
)

func startTestCamera(t *testing.T) (*Camera, string) {
	t.Helper()
	cam := New(Config{
		Address:      "127.0.0.1:0",
		ONVIFAddress: "127.0.0.1:0",
		User:         testUser,
		Pass:         testPass,
		UUID:         "urn:uuid:11111111-2222-3333-4444-555555555555",
		Name:         "Back door",
		Model:        "Pixel 4a",
		Serial:       "CHAM-0001",
		Width:        1280, Height: 720, FPS: 15, Bitrate: 2_000_000,
	})
	// Port 0 would make the advertised URLs meaningless, so bind real ports.
	cam.cfg.Address = "127.0.0.1:" + freePort(t)
	cam.cfg.ONVIFAddress = "127.0.0.1:" + freePort(t)
	if err := cam.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cam.Stop)
	return cam, "http://" + cam.cfg.ONVIFAddress + DeviceServicePath
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// anivarEnvelope reproduces Anivar's SOAP envelope, including its
// WS-UsernameToken with the spec's SHA-1 digest.
func anivarEnvelope(body, user, pass string, created time.Time, nonce []byte) string {
	security := ""
	if user != "" {
		stamp := created.UTC().Format("2006-01-02T15:04:05Z")
		sum := sha1.Sum(append(append(append([]byte{}, nonce...), []byte(stamp)...), []byte(pass)...))
		security = fmt.Sprintf(`<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken><wsse:Username>%s</wsse:Username><wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password><wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/08/security#Base64Binary">%s</wsse:Nonce><wsu:Created>%s</wsu:Created></wsse:UsernameToken></wsse:Security>`,
			user,
			base64.StdEncoding.EncodeToString(sum[:]),
			base64.StdEncoding.EncodeToString(nonce),
			stamp)
	}
	return `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header>` +
		security + `</s:Header><s:Body>` + body + `</s:Body></s:Envelope>`
}

func call(t *testing.T, url, body, user, pass string) string {
	t.Helper()
	// A fresh nonce per call, as a real client does: reusing one is exactly
	// what the replay cache rejects.
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return callWith(t, url, anivarEnvelope(body, user, pass, time.Now(), nonce))
}

func callWith(t *testing.T, url, envelope string) string {
	t.Helper()
	resp, err := http.Post(url, "application/soap+xml; charset=utf-8", strings.NewReader(envelope))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return string(out)
}

const (
	getProfiles          = `<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`
	getDeviceInformation = `<tds:GetDeviceInformation xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`
)

func getStreamURI(token string) string {
	return fmt.Sprintf(`<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:StreamSetup><tt:Stream xmlns:tt="http://www.onvif.org/ver10/schema">RTP-Unicast</tt:Stream><tt:Transport xmlns:tt="http://www.onvif.org/ver10/schema"><tt:Protocol>RTSP</tt:Protocol></tt:Transport></trt:StreamSetup><trt:ProfileToken>%s</trt:ProfileToken></trt:GetStreamUri>`, token)
}

func TestAnivarGetsAStreamURL(t *testing.T) {
	cam, url := startTestCamera(t)

	profiles := call(t, url, getProfiles, testUser, testPass)
	if !strings.Contains(profiles, `token="main"`) {
		t.Fatalf("no profile token in %s", profiles)
	}

	stream := call(t, url, getStreamURI("main"), testUser, testPass)
	uri := tagValue(stream, "Uri")
	want := "rtsp://" + cam.cfg.Address + "/main"
	if uri != want {
		t.Errorf("stream uri = %q, want %q", uri, want)
	}

	// Anivar probes every token="…" it finds, including the nested
	// configuration ones. Those must fault, or it lists phantom cameras.
	for _, token := range []string{"source", "encoder"} {
		resp := call(t, url, getStreamURI(token), testUser, testPass)
		if tagValue(resp, "Uri") != "" {
			t.Errorf("token %q returned a stream instead of a fault", token)
		}
	}

	info := call(t, url, getDeviceInformation, testUser, testPass)
	for tag, want := range map[string]string{
		"Manufacturer": "Chameleon IP",
		"Model":        "Pixel 4a",
		"SerialNumber": "CHAM-0001",
	} {
		if got := tagValue(info, tag); got != want {
			t.Errorf("%s = %q, want %q", tag, got, want)
		}
	}
}

func TestAuthentication(t *testing.T) {
	_, url := startTestCamera(t)

	// The clock is readable without credentials, so a client can build a
	// digest our replay window will accept.
	if tagValue(call(t, url, `<tds:GetSystemDateAndTime xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`, "", ""), "Year") == "" {
		t.Error("GetSystemDateAndTime should answer unauthenticated")
	}

	// Everything else must not.
	if strings.Contains(call(t, url, getProfiles, "", ""), `token="main"`) {
		t.Error("GetProfiles answered without credentials")
	}
	if strings.Contains(call(t, url, getProfiles, testUser, "wrong"), `token="main"`) {
		t.Error("GetProfiles answered a wrong password")
	}

	// SHA-256 is what Anivar used to send, and what the spec does not allow.
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	nonce := []byte("0123456789abcdef")
	wrongAlgo := fmt.Sprintf(`<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header><wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken><wsse:Username>%s</wsse:Username><wsse:Password>%s</wsse:Password><wsse:Nonce>%s</wsse:Nonce><wsu:Created>%s</wsu:Created></wsse:UsernameToken></wsse:Security></s:Header><s:Body>%s</s:Body></s:Envelope>`,
		testUser, "bm90LWEtc2hhMQ==", base64.StdEncoding.EncodeToString(nonce), stamp, getProfiles)
	if strings.Contains(callWith(t, url, wrongAlgo), `token="main"`) {
		t.Error("a bad digest was accepted")
	}

	// A token whose clock is far from ours is a replay.
	stale := anivarEnvelope(getProfiles, testUser, testPass, time.Now().Add(-30*time.Minute), nonce)
	if strings.Contains(callWith(t, url, stale), `token="main"`) {
		t.Error("a stale token was accepted")
	}

	// And the same nonce must not work twice.
	fresh := anivarEnvelope(getProfiles, testUser, testPass, time.Now(), []byte("replay-me-abcdef"))
	if !strings.Contains(callWith(t, url, fresh), `token="main"`) {
		t.Fatal("valid token rejected")
	}
	if strings.Contains(callWith(t, url, fresh), `token="main"`) {
		t.Error("a replayed nonce was accepted")
	}
}

func TestProfileTellsTheTruth(t *testing.T) {
	_, url := startTestCamera(t)
	profiles := call(t, url, getProfiles, testUser, testPass)

	// UniFi Protect refuses adoption when rate control is missing, and every
	// NVR reads the resolution from here.
	for _, want := range []string{
		"<tt:FrameRateLimit>15</tt:FrameRateLimit>",
		"<tt:BitrateLimit>2000</tt:BitrateLimit>",
		"<tt:Width>1280</tt:Width>",
		"<tt:Height>720</tt:Height>",
		"<tt:Encoding>H264</tt:Encoding>",
	} {
		if !strings.Contains(profiles, want) {
			t.Errorf("profile is missing %s:\n%s", want, profiles)
		}
	}
}
