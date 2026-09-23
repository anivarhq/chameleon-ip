package desktop

import (
	"net"
	"os"
	"runtime"
	"strings"
)

// Status is what the desktop app reads from the engine's output.
type Status struct {
	StreamURL string `json:"stream_url"`
	ONVIFPort int    `json:"onvif_port"`
	Camera    string `json:"camera"`
	Viewers   int    `json:"viewers"`
	Notes     string `json:"notes,omitempty"`
	Error     string `json:"error,omitempty"`
}

// LANAddress is the address to show people, i.e. the one an NVR on the same
// network can reach.
func LANAddress() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				return n.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

// MachineModel is what NVRs show as the camera's model.
func MachineModel() string {
	name, _ := os.Hostname()
	name = strings.TrimSpace(name)
	if name == "" {
		name = "computer"
	}
	return name + " (" + runtime.GOOS + ")"
}
