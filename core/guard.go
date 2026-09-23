package core

import (
	"net"
	"strings"
	"sync"
	"time"
)

// Who is allowed to reach the camera, and how often they may get it wrong.
//
// Both rules come from what happens to cameras that skip them: tens of
// thousands sit on Shodan because they were reachable from the internet, and
// devices that lock an account after N failures let anyone on the network
// lock the owner's own NVR out of its camera.

// allowedSource reports whether an address may talk to the camera at all.
//
// Only the local network: RFC 1918, carrier-grade NAT (which is what
// Tailscale uses), link-local and unique-local IPv6, plus loopback. A phone
// usually holds a globally routable IPv6 address, so without this the camera
// would be reachable from the internet the moment IPv6 works.
func allowedSource(addr net.Addr) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	// A link-local address carries a zone, "fe80::1%eth0", which the parser
	// does not take.
	if cut := strings.IndexByte(host, '%'); cut >= 0 {
		host = host[:cut]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	// 100.64.0.0/10, carrier-grade NAT, is how a tailnet addresses peers.
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return false
}

// throttle slows down repeated wrong passwords from one address.
//
// It never locks the account, only the address that keeps guessing, and an
// address that has authenticated recently is exempt — otherwise an attacker
// on the network could keep an NVR from reconnecting by failing on its behalf.
type throttle struct {
	mu       sync.Mutex
	failures map[string]*attempts
}

type attempts struct {
	count     int
	blockedAt time.Time
	lastGood  time.Time
}

const (
	freeAttempts = 5
	maxBlock     = 5 * time.Minute
	trustWindow  = 24 * time.Hour
)

func newThrottle() *throttle { return &throttle{failures: map[string]*attempts{}} }

// blocked reports whether this address must wait before trying again.
func (t *throttle) blocked(addr net.Addr, now time.Time) bool {
	key := hostOf(addr)
	t.mu.Lock()
	defer t.mu.Unlock()

	a := t.failures[key]
	if a == nil {
		return false
	}
	if now.Sub(a.lastGood) < trustWindow {
		return false // a client that got it right today is not an attacker
	}
	if a.count <= freeAttempts {
		return false
	}
	return now.Before(a.blockedAt.Add(backoff(a.count)))
}

// failed records a wrong password. Only wrong credentials count: the first
// request of any session carries none by design, and a stale nonce is a
// normal retry.
func (t *throttle) failed(addr net.Addr, now time.Time) {
	key := hostOf(addr)
	t.mu.Lock()
	defer t.mu.Unlock()

	a := t.failures[key]
	if a == nil {
		a = &attempts{}
		t.failures[key] = a
	}
	a.count++
	a.blockedAt = now
	t.prune(now)
}

// succeeded clears the count and marks the address as trusted for a day.
func (t *throttle) succeeded(addr net.Addr, now time.Time) {
	key := hostOf(addr)
	t.mu.Lock()
	defer t.mu.Unlock()

	a := t.failures[key]
	if a == nil {
		a = &attempts{}
		t.failures[key] = a
	}
	a.count, a.lastGood = 0, now
	t.prune(now)
}

// prune keeps the table from growing without bound while someone hammers it
// from a wide range of addresses. Caller holds the lock.
func (t *throttle) prune(now time.Time) {
	if len(t.failures) < 4096 {
		return
	}
	for key, a := range t.failures {
		if now.Sub(a.blockedAt) > maxBlock && now.Sub(a.lastGood) > trustWindow {
			delete(t.failures, key)
		}
	}
}

// backoff doubles with each failure past the free ones, up to five minutes.
func backoff(count int) time.Duration {
	over := count - freeAttempts
	if over < 1 {
		return 0
	}
	if over > 12 {
		return maxBlock
	}
	d := time.Second << uint(over-1)
	if d > maxBlock {
		return maxBlock
	}
	return d
}

func hostOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// localOnlyListener drops connections from outside the local network before
// the protocol ever sees them.
type localOnlyListener struct{ net.Listener }

func (l localOnlyListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if allowedSource(conn.RemoteAddr()) {
			return conn, nil
		}
		_ = conn.Close()
	}
}
