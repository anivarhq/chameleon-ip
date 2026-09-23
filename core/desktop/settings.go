package desktop

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Settings survive restarts. The UUID especially: NVRs key a camera on it,
// so a new one every launch looks like a new camera every launch.
type Settings struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Pass     string `json:"pass"`
	Device   string `json:"device,omitempty"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FPS      int    `json:"fps"`
	Bitrate  int    `json:"bitrate"`
	RTSPPort int    `json:"rtsp_port"`
	HTTPPort int    `json:"onvif_port"`
}

// LoadSettings reads the stored settings, creating them on first run.
func LoadSettings() (Settings, error) {
	path, err := settingsPath()
	if err != nil {
		return Settings{}, err
	}

	var s Settings
	if raw, err := os.ReadFile(path); err == nil {
		if plain, err := unprotect(raw); err == nil {
			if json.Unmarshal(plain, &s) == nil && s.Pass != "" {
				return s, nil
			}
		}
		// Unreadable, e.g. copied from another machine: start fresh rather
		// than serve with a password nobody can see.
	}

	pass, err := NewPassword()
	if err != nil {
		return Settings{}, err
	}
	s = Settings{
		UUID: "urn:uuid:" + uuid.NewString(),
		Name: hostname(),
		User: "admin", Pass: pass,
		Width: 1280, Height: 720, FPS: 15, Bitrate: 2_000_000,
		RTSPPort: 8554, HTTPPort: 8000,
	}
	return s, s.Save()
}

// Save writes the settings back, protected by the operating system where it
// offers that: the password cannot be hashed, because ONVIF and RTSP Digest
// both need the original to verify a request.
func (s Settings) Save() error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	plain, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	sealed, err := protect(plain)
	if err != nil {
		return err
	}
	return os.WriteFile(path, sealed, 0o600)
}

// NewPassword generates the device's own password: 16 characters, no
// look-alikes, alphanumeric so it survives an rtsp:// URL unescaped and fits
// NVR fields that cap at 16.
func NewPassword() (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 16)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ChameleonIP", "settings.json"), nil
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "Chameleon"
	}
	return name
}
