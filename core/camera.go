// Package core serves one device's camera to NVRs.
//
// The platform app captures and hardware-encodes; it hands the encoded access
// units here, and this package does the network side: an RTSP server with
// Digest authentication, shared by Android, Apple and desktop.
package core

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"net"

	"github.com/google/uuid"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/auth"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/liberrors"
)

// Version is reported to NVRs as the firmware version.
const Version = "0.1.0"

// Config is how a platform app sets the camera up.
type Config struct {
	// Address to listen on, e.g. ":8554". 8554 and not 554, because phones
	// cannot bind ports below 1024.
	Address string `json:"address"`
	// ONVIFAddress is where the ONVIF service listens, e.g. ":8000".
	ONVIFAddress string `json:"onvif_address"`
	// Stream path, e.g. "main" for rtsp://user:pass@host:8554/main.
	Path string `json:"path"`
	User string `json:"user"`
	Pass string `json:"pass"`

	// Identity. NVRs key cameras on these, so UUID and Serial must be stored
	// by the app and survive restarts, or a reconnecting camera looks new.
	UUID   string `json:"uuid"`  // "urn:uuid:…"
	Name   string `json:"name"`  // what the owner calls it, e.g. "Back door"
	Model  string `json:"model"` // the device, e.g. "Pixel 4a"
	Serial string `json:"serial"`

	// What the encoder is actually doing. Reported over ONVIF, and it has to
	// be the truth: UniFi Protect refuses a camera whose rate control is
	// empty, and wrong numbers mislead every NVR that reads them.
	Width   int `json:"width"`
	Height  int `json:"height"`
	FPS     int `json:"fps"`
	Bitrate int `json:"bitrate"`
}

func (c *Config) applyDefaults() {
	if c.Address == "" {
		c.Address = ":8554"
	}
	if c.ONVIFAddress == "" {
		c.ONVIFAddress = ":8000"
	}
	if c.Path == "" {
		c.Path = "main"
	}
	if c.UUID == "" {
		// Better than nothing, but an NVR will see a new camera after every
		// restart; apps are expected to store one.
		c.UUID = "urn:uuid:" + uuid.NewString()
	}
	if c.Name == "" {
		c.Name = "Chameleon"
	}
	if c.Model == "" {
		c.Model = "Chameleon IP"
	}
	if c.Serial == "" {
		c.Serial = strings.TrimPrefix(c.UUID, "urn:uuid:")
	}
	if c.Width == 0 {
		c.Width = 1280
	}
	if c.Height == 0 {
		c.Height = 720
	}
	if c.FPS == 0 {
		c.FPS = 15
	}
	if c.Bitrate == 0 {
		c.Bitrate = 2_000_000
	}
}

// Camera serves one video stream over RTSP.
type Camera struct {
	cfg Config

	// KeyframeWanted is called when a viewer joins. The app must make its
	// encoder emit an IDR at once, otherwise the viewer waits for the next
	// one and sees nothing until then.
	KeyframeWanted func()

	mu       sync.Mutex
	srv      *gortsplib.Server
	stream   *gortsplib.ServerStream
	media    *description.Media
	forma    *format.H264
	enc      *rtph264.Encoder
	sps, pps []byte
	viewers  int
	epoch    time.Time

	onvif     *onvifServer
	discovery *discovery
	notes     map[string]string
	guard     *throttle
}

// New prepares a camera. Nothing listens until Start.
func New(cfg Config) *Camera {
	cfg.applyDefaults()
	return &Camera{cfg: cfg, guard: newThrottle()}
}

