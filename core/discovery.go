package core

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"golang.org/x/net/ipv4"
)

// WS-Discovery, the way every NVR but Frigate finds a camera.
const (
	discoveryPort  = 3702
	discoveryGroup = "239.255.255.250"
)

type discovery struct {
	cam *Camera
	// port is 3702 in production; tests use an ephemeral one so they do not
	// fight the operating system's own discovery service for the real port.
	port int
	// host is the address to bind; empty means the LAN interface. Tests set
	// loopback.
	host string

	mu   sync.Mutex
	conn *net.UDPConn
	pc   *ipv4.PacketConn
	stop chan struct{}
}

// start joins the discovery group and answers probes. It reports an error
// rather than failing the camera: on Windows the system's own WS-Discovery
// service already holds this port, and a camera that can only be added by
// typing its address still works.
func (d *discovery) start() error {
	if d.port == 0 {
		d.port = discoveryPort
	}
	// Bind the LAN address rather than the wildcard where we can. Windows
	// runs its own WS-Discovery service on this port, and with the port
	// shared it is the more specific binding that receives the datagrams —
	// bound to 0.0.0.0 we get none of them.
	host := d.host
	if host == "" {
		if _, ip := primaryInterface(); ip != "0.0.0.0" {
			host = ip
		}
	}
	lc := net.ListenConfig{Control: reusePort}
	pconn, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("%s:%d", host, d.port))
	if err != nil && host != "" {
		// A phone that just changed networks may not have that address yet.
		pconn, err = lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf(":%d", d.port))
	}
	if err != nil {
		return err
	}
	conn, ok := pconn.(*net.UDPConn)
	if !ok {
		_ = pconn.Close()
		return fmt.Errorf("unexpected socket type %T", pconn)
	}

	pc := ipv4.NewPacketConn(conn)
	group := net.IPv4(239, 255, 255, 250)
	joined := 0
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		iface := ifaces[i]
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if err := pc.JoinGroup(&iface, &net.UDPAddr{IP: group}); err == nil {
			joined++
		}
	}
	if joined == 0 {
		// iOS refuses multicast without Apple's entitlement, and that is
		// exactly the case the unicast fallback exists for: an NVR probing
		// each address one by one still gets an answer. Keep the socket and
		// say what was lost.
		d.cam.note("discovery", fmt.Errorf(
			"no multicast on any interface; answering direct probes only"))
	}

	d.mu.Lock()
	d.conn, d.pc, d.stop = conn, pc, make(chan struct{})
	d.mu.Unlock()

	go d.serve(conn)
	d.announce("Hello")
	return nil
}

func (d *discovery) close() {
	d.mu.Lock()
	conn, stop := d.conn, d.stop
	d.conn, d.pc, d.stop = nil, nil, nil
	d.mu.Unlock()
	if conn == nil {
		return
	}
	// Tell NVRs the camera is going away, instead of leaving them to time out.
	d.announceOn(conn, "Bye")
	close(stop)
	_ = conn.Close()
}

func (d *discovery) serve(conn *net.UDPConn) {
	buf := make([]byte, 65536)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // closed
		}
		msg := string(buf[:n])
		if !strings.Contains(msg, "Probe") || strings.Contains(msg, "ProbeMatch") {
			continue
		}
		// Only answer probes for video transmitters, or for anything.
		if types := tagValue(msg, "Types"); types != "" &&
			!strings.Contains(types, "NetworkVideoTransmitter") && !strings.Contains(types, "Device") {
			continue
		}
		reply := d.probeMatch(tagValue(msg, "MessageID"), from.IP)
		// Unicast back to the asker: that is what the spec says, and it is
		// also the only path that works on an iPhone without Apple's
		// multicast entitlement.
		_, _ = conn.WriteToUDP([]byte(reply), from)
	}
}

