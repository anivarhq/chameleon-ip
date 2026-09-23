//go:build !windows

package desktop

// macOS and Linux have no equivalent of DPAPI that works without a login
// session or a keyring daemon, so the settings file is plain and protected by
// its 0600 permissions in the user's own config directory. A password stolen
// from there is one camera on one LAN, and rotating it is one click.
func protect(plain []byte) ([]byte, error) { return plain, nil }

func unprotect(sealed []byte) ([]byte, error) { return sealed, nil }
