// Package core serves one device's camera to NVRs.
//
// The platform app captures and hardware-encodes; it hands the encoded access
// units here, and this package does the network side: an RTSP server with
// Digest authentication, shared by Android, Apple and desktop.
package core

import (
	"bytes"
	"errors"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/auth"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/liberrors"
)

// Config is how a platform app sets the camera up.
type Config struct {
	// Address to listen on, e.g. ":8554". 8554 and not 554, because phones
	// cannot bind ports below 1024.
	Address string
	// Stream path, e.g. "main" for rtsp://user:pass@host:8554/main.
	Path string
	User string
	Pass string
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
}

// New prepares a camera. Nothing listens until Start.
func New(cfg Config) *Camera {
	if cfg.Address == "" {
		cfg.Address = ":8554"
	}
	if cfg.Path == "" {
		cfg.Path = "main"
	}
	return &Camera{cfg: cfg}
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
	return nil
}

// ready returns the stream once it can serve; a client that connects in the
// moment between the listener opening and the stream existing gets a retry.
func (c *Camera) ready() *gortsplib.ServerStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream
}

// Stop closes the port and drops every viewer.
func (c *Camera) Stop() {
	if c.srv != nil {
		c.srv.Close()
	}
	if c.stream != nil {
		c.stream.Close()
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
	ts := uint32(pts.Seconds() * 90000)
	ntp := c.epoch.Add(pts)
	for _, pkt := range pkts {
		pkt.Timestamp = ts
		if err := stream.WritePacketRTPWithNTP(c.media, pkt, ntp); err != nil {
			return err
		}
	}
	return nil
}

// --- gortsplib handlers ---

func (c *Camera) authorized(conn *gortsplib.ServerConn, req *base.Request) bool {
	return conn.VerifyCredentials(req, c.cfg.User, c.cfg.Pass)
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
