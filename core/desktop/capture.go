// Package desktop captures a webcam on Windows, macOS or Linux and feeds it
// to the shared core.
//
// Capture goes through ffmpeg rather than each platform's own API: it already
// knows every webcam quirk on three operating systems, it uses the hardware
// encoder when there is one, and the alternative is three more capture stacks
// to maintain. The cost is one child process and one pipe.
package desktop

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/anivarhq/chameleon-ip/core"
)

// Options describe what to capture and how.
type Options struct {
	FFmpeg  string // path to ffmpeg; "ffmpeg" if it is on PATH
	Device  string // platform device name; empty means the first camera
	Width   int
	Height  int
	FPS     int
	Bitrate int
	Encoder string // empty means pick the best available
}

// Capture runs ffmpeg and pushes every encoded frame into the camera until
// the context is cancelled.
func Capture(ctx context.Context, cam *core.Camera, o Options) error {
	if o.FFmpeg == "" {
		o.FFmpeg = "ffmpeg"
	}
	if o.Device == "" {
		device, err := FirstCamera(o.FFmpeg)
		if err != nil {
			return err
		}
		o.Device = device
	}
	if o.Encoder == "" {
		o.Encoder = BestEncoder(o.FFmpeg)
	}

	cmd := exec.CommandContext(ctx, o.FFmpeg, o.args()...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", o.FFmpeg, err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	// ffmpeg cannot be asked for a keyframe on demand, so a viewer joining
	// mid-GOP waits for the next one; the 2 s interval bounds that wait.
	reader := bufio.NewReaderSize(stdout, 1<<20)

	// ffmpeg's raw H.264 output carries no timestamps, so each frame is
	// stamped with the time it arrived. Counting frames against the
	// configured rate instead looks tidier but drifts whenever the camera
	// delivers at a slightly different rate — measured at 0.9x here, which
	// is six minutes of drift an hour in an NVR's recording.
	start := time.Now()
	for {
		au, err := readAccessUnit(reader)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%s stopped: %w: %s", o.FFmpeg, err, tail(errBuf.String()))
		}
		if err := cam.PushAU(au, time.Since(start)); err != nil {
			return err
		}
	}
}

func (o Options) args() []string {
	args := []string{"-hide_banner", "-loglevel", "error"}

	switch runtime.GOOS {
	case "windows":
		args = append(args, "-f", "dshow", "-video_size", o.size(), "-framerate", fmt.Sprint(o.FPS),
			"-rtbufsize", "64M", "-i", "video="+o.Device)
	case "darwin":
		args = append(args, "-f", "avfoundation", "-video_size", o.size(), "-framerate", fmt.Sprint(o.FPS),
			"-i", o.Device)
	default:
		args = append(args, "-f", "v4l2", "-video_size", o.size(), "-framerate", fmt.Sprint(o.FPS),
			"-i", o.Device)
	}

	args = append(args,
		"-c:v", o.Encoder,
		"-b:v", fmt.Sprint(o.Bitrate),
		"-g", fmt.Sprint(o.FPS*2), // a keyframe every 2 seconds
		"-pix_fmt", "yuv420p",
		"-bf", "0", // B-frames would reorder timestamps for no benefit here
	)
	if o.Encoder == "libx264" {
		args = append(args, "-preset", "veryfast", "-tune", "zerolatency")
	}
	return append(args, "-f", "h264", "-")
}

func (o Options) size() string { return fmt.Sprintf("%dx%d", o.Width, o.Height) }

// readAccessUnit reads NAL units until the next frame starts.
func readAccessUnit(r *bufio.Reader) ([][]byte, error) {
	var au [][]byte
	for {
		nalu, err := readNALU(r)
		if err != nil {
			return nil, err
		}
		au = append(au, nalu)
		// A slice ends the frame; anything after it belongs to the next one.
		if t := nalu[0] & 0x1F; t >= 1 && t <= 5 {
			return au, nil
		}
		if len(au) > 64 {
			return au, nil // malformed stream; don't grow without bound
		}
	}
}

// readNALU reads one start-code-delimited NAL unit.
func readNALU(r *bufio.Reader) ([]byte, error) {
	// Skip to the first start code.
	if err := skipStartCode(r); err != nil {
		return nil, err
	}
	var out []byte
	zeros := 0
	for {
		b, err := r.ReadByte()
		if err != nil {
			if len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		if b == 0 {
			zeros++
			continue
		}
		if b == 1 && zeros >= 2 {
			// Next start code: give back the bytes before it, minus the
			// zeros that belong to the start code.
			if err := r.UnreadByte(); err != nil {
				return nil, err
			}
			for i := 0; i < zeros; i++ {
				_ = r.UnreadByte()
			}
			_ = r.UnreadByte()
			if len(out) > 0 {
				return out, nil
			}
			return nil, fmt.Errorf("empty NAL unit")
		}
		for i := 0; i < zeros; i++ {
			out = append(out, 0)
		}
		zeros = 0
		out = append(out, b)
	}
}

func skipStartCode(r *bufio.Reader) error {
	zeros := 0
	for {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		switch {
		case b == 0:
			zeros++
		case b == 1 && zeros >= 2:
			return nil
		default:
			zeros = 0
		}
	}
}

func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "; ")
}
