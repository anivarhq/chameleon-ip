// Package mobile is the surface gomobile binds for Android and Apple.
//
// It stays deliberately narrow, because every change here has to be mirrored
// in Kotlin and Swift: start, stop, push a frame, ask how many are watching.
// Everything else is configured through one JSON string, so adding a setting
// never changes the binding.
//
// A Go panic crossing this boundary kills the host app, so every exported
// function recovers.
package mobile

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/anivarhq/chameleon-ip/core"
)

var (
	mu  sync.Mutex
	cam *core.Camera
	// keyframeWanted is set when a viewer joins; the app polls it on its
	// encoder thread. A callback interface would work too, but polling a
	// bool avoids a Java/ObjC call on every frame.
	keyframeWanted bool
)

// Start opens the RTSP port. config is JSON:
//
//	{"address":":8554","path":"main","user":"admin","pass":"…"}
func Start(config string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("chameleon: start panicked")
		}
	}()

	var cfg core.Config
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		return err
	}

	mu.Lock()
	defer mu.Unlock()
	if cam != nil {
		return errors.New("already started")
	}
	c := core.New(cfg)
	c.KeyframeWanted = func() {
		mu.Lock()
		keyframeWanted = true
		mu.Unlock()
	}
	if err := c.Start(); err != nil {
		return err
	}
	cam = c
	return nil
}

// Stop closes the port and drops every viewer.
func Stop() {
	defer func() { _ = recover() }()
	mu.Lock()
	defer mu.Unlock()
	if cam != nil {
		cam.Stop()
		cam = nil
	}
}

// PushFrame sends one encoded frame: start-code-delimited NAL units, and the
// capture time in microseconds since the first frame.
func PushFrame(frame []byte, ptsMicros int64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("chameleon: push panicked")
		}
	}()
	mu.Lock()
	c := cam
	mu.Unlock()
	if c == nil {
		return errors.New("not started")
	}
	return c.PushAnnexB(frame, time.Duration(ptsMicros)*time.Microsecond)
}

// Viewers is how many players are connected. At zero, the app can stop
// encoding and let the device cool down.
func Viewers() int {
	mu.Lock()
	defer mu.Unlock()
	if cam == nil {
		return 0
	}
	return cam.Viewers()
}

// TakeKeyframeWanted reports whether a viewer joined since it was last called.
// When true, the app asks its encoder for an immediate keyframe.
func TakeKeyframeWanted() bool {
	mu.Lock()
	defer mu.Unlock()
	w := keyframeWanted
	keyframeWanted = false
	return w
}
