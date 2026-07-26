//go:build windows

package platform

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// DecodeOutput converts captured child-process output to UTF-8.
//
// Console programs on Windows write in the console output code page (866 on a
// Russian install, 437 on a US one), so their bytes are not UTF-8. Commands run
// through wrapPowerShellCommand switch to UTF-8 themselves, but a PowerShell
// script that fails to parse never reaches its own prologue, and its parser
// error still arrives in the legacy code page - that is the output the model has
// to read to recover. Output that is already valid UTF-8 is returned untouched,
// which keeps the wrapped case a no-op.
func DecodeOutput(b []byte) string {
	if len(b) == 0 || utf8.Valid(b) {
		return string(b)
	}
	codePage, err := windows.GetConsoleOutputCP()
	if err != nil || codePage == 0 {
		return string(b)
	}
	decoded, err := decodeCodePage(codePage, b)
	if err != nil {
		return string(b)
	}
	return decoded
}

// decodeCodePage converts bytes in the given Windows code page to a Go string.
// dwFlags is 0 rather than MB_ERR_INVALID_CHARS so that a stray invalid byte
// yields a replacement character instead of discarding the whole message.
func decodeCodePage(codePage uint32, b []byte) (string, error) {
	size, err := windows.MultiByteToWideChar(codePage, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil {
		return "", err
	}
	if size <= 0 {
		return "", fmt.Errorf("MultiByteToWideChar(%d) reported size %d", codePage, size)
	}
	buf := make([]uint16, size)
	if _, err := windows.MultiByteToWideChar(codePage, 0, &b[0], int32(len(b)), &buf[0], size); err != nil {
		return "", err
	}
	// utf16.Decode rather than windows.UTF16ToString: the latter stops at the
	// first NUL, and command output is arbitrary bytes.
	return string(utf16.Decode(buf)), nil
}
