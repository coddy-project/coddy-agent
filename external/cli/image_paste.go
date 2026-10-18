//go:build cli

package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/clipimage"
	"github.com/EvilFreelancer/coddy-agent/external/cli/markup/xwindow"
	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// Pictures in the prompt. ctrl+v (alt+v where the terminal keeps ctrl+v for
// its own paste) takes the picture the clipboard holds; on a desktop with an
// X display it first opens in the markup window (external/cli/markup), where
// Done attaches it as marked up and Cancel drops it. An attached picture is an
// [image #N] marker in the editor, deleted as one piece; the prompt sent
// carries the pictures whose markers it still holds, and the marker text, so
// the model can tell which picture a sentence means.

// pastedImage is a picture attached to the prompt being written.
type pastedImage struct {
	dataURL string
	name    string
}

// maxPastedImages bounds the pictures the console remembers: a prompt
// recalled from history attaches its pictures again while they are among the
// last this many.
const maxPastedImages = 32

var imageMarkerRE = regexp.MustCompile(`\[image #(\d+)\]`)

// imageIO is how the console reads the clipboard and marks a picture up,
// replaced by tests.
type imageIO struct {
	read func(ctx context.Context) (clipimage.Image, error)
	// edit is nil without a display to open the markup window on.
	edit func(ctx context.Context, img image.Image) (png []byte, ok bool, err error)
}

// systemImageIO reads the desktop's clipboard and opens the markup window on
// the X display the environment names.
func systemImageIO(getenv func(string) string) imageIO {
	io := imageIO{read: func(ctx context.Context) (clipimage.Image, error) {
		return clipimage.Read(ctx, clipimage.Env{
			GOOS:     runtime.GOOS,
			Getenv:   getenv,
			LookPath: exec.LookPath,
			Run:      clipimage.RunProgram,
			X11:      clipimage.ReadX11,
		})
	}}
	if display := xutil.DisplayName(getenv); display != "" {
		io.edit = func(ctx context.Context, img image.Image) ([]byte, bool, error) {
			return xwindow.Edit(ctx, display, img)
		}
	}
	return io
}

// clipboardReadTimeout bounds reading the clipboard, so a helper program
// stuck on a frozen clipboard owner does not keep the paste busy for the
// rest of the session. The markup window has no bound: it waits for a person.
var clipboardReadTimeout = 15 * time.Second

// modelTakesPictures reports whether the session's model is marked as one
// that reads images. Over --remote the server decides with its own
// catalogue, which the local config does not mirror; in-process a model with
// no models row takes none, as the send boundary decides.
func (a *App) modelTakesPictures() bool {
	if a.remoteURL != "" {
		return true
	}
	cfg := a.config()
	return cfg.ModelMultimodal(cfg.FindModelEntry(a.modelID))
}

func (a *App) refuseForModel() {
	if a.config().FindModelEntry(a.modelID) == nil {
		a.appendStatus(roleWarning, fmt.Sprintf("The model %s is not in models, so it takes no pictures: add it there with multimodal: true, or pick a model that has it", a.modelID))
		return
	}
	a.appendStatus(roleWarning, fmt.Sprintf("The model %s does not take pictures: set multimodal: true on its models row", a.modelID))
}

// pasteImage starts taking a picture from the clipboard (ctrl+v, alt+v).
func (a *App) pasteImage() {
	if a.imageBusy {
		a.appendStatus(roleDim, "The last picture is still being read or marked up")
		return
	}
	if !a.modelTakesPictures() {
		a.refuseForModel()
		return
	}
	io := a.imgIO
	if io.read == nil {
		io = systemImageIO(os.Getenv)
		a.imgIO = io
	}
	a.imageBusy = true
	ctx := a.workCtx
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		readCtx, cancel := context.WithTimeout(ctx, clipboardReadTimeout)
		got, err := io.read(readCtx)
		cancel()
		if err != nil {
			a.postUI(func() {
				a.imageBusy = false
				switch {
				case errors.Is(err, clipimage.ErrNoImage):
					a.appendStatus(roleDim, "No picture in the clipboard")
				case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
					a.appendStatus(roleWarning, "Could not read the clipboard: its owner did not answer")
				case ctx.Err() == nil:
					a.appendStatus(roleWarning, "Could not read the clipboard: "+err.Error())
				}
			})
			return
		}
		if ctx.Err() != nil {
			// The console is closing: no window, nothing to attach.
			a.postUI(func() { a.imageBusy = false })
			return
		}
		if io.edit == nil {
			a.postUI(func() { a.imageBusy = false; a.attachImage(got.Data, got.MIME, got.Name) })
			return
		}
		a.postUI(func() {
			a.appendStatus(roleDim, "Marking up the picture in its window: Done attaches it, Cancel drops it")
		})
		png, ok, err := io.edit(ctx, got.Picture)
		a.postUI(func() {
			a.imageBusy = false
			switch {
			case ctx.Err() != nil:
			case err != nil:
				a.appendStatus(roleWarning, "The markup window did not open ("+err.Error()+"): the picture is attached as copied")
				a.attachImage(got.Data, got.MIME, got.Name)
			case !ok:
				a.appendStatus(roleDim, "The picture was not attached")
			default:
				a.attachImage(png, "image/png", pngName(got.Name))
			}
		})
	}()
}

func pngName(name string) string {
	if name == "" {
		return ""
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	return name + ".png"
}

// attachImage keeps a picture and puts its marker into the editor, unless
// the model was switched to one that takes no pictures while it was on its
// way.
func (a *App) attachImage(data []byte, mime, name string) {
	if !a.modelTakesPictures() {
		a.refuseForModel()
		return
	}
	if a.pasted == nil {
		a.pasted = map[int]pastedImage{}
	}
	a.pastedSeq++
	id := a.pastedSeq
	if name == "" {
		name = "pasted-" + strconv.Itoa(id) + extForMIME(mime)
	}
	a.pasted[id] = pastedImage{dataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), name: name}
	if len(a.pasted) > maxPastedImages {
		ids := make([]int, 0, len(a.pasted))
		for k := range a.pasted {
			ids = append(ids, k)
		}
		sort.Ints(ids)
		for _, k := range ids[:len(ids)-maxPastedImages] {
			delete(a.pasted, k)
		}
	}
	a.editor.InsertAtom(fmt.Sprintf("[image #%d]", id))
	a.screen.RequestRender()
}

func extForMIME(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".png"
}

// imagePartsFor is the pictures a prompt's markers name, in the order they
// first appear. A marker of a picture the console no longer holds stays plain
// text; one it still holds attaches that picture, whether the marker was
// pasted, recalled from history or typed.
func (a *App) imagePartsFor(text string) []acp.ImagePartRef {
	var parts []acp.ImagePartRef
	seen := map[int]bool{}
	for _, m := range imageMarkerRE.FindAllStringSubmatch(text, -1) {
		id, err := strconv.Atoi(m[1])
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		if img, ok := a.pasted[id]; ok {
			parts = append(parts, acp.ImagePartRef{DataURL: img.dataURL, Name: img.name})
		}
	}
	return parts
}