// Start begins listening. It returns once the port is open.
func (c *Camera) Start() error {
	if c.cfg.User == "" || c.cfg.Pass == "" {
		return errors.New("refusing to serve without credentials")
	}

	c.forma = &format.H264{PayloadTyp: 96, PacketizationMode: 1}
	c.media = &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{c.forma},
	}

	enc, err := c.forma.CreateEncoder()
	if err != nil {
		return err
	}
	c.enc = enc

	c.srv = &gortsplib.Server{
		Handler:     c,
		RTSPAddress: c.cfg.Address,
		// Digest MD5 only. Basic would put the password on the wire in
		// readable form, and no NVR client supports Digest SHA-256 — it
		// stops ffmpeg authenticating at all.
		AuthMethods: []auth.VerifyMethod{auth.VerifyMethodDigestMD5},
		// Only the local network may even open a connection. A phone often
		// holds a globally routable IPv6 address, and an exposed camera is
		// how tens of thousands of them ended up on Shodan.
		Listen: func(network, address string) (net.Listener, error) {
			ln, err := net.Listen(network, address)
			if err != nil {
				return nil, err
			}
			return localOnlyListener{ln}, nil
		},
	}

	// The listener comes up first: a ServerStream can only be initialized
	// against a started server.
	if err := c.srv.Start(); err != nil {
		return err
	}

	stream := &gortsplib.ServerStream{Server: c.srv, Desc: &description.Session{
		Medias: []*description.Media{c.media},
	}}
	if err := stream.Initialize(); err != nil {
		c.srv.Close()
		return err
	}

	c.mu.Lock()
	c.stream = stream
	c.epoch = time.Now()
	c.mu.Unlock()

	// ONVIF is how NVRs other than Frigate find and configure a camera. If it
	// cannot listen, the stream still works and can be added by address, so
	// the failure is recorded rather than fatal.
	c.onvif = &onvifServer{cam: c}
	if err := c.onvif.start(c.cfg.ONVIFAddress); err != nil {
		c.note("onvif", err)
		c.onvif = nil
	} else {
		c.discovery = &discovery{cam: c}
		if err := c.discovery.start(); err != nil {
			// Windows keeps its own WS-Discovery service on this port.
			c.note("discovery", err)
			c.discovery = nil
		}
	}
	return nil
}

// Note records a non-fatal problem for the app to show. Platform code uses it
// for limitations it hits at runtime.
func (c *Camera) Note(what string, err error) { c.note(what, err) }

// note records a non-fatal startup problem for the app to show.
func (c *Camera) note(what string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.notes == nil {
		c.notes = map[string]string{}
	}
	c.notes[what] = err.Error()
}

// Notes reports what started with a limitation, as "what: why" lines.
func (c *Camera) Notes() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.notes))
	for what, why := range c.notes {
		out = append(out, what+": "+why)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// ready returns the stream once it can serve; a client that connects in the
// moment between the listener opening and the stream existing gets a retry.
func (c *Camera) ready() *gortsplib.ServerStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream
}

// Stop closes the ports, says goodbye over discovery, and drops every viewer.
func (c *Camera) Stop() {
	c.mu.Lock()
	discovery, onvif, srv, stream := c.discovery, c.onvif, c.srv, c.stream
	c.discovery, c.onvif, c.srv, c.stream = nil, nil, nil, nil
	c.mu.Unlock()

	// Outside the lock: closing a server waits for its handlers, and those
	// handlers take this lock.
	if discovery != nil {
		discovery.close()
	}
	if onvif != nil {
		onvif.stop()
	}
	if srv != nil {
		srv.Close()
	}
	if stream != nil {
		stream.Close()
	}
}

// Viewers is how many players are connected right now. With none, the app can
// stop its encoder and let the device cool down.
func (c *Camera) Viewers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.viewers
}

// PushAnnexB sends one encoded frame, as start-code-delimited NAL units, with
// its capture time measured from the first frame.
func (c *Camera) PushAnnexB(frame []byte, pts time.Duration) error {
	return c.PushAU(splitAnnexB(frame), pts)
}

