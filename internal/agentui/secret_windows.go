//go:build windows

package agentui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

const (
	dpapiPrefix          = "dpapi:"
	cryptProtectUIForbid = 0x01
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

func tokenStorageDescription() string {
	return "Windows DPAPI 当前用户加密"
}

func sealToken(plain string) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", nil
	}
	data := []byte(plain)
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbid,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return "", fmt.Errorf("DPAPI encrypt token: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	buf := unsafe.Slice(out.pbData, out.cbData)
	copied := append([]byte(nil), buf...)
	return dpapiPrefix + base64.StdEncoding.EncodeToString(copied), nil
}

func unsealToken(sealed string) (string, error) {
	sealed = strings.TrimSpace(sealed)
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, dpapiPrefix) {
		return "", errors.New("unsupported token encoding on Windows")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, dpapiPrefix))
	if err != nil {
		return "", fmt.Errorf("decode DPAPI token: %w", err)
	}
	if len(raw) == 0 {
		return "", nil
	}
	in := dataBlob{cbData: uint32(len(raw)), pbData: &raw[0]}
	var out dataBlob
	r, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbid,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return "", fmt.Errorf("DPAPI decrypt token: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	buf := unsafe.Slice(out.pbData, out.cbData)
	return strings.TrimSpace(string(buf)), nil
}

func deleteToken(string) error { return nil }
