package desktop

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Cameras lists the capture devices this machine has, as ffmpeg names them.
func Cameras(ffmpeg string) ([]string, error) {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	switch runtime.GOOS {
	case "windows":
		return dshowCameras(ffmpeg)
	case "darwin":
		return avfoundationCameras(ffmpeg)
	default:
		return v4l2Cameras()
	}
}

// FirstCamera is the device used when the user has not chosen one.
func FirstCamera(ffmpeg string) (string, error) {
	cams, err := Cameras(ffmpeg)
	if err != nil {
		return "", err
	}
	if len(cams) == 0 {
		return "", fmt.Errorf("no camera found on this machine")
	}
	return cams[0], nil
}

// ffmpeg prints its device list to stderr and exits non-zero by design.
func listOutput(ffmpeg string, args ...string) string {
	cmd := exec.Command(ffmpeg, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	return out.String()
}

var dshowVideoName = regexp.MustCompile(`"([^"]+)"\s*\(video\)`)

func dshowCameras(ffmpeg string) ([]string, error) {
	out := listOutput(ffmpeg, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	var cams []string
	for _, m := range dshowVideoName.FindAllStringSubmatch(out, -1) {
		cams = append(cams, m[1])
	}
	return cams, nil
}

var avfIndexedName = regexp.MustCompile(`\[(\d+)\]\s+(.+)`)

func avfoundationCameras(ffmpeg string) ([]string, error) {
	out := listOutput(ffmpeg, "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	var cams []string
	inVideo := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "AVFoundation video devices"):
			inVideo = true
			continue
		case strings.Contains(line, "AVFoundation audio devices"):
			inVideo = false
			continue
		}
		if !inVideo {
			continue
		}
		if m := avfIndexedName.FindStringSubmatch(line); m != nil {
			// avfoundation takes the index, not the name.
			cams = append(cams, m[1])
		}
	}
	return cams, nil
}

func v4l2Cameras() ([]string, error) {
	return filepath.Glob("/dev/video*")
}

// BestEncoder picks the hardware encoder this machine has, falling back to
// software. Hardware matters: a camera runs all day, and software encoding a
// 1080p stream burns a core for no benefit.
func BestEncoder(ffmpeg string) string {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	have := listOutput(ffmpeg, "-hide_banner", "-encoders")
	order := []string{"h264_nvenc", "h264_qsv", "h264_amf", "h264_videotoolbox", "h264_vaapi"}
	if runtime.GOOS == "darwin" {
		order = []string{"h264_videotoolbox"}
	}
	for _, enc := range order {
		if strings.Contains(have, enc) {
			return enc
		}
	}
	return "libx264"
}
