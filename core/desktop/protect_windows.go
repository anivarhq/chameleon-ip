package desktop

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the settings file is sealed with DPAPI, so the password is tied
// to this user account: another account on the same machine cannot read it.

var (
	crypt32            = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) dataBlob {
	if len(d) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b dataBlob) bytes() []byte {
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

func protect(plain []byte) ([]byte, error) { return dpapi(procCryptProtect, plain) }

func unprotect(sealed []byte) ([]byte, error) { return dpapi(procCryptUnprotect, sealed) }

func dpapi(proc *windows.LazyProc, in []byte) ([]byte, error) {
	inBlob, outBlob := newBlob(in), dataBlob{}
	r, _, err := proc.Call(
		uintptr(unsafe.Pointer(&inBlob)), 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, fmt.Errorf("dpapi: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.pbData)))
	return outBlob.bytes(), nil
}
