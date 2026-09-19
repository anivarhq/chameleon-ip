// filefeed serves an H.264 Annex-B file as if it were a camera, so the core
// can be tested with a real player before any phone code exists:
//
//	ffmpeg -f lavfi -i testsrc=size=1280x720:rate=15 -t 10 -c:v libx264 \
//	    -bsf:v h264_mp4toannexb -g 30 out.h264
//	go run ./cmd/filefeed -file out.h264
//	ffmpeg -rtsp_transport tcp -i rtsp://admin:test1234@127.0.0.1:8554/main -t 3 -f null -
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/anivarhq/chameleon-ip/core"
)

func main() {
	file := flag.String("file", "", "H.264 Annex-B file to loop")
	addr := flag.String("addr", ":8554", "listen address")
	user := flag.String("user", "admin", "username")
	pass := flag.String("pass", "test1234", "password")
	fps := flag.Int("fps", 15, "frames per second")
	flag.Parse()

	raw, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("read %s: %v", *file, err)
	}
	frames := core.SplitAccessUnits(raw)
	if len(frames) == 0 {
		log.Fatalf("%s holds no access units", *file)
	}

	cam := core.New(core.Config{Address: *addr, Path: "main", User: *user, Pass: *pass})
	cam.KeyframeWanted = func() { log.Print("viewer joined, keyframe requested") }
	if err := cam.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	defer cam.Stop()
	log.Printf("serving rtsp://%s:%s@%s/main — %d frames", *user, *pass, *addr, len(frames))

	interval := time.Second / time.Duration(*fps)
	tick := time.NewTicker(interval)
	defer tick.Stop()

	var pts time.Duration
	for i := 0; ; i = (i + 1) % len(frames) {
		<-tick.C
		if err := cam.PushAU(frames[i], pts); err != nil {
			log.Printf("push: %v", err)
		}
		// Keeps rising across loops, so timestamps never restart.
		pts += interval
	}
}
