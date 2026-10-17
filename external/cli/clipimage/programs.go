//go:build cli

package clipimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// maxProgramOutput bounds what a clipboard program may print: the picture as
// hex (osascript) is twice its size, as base64 (PowerShell) a third more.
const maxProgramOutput = 2*MaxBytes + 64<<10

// errOutputTooLarge ends a program that printed more than a picture can be.
var errOutputTooLarge error = &BadImageError{Err: errors.New("the clipboard image is too large")}

// cappedBuffer keeps at most max bytes and fails the write past them, which
// ends the copy from the program's pipe.
type cappedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.over = true
		return 0, errOutputTooLarge
	}
	return b.Buffer.Write(p)
}

// RunProgram runs a program and returns its standard output; a non-zero exit
// is an error carrying the exit status. ctx bounds it, and the pipes are
// given up a second after the program is killed, so a helper that leaves a
// child holding them does not hang the read.
func RunProgram(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out := &cappedBuffer{max: maxProgramOutput}
	cmd.Stdout = out
	cmd.WaitDelay = time.Second
	platform.AdaptCommand(cmd)
	if err := cmd.Run(); err != nil {
		if out.over || errors.Is(err, errOutputTooLarge) {
			return nil, errOutputTooLarge
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

// exitCode is the exit status a failed run reports (*exec.ExitError has
// one), or -1.
func exitCode(err error) int {
	var ee interface{ ExitCode() int }
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// readWayland asks wl-paste which types the clipboard offers and takes the
// best picture, or the first picture file of a copied file list.
func readWayland(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error)) (Image, error) {
	types, err := run(ctx, "wl-paste", "--list-types")
	if err != nil {
		// wl-paste exits non-zero on an empty clipboard.
		return Image{}, ErrNoImage
	}
	t := pickType(strings.Split(string(types), "\n"))
	if t == "" {
		return Image{}, ErrNoImage
	}
	data, err := run(ctx, "wl-paste", "--no-newline", "--type", t)
	if err != nil {
		return Image{}, err // errOutputTooLarge says the picture is too large
	}
	if t == "text/uri-list" {
		return fromURIList(string(data))
	}
	return fromBytes(data, "")
}

// The AppleScript coercions of the macOS reader: a PNG, else a TIFF (what
// some apps put on the pasteboard), else the path of a copied file.
const (
	macPNG  = "the clipboard as «class PNGf»"
	macTIFF = "the clipboard as «class TIFF»"
	macFile = "POSIX path of (the clipboard as «class furl»)"
)

func readMac(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error)) (Image, error) {
	for _, script := range []string{macPNG, macTIFF} {
		out, err := run(ctx, "osascript", "-e", script)
		if errors.Is(err, errOutputTooLarge) {
			return Image{}, err
		}
		if err != nil {
			continue
		}
		if data, ok := parseAppleData(string(out)); ok {
			return fromBytes(data, "")
		}
	}
	out, err := run(ctx, "osascript", "-e", macFile)
	if err != nil {
		return Image{}, ErrNoImage
	}
	return fromFile(strings.TrimSpace(string(out)))
}

// parseAppleData reads the «data XXXX0123ABCD…» form osascript prints for
// binary clipboard contents.
func parseAppleData(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	const open = "«data "
	if !strings.HasPrefix(s, open) || !strings.HasSuffix(s, "»") {
		return nil, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(s, open), "»")
	if len(body) < 4 {
		return nil, false
	}
	data, err := hex.DecodeString(body[4:]) // past the four-letter type code
	if err != nil {
		return nil, false
	}
	return data, true
}

// windowsScript prints the clipboard picture as base64 PNG, or "FILE:" and
// the first copied file, or exits 3 when there is neither.
const windowsScript = `Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -eq $null) {
  $files = [System.Windows.Forms.Clipboard]::GetFileDropList()
  if ($files.Count -gt 0) { [Console]::Out.Write('FILE:' + $files[0]); exit 0 }
  exit 3
}
$ms = New-Object System.IO.MemoryStream
$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
[Console]::Out.Write([Convert]::ToBase64String($ms.ToArray()))`

func readWindows(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error)) (Image, error) {
	out, err := run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsScript)
	if err != nil {
		if exitCode(err) == 3 {
			return Image{}, ErrNoImage
		}
		return Image{}, err
	}
	return parseWindows(string(out))
}

func parseWindows(out string) (Image, error) {
	out = strings.TrimSpace(out)
	if path, ok := strings.CutPrefix(out, "FILE:"); ok {
		return fromFile(path)
	}
	data, err := base64.StdEncoding.DecodeString(out)
	if err != nil || len(data) == 0 {
		return Image{}, ErrNoImage
	}
	return fromBytes(data, "")
}
