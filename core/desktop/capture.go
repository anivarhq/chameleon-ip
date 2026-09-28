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
	"io"
	"net"
	"os/exec"
	"runtime"
	"strconv"
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

	// Preview, when set, receives small JPEGs of what the camera sees (640 px
	// wide, 10 a second) for the desktop window. It must not block: a slow
	// preview would otherwise stall ffmpeg, and the stream with it.
	Preview func(jpeg []byte)
}

// previewBoundary separates the preview JPEGs on the loopback socket.
const previewBoundary = "chameleonpreview"

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

	// The preview is a second output of the same ffmpeg: the camera is opened
	// and decoded once, the stream NVRs get is untouched, and the only extra
	// work is a small JPEG encode. It reaches us over a loopback socket, since
	// stdout already carries the stream and Windows gives a child no other pipe.
	args := o.args()
	var preview net.Listener
	if o.Preview != nil {
		if ln, err := net.Listen("tcp", "127.0.0.1:0"); err != nil {
			cam.Note("preview", err) // the stream matters more than the window's picture
		} else {
			preview = ln
			defer ln.Close()
			args = append(args, previewArgs(ln.Addr().String())...)
		}
	}

	cmd := exec.CommandContext(ctx, o.FFmpeg, args...)
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

	// Tie ffmpeg's life to ours, so a hard kill cannot leave it running with
	// the camera light on.
	release, err := superviseChild(cmd)
	if err != nil {
		// Worth knowing, not worth refusing to stream over.
		cam.Note("child supervision", err)
	}
	defer release()

	if preview != nil {
		go readPreview(preview, o.Preview)
	}

	// ffmpeg cannot be asked for a keyframe on demand, so a viewer joining
	// mid-GOP waits for the next one; the 2 s interval bounds that wait.
	scanner := &nalScanner{r: bufio.NewReaderSize(stdout, 1<<20)}

	// ffmpeg's raw H.264 output carries no timestamps, so each frame is
	// stamped with the time it arrived. Counting frames against the
	// configured rate instead looks tidier but drifts whenever the camera
	// delivers at a slightly different rate — measured at 0.9x here, which
	// is six minutes of drift an hour in an NVR's recording.
	start := time.Now()
	for {
		au, err := readAccessUnit(scanner)
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

// previewArgs adds ffmpeg's second output: 640-pixel JPEGs at 10 fps, each
// with its length in a multipart header, sent to the engine over loopback.
func previewArgs(addr string) []string {
	return []string{
		"-vf", "fps=10,scale=640:-2",
		"-c:v", "mjpeg", "-q:v", "7",
		"-f", "mpjpeg", "-boundary_tag", previewBoundary,
		"tcp://" + addr,
	}
}

// readPreview takes the preview JPEGs off the socket and hands each to fn. It
// accepts exactly one connection, ffmpeg's, then stops listening.
func readPreview(ln net.Listener, fn func([]byte)) {
	conn, err := ln.Accept()
	_ = ln.Close()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = splitPreview(conn, fn)
}

// splitPreview reads ffmpeg's mpjpeg stream and hands over each JPEG as soon
// as its Content-length worth of bytes is in. (A multipart reader waits for
// the NEXT boundary to end a part, which kept the window a frame behind.)
func splitPreview(r io.Reader, fn func([]byte)) error {
	br := bufio.NewReaderSize(r, 1<<16)
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "" && length >= 0:
			// A preview frame is tens of kilobytes; anything past 8 MB is not one.
			if length > 8<<20 {
				return fmt.Errorf("preview frame of %d bytes", length)
			}
			jpeg := make([]byte, length)
			if _, err := io.ReadFull(br, jpeg); err != nil {
				return err
			}
			if length > 2 && jpeg[0] == 0xFF && jpeg[1] == 0xD8 {
				fn(jpeg)
			}
			length = -1
		case strings.HasPrefix(strings.ToLower(line), "content-length:"):
			if length, err = strconv.Atoi(strings.TrimSpace(line[len("content-length:"):])); err != nil {
				return err
			}
		}
	}
}

// nalScanner splits an Annex-B stream into NAL units.
//
// It carries its own state rather than pushing bytes back into the reader:
// bufio can unread exactly one byte and a start code is three or four, so the
// obvious version silently skipped to the next start code and dropped every
// other NAL unit. That halved the frame rate and looked like a slow webcam.
type nalScanner struct {
	r       *bufio.Reader
	current []byte
	inNALU  bool
}

// next returns the next NAL unit, without its start code.
//
// Zero bytes immediately before a start code are treated as part of it. The
// H.264 syntax allows both readings, and a decoder ignores trailing zeros.
func (s *nalScanner) next() ([]byte, error) {
	zeros := 0
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			if s.inNALU && len(s.current) > 0 {
				out := s.current
				s.current, s.inNALU = nil, false
				return out, nil
			}
			return nil, err
		}

		switch {
		case b == 0:
			zeros++

		case b == 1 && zeros >= 2:
			// A start code: whatever came before it is a complete NAL unit.
			out := s.current
			s.current, s.inNALU = nil, true
			zeros = 0
			if len(out) > 0 {
				return out, nil
			}

		default:
			if s.inNALU {
				for i := 0; i < zeros; i++ {
					s.current = append(s.current, 0)
				}
				s.current = append(s.current, b)
			}
			zeros = 0
		}
	}
}

// readAccessUnit reads NAL units until the next frame starts.
func readAccessUnit(s *nalScanner) ([][]byte, error) {
	var au [][]byte
	for {
		nalu, err := s.next()
		if err != nil {
			return nil, err
		}
		au = append(au, nalu)
		// A coded slice ends the frame; anything after it starts the next.
		if t := nalu[0] & 0x1F; t >= 1 && t <= 5 {
			return au, nil
		}
		if len(au) > 64 {
			return au, nil // malformed stream; do not grow without bound
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
