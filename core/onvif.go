package core

import (
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DeviceServicePath is where NVRs expect the ONVIF endpoint to live.
const DeviceServicePath = "/onvif/device_service"

// Operations every ONVIF device must answer without credentials. A client
// reads the clock first so it can build a password digest that isn't rejected
// as stale, and reads capabilities to find out what else to ask for.
var preAuth = map[string]bool{
	"GetSystemDateAndTime":  true,
	"GetCapabilities":       true,
	"GetServices":           true,
	"GetServiceCapabilities": true,
	"GetWsdlUrl":            true,
	"GetEndpointReference":  true,
	// GetUsers is deliberately NOT here: leaving it open leaks the account
	// list, as onvif_simple_server does.
}

var bodyFirstChild = regexp.MustCompile(`(?s)<(?:\w+:)?Body[^>]*>\s*<(?:\w+:)?([A-Za-z]\w*)`)

// onvifServer answers the SOAP calls NVRs actually make. It is not a bid for
// Profile S conformance: that needs ONVIF membership and mandates MJPEG, and
// Profile S is retired in March 2027. This covers what go2rtc's server covers,
// which is tested against Home Assistant, ONVIF Device Manager and Onvier,
// plus the authentication go2rtc leaves out.
type onvifServer struct {
	cam  *Camera
	http *http.Server

	mu     sync.Mutex
	nonces map[string]time.Time // replay cache, pruned by age
}

func (o *onvifServer) start(address string) error {
	o.nonces = map[string]time.Time{}
	mux := http.NewServeMux()
	mux.HandleFunc(DeviceServicePath, o.handle)
	o.http = &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	go func() { _ = o.http.Serve(ln) }()
	return nil
}

func (o *onvifServer) stop() {
	if o.http != nil {
		_ = o.http.Close()
	}
}

func (o *onvifServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	body := string(raw)

	op := ""
	if m := bodyFirstChild.FindStringSubmatch(body); m != nil {
		op = m[1]
	}

	if !preAuth[op] && !o.authenticated(body) {
		// 400 with a fault, which is what ONVIF devices send for a failed
		// UsernameToken; clients read the fault, not the status code.
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, fault("ter:NotAuthorized", "The action requires authorization"))
		return
	}

	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}

	resp, ok := o.respond(op, body, host)
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
	}
	_, _ = io.WriteString(w, resp)
}