// PushAU sends one access unit, already split into NAL units.
func (c *Camera) PushAU(au [][]byte, pts time.Duration) error {
	if len(au) == 0 {
		return nil
	}
	c.mu.Lock()
	stream := c.stream
	if stream == nil {
		c.mu.Unlock()
		return errors.New("not started")
	}
	if sps, pps := parameterSets(au); sps != nil && pps != nil &&
		(!bytes.Equal(sps, c.sps) || !bytes.Equal(pps, c.pps)) {
		// Resolution or encoder settings changed: tell anyone who asks.
		c.sps, c.pps = sps, pps
		c.forma.SafeSetParams(sps, pps)
		stream.ReloadDesc()
	}
	au = prepareAU(au, c.sps, c.pps)
	c.mu.Unlock()

	pkts, err := c.enc.Encode(au)
	if err != nil {
		return err
	}
	// Timestamps run from one epoch and never restart, even if the encoder
	// is torn down and rebuilt. A reset makes recorders stall at the seam.
	ts := rtpTimestamp(pts)
	ntp := c.epoch.Add(pts)
	for _, pkt := range pkts {
		pkt.Timestamp = ts
		if err := stream.WritePacketRTPWithNTP(c.media, pkt, ntp); err != nil {
			return err
		}
	}
	return nil
}

// rtpTimestamp converts a capture time to RTP's 90 kHz clock.
//
// The arithmetic stays in integers and the truncation to 32 bits is what
// makes it wrap, which is how RTP is meant to behave. Going through a float
// instead looked fine for half a day and then went undefined: past roughly
// 13 hours 15 minutes the value no longer fits a uint32, and a camera runs
// for weeks.
func rtpTimestamp(pts time.Duration) uint32 {
	if pts < 0 {
		return 0
	}
	// Seconds and remainder are converted separately: nanoseconds times
	// 90000 passes what a uint64 can hold after about five hours.
	seconds := uint64(pts / time.Second)
	remainder := uint64(pts % time.Second)
	return uint32(seconds*90000 + remainder*90000/uint64(time.Second))
}

// --- gortsplib handlers ---

func (c *Camera) authorized(conn *gortsplib.ServerConn, req *base.Request) bool {
	addr := conn.NetConn().RemoteAddr()
	now := time.Now()
	if c.guard.blocked(addr, now) {
		return false
	}
	ok := conn.VerifyCredentials(req, c.cfg.User, c.cfg.Pass)
	// Only a wrong password counts. The first request of a session carries
	// none by design, and gortsplib answers that with a challenge.
	if ok {
		c.guard.succeeded(addr, now)
	} else if req.Header["Authorization"] != nil {
		c.guard.failed(addr, now)
	}
	return ok
}

func (c *Camera) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if !c.authorized(ctx.Conn, ctx.Request) {
		// The error is what makes gortsplib attach the Digest challenge;
		// without it the client has nothing to authenticate against.
		return &base.Response{StatusCode: base.StatusUnauthorized}, nil, liberrors.ErrServerAuth{}
	}
	stream := c.ready()
	if stream == nil {
		return &base.Response{StatusCode: base.StatusServiceUnavailable}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, stream, nil
}

func (c *Camera) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if !c.authorized(ctx.Conn, ctx.Request) {
		// The error is what makes gortsplib attach the Digest challenge;
		// without it the client has nothing to authenticate against.
		return &base.Response{StatusCode: base.StatusUnauthorized}, nil, liberrors.ErrServerAuth{}
	}
	stream := c.ready()
	if stream == nil {
		return &base.Response{StatusCode: base.StatusServiceUnavailable}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, stream, nil
}

func (c *Camera) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	c.mu.Lock()
	c.viewers++
	c.mu.Unlock()
	// A viewer that joins mid-GOP sees nothing until the next IDR.
	if c.KeyframeWanted != nil {
		c.KeyframeWanted()
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (c *Camera) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	c.mu.Lock()
	if c.viewers > 0 {
		c.viewers--
	}
	c.mu.Unlock()
}
