// Command chameleon turns this computer's webcam into an IP camera.
//
// It runs on its own, and it is also what the desktop app drives: the app
// starts it and reads the JSON status lines it prints.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anivarhq/chameleon-ip/core"
	"github.com/anivarhq/chameleon-ip/core/desktop"
)

func main() {
	var (
		ffmpeg   = flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
		device   = flag.String("device", "", "camera to use; default is the first one")
		list     = flag.Bool("list", false, "list cameras and exit")
		jsonOut  = flag.Bool("json", false, "print status as JSON lines, for the desktop app")
		rotate   = flag.Bool("rotate-password", false, "generate a new password and exit")
		rtspPort = flag.Int("port", 0, "RTSP port (default 8554)")
	)
	flag.Parse()

	if *list {
		cams, err := desktop.Cameras(*ffmpeg)
		if err != nil {
			log.Fatalf("listing cameras: %v", err)
		}
		for _, c := range cams {
			fmt.Println(c)
		}
		return
	}

	settings, err := desktop.LoadSettings()
	if err != nil {
		log.Fatalf("settings: %v", err)
	}
	if *rotate {
		if settings.Pass, err = desktop.NewPassword(); err != nil {
			log.Fatalf("new password: %v", err)
		}
		if err := settings.Save(); err != nil {
			log.Fatalf("save: %v", err)
		}
		fmt.Println("new password:", settings.Pass)
		return
	}
	if *device != "" {
		settings.Device = *device
	}
	if *rtspPort != 0 {
		settings.RTSPPort = *rtspPort
	}

	cam := core.New(core.Config{
		Address:      fmt.Sprintf(":%d", settings.RTSPPort),
		ONVIFAddress: fmt.Sprintf(":%d", settings.HTTPPort),
		Path:         "main",
		User:         settings.User,
		Pass:         settings.Pass,
		UUID:         settings.UUID,
		Name:         settings.Name,
		Model:        desktop.MachineModel(),
		Serial:       lastRunes(settings.UUID, 12),
		Width:        settings.Width,
		Height:       settings.Height,
		FPS:          settings.FPS,
		Bitrate:      settings.Bitrate,
	})
	if err := cam.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	defer cam.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	status := desktop.Status{
		StreamURL: fmt.Sprintf("rtsp://%s:%s@%s:%d/main",
			settings.User, settings.Pass, desktop.LANAddress(), settings.RTSPPort),
		ONVIFPort: settings.HTTPPort,
		Camera:    settings.Device,
		Notes:     cam.Notes(),
	}
	if *jsonOut {
		emit(status)
	} else {
		fmt.Println("Add this to your NVR:")
		fmt.Println("   ", status.StreamURL)
		if status.Notes != "" {
			fmt.Println("Limitations:", status.Notes)
		}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				status.Viewers = cam.Viewers()
				if *jsonOut {
					emit(status)
				}
			}
		}
	}()

	err = desktop.Capture(ctx, cam, desktop.Options{
		FFmpeg: *ffmpeg, Device: settings.Device,
		Width: settings.Width, Height: settings.Height,
		FPS: settings.FPS, Bitrate: settings.Bitrate,
	})
	if err != nil && ctx.Err() == nil {
		if *jsonOut {
			status.Error = err.Error()
			emit(status)
		}
		log.Fatalf("capture: %v", err)
	}
}

// lastRunes is the tail of a string, or all of it when it is shorter. Slicing
// blind would panic on a settings file someone had edited.
func lastRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func emit(s desktop.Status) {
	line, err := json.Marshal(s)
	if err != nil {
		return
	}
	fmt.Println(string(line))
}