func (o *onvifServer) respond(op, body, host string) (string, bool) {
	c := o.cam.cfg
	device := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, portOf(c.ONVIFAddress)), DeviceServicePath)

	switch op {
	case "GetSystemDateAndTime":
		n := time.Now().UTC()
		return envelope(fmt.Sprintf(`<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime>`+
			`<tt:DateTimeType>NTP</tt:DateTimeType><tt:DaylightSavings>false</tt:DaylightSavings>`+
			`<tt:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>`+
			`<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tt:UTCDateTime>`+
			`</tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>`,
			n.Hour(), n.Minute(), n.Second(), n.Year(), int(n.Month()), n.Day())), true

	case "GetCapabilities":
		return envelope(fmt.Sprintf(`<tds:GetCapabilitiesResponse><tds:Capabilities>`+
			`<tt:Device><tt:XAddr>%[1]s</tt:XAddr><tt:System><tt:DiscoveryResolve>true</tt:DiscoveryResolve>`+
			`<tt:DiscoveryBye>true</tt:DiscoveryBye></tt:System></tt:Device>`+
			`<tt:Media><tt:XAddr>%[1]s</tt:XAddr><tt:StreamingCapabilities>`+
			`<tt:RTPMulticast>false</tt:RTPMulticast><tt:RTP_TCP>true</tt:RTP_TCP>`+
			`<tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media>`+
			`</tds:Capabilities></tds:GetCapabilitiesResponse>`, device)), true

	case "GetServices":
		return envelope(fmt.Sprintf(`<tds:GetServicesResponse>`+
			`<tds:Service><tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace>`+
			`<tds:XAddr>%[1]s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>5</tt:Minor></tds:Version></tds:Service>`+
			`<tds:Service><tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace>`+
			`<tds:XAddr>%[1]s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>5</tt:Minor></tds:Version></tds:Service>`+
			`</tds:GetServicesResponse>`, device)), true

	case "GetDeviceInformation":
		return envelope(fmt.Sprintf(`<tds:GetDeviceInformationResponse>`+
			`<tds:Manufacturer>Chameleon IP</tds:Manufacturer><tds:Model>%s</tds:Model>`+
			`<tds:FirmwareVersion>%s</tds:FirmwareVersion><tds:SerialNumber>%s</tds:SerialNumber>`+
			`<tds:HardwareId>%s</tds:HardwareId></tds:GetDeviceInformationResponse>`,
			esc(c.Model), esc(Version), esc(c.Serial), esc(c.Serial))), true

	case "GetScopes":
		return envelope(fmt.Sprintf(`<tds:GetScopesResponse>%s</tds:GetScopesResponse>`,
			scopeXML(c, "tds:Scopes"))), true

	case "GetNetworkInterfaces":
		// UniFi Protect and Home Assistant identify a camera by its MAC, so
		// this has to be the real one, and the same one every time.
		mac, ip := primaryInterface()
		return envelope(fmt.Sprintf(`<tds:GetNetworkInterfacesResponse>`+
			`<tds:NetworkInterfaces token="eth0"><tt:Enabled>true</tt:Enabled>`+
			`<tt:Info><tt:Name>eth0</tt:Name><tt:HwAddress>%s</tt:HwAddress><tt:MTU>1500</tt:MTU></tt:Info>`+
			`<tt:IPv4><tt:Enabled>true</tt:Enabled><tt:Config><tt:Manual><tt:Address>%s</tt:Address>`+
			`<tt:PrefixLength>24</tt:PrefixLength></tt:Manual><tt:DHCP>true</tt:DHCP></tt:Config></tt:IPv4>`+
			`</tds:NetworkInterfaces></tds:GetNetworkInterfacesResponse>`, mac, ip)), true

	case "GetVideoSources":
		return envelope(fmt.Sprintf(`<trt:GetVideoSourcesResponse>`+
			`<trt:VideoSources token="source"><tt:Framerate>%d</tt:Framerate>`+
			`<tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution>`+
			`</trt:VideoSources></trt:GetVideoSourcesResponse>`, c.FPS, c.Width, c.Height)), true

	case "GetProfiles":
		return envelope(fmt.Sprintf(`<trt:GetProfilesResponse>%s</trt:GetProfilesResponse>`,
			profileXML(c))), true

	case "GetProfile":
		return envelope(fmt.Sprintf(`<trt:GetProfileResponse>%s</trt:GetProfileResponse>`,
			strings.Replace(profileXML(c), "trt:Profiles", "trt:Profile", 2))), true

	case "GetStreamUri":
		// Any token other than the one real profile is a fault. Anivar probes
		// every token="…" it finds in GetProfiles, including the nested
		// configuration tokens, and relies on the fault to skip them —
		// answering them all would add phantom duplicate cameras.
		if tok := tagValue(body, "ProfileToken"); tok != "" && tok != profileToken {
			return fault("ter:InvalidArgVal/ter:NoProfile", "No such profile"), false
		}
		uri := fmt.Sprintf("rtsp://%s/%s", net.JoinHostPort(host, portOf(c.Address)), c.Path)
		return envelope(fmt.Sprintf(`<trt:GetStreamUriResponse><trt:MediaUri>`+
			`<tt:Uri>%s</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect>`+
			`<tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT60S</tt:Timeout>`+
			`</trt:MediaUri></trt:GetStreamUriResponse>`, esc(uri))), true

	case "":
		return fault("ter:OperationProhibited", "Empty request"), false

	default:
		return fault("ter:ActionNotSupported", op+" is not supported"), false
	}
}

const profileToken = "main"

func profileXML(c Config) string {
	// RateControl, Resolution and the source configuration must all be filled
	// in with the truth: UniFi Protect rejects adoption ("Channel fps is not
	// found") when RateControl comes back empty.
	return fmt.Sprintf(`<trt:Profiles token="%[1]s" fixed="true"><tt:Name>%[1]s</tt:Name>`+
		`<tt:VideoSourceConfiguration token="source"><tt:Name>source</tt:Name><tt:UseCount>1</tt:UseCount>`+
		`<tt:SourceToken>source</tt:SourceToken>`+
		`<tt:Bounds x="0" y="0" width="%[2]d" height="%[3]d"></tt:Bounds></tt:VideoSourceConfiguration>`+
		`<tt:VideoEncoderConfiguration token="encoder"><tt:Name>encoder</tt:Name><tt:UseCount>1</tt:UseCount>`+
		`<tt:Encoding>H264</tt:Encoding>`+
		`<tt:Resolution><tt:Width>%[2]d</tt:Width><tt:Height>%[3]d</tt:Height></tt:Resolution>`+
		`<tt:Quality>4</tt:Quality>`+
		`<tt:RateControl><tt:FrameRateLimit>%[4]d</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval>`+
		`<tt:BitrateLimit>%[5]d</tt:BitrateLimit></tt:RateControl>`+
		`<tt:H264><tt:GovLength>%[6]d</tt:GovLength><tt:H264Profile>High</tt:H264Profile></tt:H264>`+
		`<tt:SessionTimeout>PT60S</tt:SessionTimeout></tt:VideoEncoderConfiguration></trt:Profiles>`,
		profileToken, c.Width, c.Height, c.FPS, c.Bitrate/1000, c.FPS*2)
}

