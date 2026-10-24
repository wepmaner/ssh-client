package store

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func blobOf(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{}
	}
	// DPAPI inputs are small (keys), so the length always fits uint32.
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]} //nolint:gosec // G115: DPAPI inputs are small, length fits uint32
}

func takeBlob(out windows.DataBlob) []byte {
	res := make([]byte, out.Size)
	src := unsafe.Slice(out.Data, out.Size) //nolint:gosec // G103: reading the DPAPI-owned output buffer
	copy(res, src)
	clear(src)                                                         // do not leave plaintext in freed memory
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) //nolint:gosec // G103,G104: nothing to do if free fails
	return res
}

// dpapiProtect encrypts b for the current Windows user.
func dpapiProtect(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("store: dpapi: empty input")
	}
	in := blobOf(b)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}

// dpapiUnprotect decrypts a blob produced by dpapiProtect for the same user.
func dpapiUnprotect(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("store: dpapi: empty input")
	}
	in := blobOf(b)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(out), nil
}
