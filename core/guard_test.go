package core

import (
	"net"
	"strings"
	"testing"
	"time"
)

type fakeAddr string

func (f fakeAddr) Network() string { return "tcp" }
func (f fakeAddr) String() string  { return string(f) }

func TestOnlyTheLocalNetworkIsAllowed(t *testing.T) {
	allowed := []string{
		"192.168.1.10:5000", "10.0.0.4:5000", "172.16.9.9:5000",
		"127.0.0.1:5000", "[::1]:5000",
		"100.101.102.103:5000", // Tailscale
		"[fe80::1%eth0]:5000",
	}
	for _, addr := range allowed {
		if !allowedSource(fakeAddr(addr)) {
			t.Errorf("%s should be allowed", addr)
		}
	}

	refused := []string{
		"8.8.8.8:5000",
		"203.0.113.7:5000",
		// A phone's own IPv6 is globally routable: without this rule the
		// camera answers the internet.
		"[2a00:1450:4009:81f::200e]:5000",
		"not-an-address",
	}
	for _, addr := range refused {
		if allowedSource(fakeAddr(addr)) {
			t.Errorf("%s should be refused", addr)
		}
	}
}

func TestThrottleSlowsGuessingButNotTheNVR(t *testing.T) {
	now := time.Now()
	guard := newThrottle()
	attacker := fakeAddr("192.168.1.66:40000")

	// A few wrong tries are free: NVRs retry, people mistype.
	for i := 0; i < freeAttempts; i++ {
		guard.failed(attacker, now)
		if guard.blocked(attacker, now) {
			t.Fatalf("blocked after only %d failures", i+1)
		}
	}

	// Past that it backs off.
	guard.failed(attacker, now)
	if !guard.blocked(attacker, now) {
		t.Fatal("guessing was not slowed down at all")
	}

	// The block lifts on its own.
	if guard.blocked(attacker, now.Add(maxBlock+time.Second)) {
		t.Error("the block never lifted")
	}

	// One address guessing must not affect another.
	if guard.blocked(fakeAddr("192.168.1.99:40000"), now) {
		t.Error("a different address was caught by someone else's failures")
	}
}

func TestAnNVRThatWorkedTodayIsNeverBlocked(t *testing.T) {
	now := time.Now()
	guard := newThrottle()
	nvr := fakeAddr("192.168.1.5:50000")

	guard.succeeded(nvr, now)
	// Something on the network fails on its behalf, or its own retry races
	// the password rotation.
	for i := 0; i < 50; i++ {
		guard.failed(nvr, now)
	}
	if guard.blocked(nvr, now) {
		t.Error("an NVR that authenticated today was locked out")
	}

	// A day later the trust has expired, so fresh guessing from that address
	// is slowed down like anyone else's. (Old failures alone do not keep it
	// blocked: a block that outlives its cause is its own outage.)
	later := now.Add(trustWindow + time.Minute)
	if guard.blocked(nvr, later) {
		t.Error("an old block was still in force long after the failures")
	}
	for i := 0; i <= freeAttempts; i++ {
		guard.failed(nvr, later)
	}
	if !guard.blocked(nvr, later) {
		t.Error("the trust window never expires")
	}
}

func TestBackoffGrowsAndStops(t *testing.T) {
	if backoff(freeAttempts) != 0 {
		t.Error("free attempts should not wait")
	}
	if backoff(freeAttempts+1) >= backoff(freeAttempts+2) {
		t.Error("the wait should grow with each failure")
	}
	if got := backoff(freeAttempts + 100); got != maxBlock {
		t.Errorf("backoff caps at %v, got %v", maxBlock, got)
	}
}

// The listener has to refuse a remote address before any protocol code runs.
func TestListenerRefusesOutsideAddresses(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	guarded := localOnlyListener{inner}
	defer guarded.Close()

	done := make(chan string, 1)
	go func() {
		conn, err := guarded.Accept()
		if err != nil {
			done <- "accept failed: " + err.Error()
			return
		}
		defer conn.Close()
		done <- conn.RemoteAddr().String()
	}()

	client, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	select {
	case got := <-done:
		if !strings.HasPrefix(got, "127.0.0.1:") {
			t.Errorf("accepted %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Error("a loopback connection was not accepted")
	}
}