func scopeXML(c Config, tag string) string {
	scopes := []string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/name/" + urlScope(c.Name),
		"onvif://www.onvif.org/hardware/" + urlScope(c.Model),
	}
	var b strings.Builder
	for _, s := range scopes {
		fmt.Fprintf(&b, `<%[1]s><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>%[2]s</tt:ScopeItem></%[1]s>`, tag, esc(s))
	}
	return b.String()
}

func envelope(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:tds="http://www.onvif.org/ver10/device/wsdl"` +
		` xmlns:trt="http://www.onvif.org/ver10/media/wsdl"` +
		` xmlns:tt="http://www.onvif.org/ver10/schema">` +
		`<s:Body>` + body + `</s:Body></s:Envelope>`
}

func fault(code, reason string) string {
	return envelope(fmt.Sprintf(`<s:Fault><s:Code><s:Value>s:Sender</s:Value>`+
		`<s:Subcode><s:Value xmlns:ter="http://www.onvif.org/ver10/error">%s</s:Value></s:Subcode></s:Code>`+
		`<s:Reason><s:Text xml:lang="en">%s</s:Text></s:Reason></s:Fault>`, esc(code), esc(reason)))
}

// authenticated checks a WS-UsernameToken. The digest is SHA-1 by the spec —
// not SHA-256, which is the bug this project found in Anivar's client.
func (o *onvifServer) authenticated(body string) bool {
	user := tagValue(body, "Username")
	digest := tagValue(body, "Password")
	nonce := tagValue(body, "Nonce")
	created := tagValue(body, "Created")
	if user == "" || digest == "" || nonce == "" || created == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(user), []byte(o.cam.cfg.User)) != 1 {
		return false
	}

	// A Created far from our clock is a replayed or bogus token. ±300 s is
	// what gSOAP-based devices allow.
	when, err := time.Parse(time.RFC3339, created)
	if err != nil || absDuration(time.Since(when)) > 5*time.Minute {
		return false
	}
	if o.seenNonce(nonce, when) {
		return false
	}

	raw, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return false
	}
	sum := sha1.Sum(append(append(raw, []byte(created)...), []byte(o.cam.cfg.Pass)...))
	want := base64.StdEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(digest)) == 1
}

// seenNonce records a nonce and reports whether it had been used before.
func (o *onvifServer) seenNonce(nonce string, when time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for n, t := range o.nonces {
		if time.Since(t) > 10*time.Minute {
			delete(o.nonces, n)
		}
	}
	if _, used := o.nonces[nonce]; used {
		return true
	}
	o.nonces[nonce] = when
	return false
}

// tagValue returns the text of the first element with this local name,
// whatever namespace prefix it carries.
func tagValue(doc, local string) string {
	dec := xml.NewDecoder(strings.NewReader(doc))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == local {
			var text string
			if err := dec.DecodeElement(&text, &start); err != nil {
				return ""
			}
			return strings.TrimSpace(text)
		}
	}
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// urlScope keeps a scope item to the characters ONVIF allows in one.
func urlScope(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "camera"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

func portOf(address string) string {
	if _, port, err := net.SplitHostPort(address); err == nil && port != "" {
		return port
	}
	return strings.TrimPrefix(address, ":")
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// primaryInterface reports the MAC and IPv4 of the interface facing the LAN.
func primaryInterface() (mac, ip string) {
	mac, ip = "00:00:00:00:00:00", "0.0.0.0"
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
				continue
			}
			if iface.HardwareAddr != nil {
				mac = iface.HardwareAddr.String()
			}
			return mac, n.IP.String()
		}
	}
	return
}
