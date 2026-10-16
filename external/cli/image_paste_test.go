//go:build cli

package cli

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/clipimage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// settle runs what the paste worker posts to the UI loop until the paste is
// over.
func settle(t *testing.T, a *App) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for a.imageBusy {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		case <-deadline:
			t.Fatal("the paste never finished")
		}
	}
}

func multimodalApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	cfg := a.config()
	cfg.Models = []config.ModelEntry{{Model: "stub/eyes", Multimodal: true}}
	a.modelID = "stub/eyes"
	return a
}

func copiedPicture() (clipimage.Image, error) {
	return clipimage.Image{Data: []byte("png-bytes"), MIME: "image/png", Picture: image.NewRGBA(image.Rect(0, 0, 4, 3))}, nil
}

func TestPasteRefusedForAModelWithoutPictures(t *testing.T) {
	a := newTestApp(t)
	a.config().Models = []config.ModelEntry{{Model: "stub/blind"}}
	a.modelID = "stub/blind"
	read := false
	a.imgIO = imageIO{read: func(context.Context) (clipimage.Image, error) { read = true; return copiedPicture() }}
	a.pasteImage()
	if read || a.imageBusy {
		t.Fatal("the clipboard was read for a model that takes no pictures")
	}
	if got := transcriptText(a); !strings.Contains(got, "does not take pictures") {
		t.Fatalf("no refusal in the transcript:\n%s", got)
	}
}

func TestPasteWithAnEmptyClipboardSaysSo(t *testing.T) {
	a := multimodalApp(t)
	a.imgIO = imageIO{read: func(context.Context) (clipimage.Image, error) { return clipimage.Image{}, clipimage.ErrNoImage }}
	a.pasteImage()
	settle(t, a)
	if !strings.Contains(transcriptText(a), "No picture in the clipboard") || !a.editor.IsEmpty() {
		t.Fatalf("an empty clipboard: editor %q, transcript:\n%s", a.editor.Text(), transcriptText(a))
	}
}

func TestPasteWhileTheLastOneIsOpenWaits(t *testing.T) {
	a := multimodalApp(t)
	release := make(chan struct{})
	a.imgIO = imageIO{
		read: func(context.Context) (clipimage.Image, error) { return copiedPicture() },
		edit: func(context.Context, image.Image) ([]byte, bool, error) {
			<-release
			return []byte("marked"), true, nil
		},
	}
	a.pasteImage()
	a.pasteImage()
	if !strings.Contains(transcriptText(a), "still being read or marked up") {
		t.Fatal("a second paste during the first did not say it waits")
	}
	close(release)
	settle(t, a)
	if got := strings.Count(a.editor.Text(), "[image #"); got != 1 {
		t.Fatalf("editor %q holds %d pictures, want 1", a.editor.Text(), got)
	}
}

func TestAWindowThatDoesNotOpenAttachesThePictureAsCopied(t *testing.T) {
	a := multimodalApp(t)
	a.imgIO = imageIO{
		read: func(context.Context) (clipimage.Image, error) { return copiedPicture() },
		edit: func(context.Context, image.Image) ([]byte, bool, error) {
			return nil, false, errors.New("connection refused")
		},
	}
	a.pasteImage()
	settle(t, a)
	parts := a.imagePartsFor(a.editor.Text())
	if len(parts) != 1 || !strings.HasSuffix(parts[0].DataURL, "cG5nLWJ5dGVz") {
		t.Fatalf("parts %+v, want the copied bytes", parts)
	}
	if !strings.Contains(transcriptText(a), "did not open (connection refused)") {
		t.Fatalf("the failure was not reported:\n%s", transcriptText(a))
	}
}

func TestImagePartsFollowTheMarkersOfThePrompt(t *testing.T) {
	a := multimodalApp(t)
	a.attachImage([]byte("one"), "image/png", "")
	a.attachImage([]byte("two"), "image/jpeg", "shot.jpg")
	a.attachImage([]byte("three"), "image/png", "")
	parts := a.imagePartsFor("see [image #3] and [image #1], again [image #3], and [image #9]")
	if len(parts) != 2 {
		t.Fatalf("parts %+v, want #3 and #1 once each", parts)
	}
	if parts[0].Name != "pasted-3.png" || parts[1].Name != "pasted-1.png" {
		t.Fatalf("names %q %q", parts[0].Name, parts[1].Name)
	}
	if !strings.HasPrefix(a.imagePartsFor("[image #2]")[0].DataURL, "data:image/jpeg;base64,") {
		t.Fatal("a JPEG lost its type")
	}
	if a.imagePartsFor("no markers") != nil {
		t.Fatal("a prompt without markers carried pictures")
	}
}

func TestOnlyTheLatestPicturesAreKept(t *testing.T) {
	a := multimodalApp(t)
	for i := 0; i < maxPastedImages+3; i++ {
		a.attachImage([]byte{byte(i)}, "image/png", "")
	}
	if len(a.pasted) != maxPastedImages {
		t.Fatalf("kept %d pictures", len(a.pasted))
	}
	if len(a.imagePartsFor("[image #1]")) != 0 || len(a.imagePartsFor("[image #35]")) != 1 {
		t.Fatal("the oldest pictures were not the ones dropped")
	}
}

// In-process a model with no models row takes no pictures (the send boundary
// would drop them), so the paste says so at once.
func TestPasteRefusedForAModelTheConfigDoesNotList(t *testing.T) {
	a := newTestApp(t)
	a.modelID = "stub/unlisted"
	read := false
	a.imgIO = imageIO{read: func(context.Context) (clipimage.Image, error) { read = true; return copiedPicture() }}
	a.pasteImage()
	if read || !strings.Contains(transcriptText(a), "does not take pictures") {
		t.Fatalf("an unlisted model: read %v, transcript:\n%s", read, transcriptText(a))
	}
	// Over --remote the server's catalogue decides.
	a.remoteURL = "http://127.0.0.1:1"
	if !a.modelTakesPictures() {
		t.Fatal("a remote console refused a model only the server knows")
	}
}

func TestAClipboardThatDoesNotAnswerFreesThePaste(t *testing.T) {
	old := clipboardReadTimeout
	clipboardReadTimeout = 50 * time.Millisecond
	t.Cleanup(func() { clipboardReadTimeout = old })
	a := multimodalApp(t)
	a.imgIO = imageIO{read: func(ctx context.Context) (clipimage.Image, error) {
		<-ctx.Done()
		return clipimage.Image{}, ctx.Err()
	}}
	a.pasteImage()
	settle(t, a)
	if !strings.Contains(transcriptText(a), "its owner did not answer") {
		t.Fatalf("a stuck clipboard was not reported:\n%s", transcriptText(a))
	}
	if a.imageBusy {
		t.Fatal("a stuck clipboard left the paste busy")
	}
}

func TestAPictureIsNotAttachedForAModelSwitchedMeanwhile(t *testing.T) {
	a := multimodalApp(t)
	release := make(chan struct{})
	a.imgIO = imageIO{
		read: func(context.Context) (clipimage.Image, error) { return copiedPicture() },
		edit: func(context.Context, image.Image) ([]byte, bool, error) {
			<-release
			return []byte("marked"), true, nil
		},
	}
	a.pasteImage()
	a.config().Models = append(a.config().Models, config.ModelEntry{Model: "stub/blind"})
	a.modelID = "stub/blind"
	close(release)
	settle(t, a)
	if strings.Contains(a.editor.Text(), "[image #") {
		t.Fatalf("a picture reached the prompt of a model that takes none: %q", a.editor.Text())
	}
}