func (d *discovery) announce(kind string) {
	d.mu.Lock()
	conn := d.conn
	d.mu.Unlock()
	if conn != nil {
		d.announceOn(conn, kind)
	}
}

func (d *discovery) announceOn(conn *net.UDPConn, kind string) {
	to := &net.UDPAddr{IP: net.ParseIP(discoveryGroup), Port: d.port}
	_, _ = conn.WriteToUDP([]byte(d.announcement(kind)), to)
}

func (d *discovery) announcement(kind string) string {
	c := d.cam.cfg
	body := fmt.Sprintf(`<d:%[1]s><a:EndpointReference><a:Address>%[2]s</a:Address></a:EndpointReference>`+
		`<d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types><d:Scopes>%[3]s</d:Scopes>`+
		`<d:XAddrs>%[4]s</d:XAddrs><d:MetadataVersion>1</d:MetadataVersion></d:%[1]s>`,
		kind, esc(c.UUID), esc(scopeList(c)), esc(d.xaddr(nil)))
	return discoveryEnvelope(
		"http://schemas.xmlsoap.org/ws/2005/04/discovery/"+kind,
		"urn:schemas-xmlsoap-org:ws:2005:04:discovery", "", body)
}

func (d *discovery) probeMatch(relatesTo string, asker net.IP) string {
	c := d.cam.cfg
	body := fmt.Sprintf(`<d:ProbeMatches><d:ProbeMatch>`+
		`<a:EndpointReference><a:Address>%s</a:Address></a:EndpointReference>`+
		`<d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types><d:Scopes>%s</d:Scopes>`+
		`<d:XAddrs>%s</d:XAddrs><d:MetadataVersion>1</d:MetadataVersion>`+
		`</d:ProbeMatch></d:ProbeMatches>`,
		esc(c.UUID), esc(scopeList(c)), esc(d.xaddr(asker)))
	return discoveryEnvelope(
		"http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches",
		"http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous", relatesTo, body)
}

// xaddr is the ONVIF address to hand out, on the interface that faces whoever
// asked; a camera with a VPN or virtual adapter has several.
func (d *discovery) xaddr(asker net.IP) string {
	ip := ""
	if asker != nil {
		ip = localAddressFacing(asker)
	}
	if ip == "" {
		_, ip = primaryInterface()
	}
	return fmt.Sprintf("http://%s%s",
		net.JoinHostPort(ip, portOf(d.cam.cfg.ONVIFAddress)), DeviceServicePath)
}

func scopeList(c Config) string {
	return strings.Join([]string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/name/" + urlScope(c.Name),
		"onvif://www.onvif.org/hardware/" + urlScope(c.Model),
	}, " ")
}

func discoveryEnvelope(action, to, relatesTo, body string) string {
	relates := ""
	if relatesTo != "" {
		relates = fmt.Sprintf(`<a:RelatesTo>%s</a:RelatesTo>`, esc(relatesTo))
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"` +
		` xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"` +
		` xmlns:dn="http://www.onvif.org/ver10/network/wsdl"` +
		` xmlns:tds="http://www.onvif.org/ver10/device/wsdl">` +
		`<s:Header><a:MessageID>urn:uuid:` + uuid.NewString() + `</a:MessageID>` +
		`<a:To s:mustUnderstand="1">` + to + `</a:To>` + relates +
		`<a:Action s:mustUnderstand="1">` + action + `</a:Action></s:Header>` +
		`<s:Body>` + body + `</s:Body></s:Envelope>`
}

// localAddressFacing reports which of our addresses routes to a given host.
func localAddressFacing(host net.IP) string {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: host, Port: discoveryPort})
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

// reusePort lets the socket share the port, which Windows needs because its
// own WS-Discovery service is already bound to it.
func reusePort(_, _ string, c syscall.RawConn) error {
	var opErr error
	err := c.Control(func(fd uintptr) {
		opErr = setReuseAddr(fd)
	})
	if err != nil {
		return err
	}
	return opErr
}
