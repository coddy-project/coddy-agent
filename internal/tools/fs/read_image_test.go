package fs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type attachedImage struct {
	name, mimeType string
	data           []byte
}

// imageEnv is a tool environment whose agent takes every picture it is handed.
func imageEnv(t *testing.T) (*tooling.Env, *[]attachedImage) {
	t.Helper()
	var got []attachedImage
	env := &tooling.Env{CWD: t.TempDir()}
	env.AttachImage = func(name, mimeType string, data []byte) error {
		got = append(got, attachedImage{name, mimeType, data})
		return nil
	}
	return env, &got
}

func solidImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	return img
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, solidImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, env *tooling.Env, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(env.CWD, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRead(env *tooling.Env, args string) (string, error) {
	return executeRead(context.Background(), args, env)
}

func TestReadHandsTheModelAPictureTellingTheTypeByContent(t *testing.T) {
	env, got := imageEnv(t)
	pngData := encodePNG(t, 40, 30)
	writeFile(t, env, "shot.png", pngData)
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, solidImage(8, 6), nil); err != nil {
		t.Fatal(err)
	}
	// A JPEG saved under a .png name, and one with no extension at all.
	writeFile(t, env, "photo.png", jpg.Bytes())
	writeFile(t, env, "noext", pngData)

	out, err := runRead(env, `{"path":"shot.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shot.png", "PNG image", "40x30"} {
		if !strings.Contains(out, want) {
			t.Errorf("result %q does not say %q", out, want)
		}
	}
	if _, err := runRead(env, `{"path":"photo.png"}`); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": filepath.Join(env.CWD, "noext"), "offset": 3, "limit": 1})
	if _, err := runRead(env, string(args)); err != nil {
		t.Fatalf("an image read with a line range: %v", err)
	}

	if len(*got) != 3 {
		t.Fatalf("attached %d pictures, want 3", len(*got))
	}
	want := []attachedImage{{"shot.png", "image/png", pngData}, {"photo.png", "image/jpeg", jpg.Bytes()}, {"noext", "image/png", pngData}}
	for i, w := range want {
		g := (*got)[i]
		if g.name != w.name || g.mimeType != w.mimeType || !bytes.Equal(g.data, w.data) {
			t.Errorf("picture %d = %s %s (%d bytes), want %s %s (%d bytes)", i, g.name, g.mimeType, len(g.data), w.name, w.mimeType, len(w.data))
		}
	}
}

func TestReadOfTextNamedLikeAnImageStaysText(t *testing.T) {
	env, got := imageEnv(t)
	writeFile(t, env, "fake.png", []byte("just text\n"))
	out, err := runRead(env, `{"path":"fake.png"}`)
	if err != nil || out != "just text\n" {
		t.Fatalf("read = %q, %v", out, err)
	}
	if len(*got) != 0 {
		t.Error("text was attached as a picture")
	}
}

func TestReadOfAnImageWithoutAnAgentIsRefusedAsBinary(t *testing.T) {
	env := &tooling.Env{CWD: t.TempDir()}
	writeFile(t, env, "shot.png", encodePNG(t, 2, 2))
	_, err := runRead(env, `{"path":"shot.png"}`)
	if err == nil || !strings.Contains(err.Error(), "binary") || !strings.Contains(err.Error(), "image/png") {
		t.Fatalf("err = %v, want the binary refusal naming image/png", err)
	}
}

func TestReadOfAnImageCarriesTheAgentsRefusal(t *testing.T) {
	env := &tooling.Env{CWD: t.TempDir()}
	env.AttachImage = func(string, string, []byte) error {
		return errors.New("the session's model m does not read images")
	}
	writeFile(t, env, "shot.png", encodePNG(t, 2, 2))
	_, err := runRead(env, `{"path":"shot.png"}`)
	if err == nil || !strings.Contains(err.Error(), "does not read images") || !strings.Contains(err.Error(), "shot.png") {
		t.Fatalf("err = %v, want the agent's refusal naming the file", err)
	}
}

func TestReadRefusesAPictureNoProviderWouldTake(t *testing.T) {
	env, got := imageEnv(t)

	huge := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, readImageMaxBytes)...)
	writeFile(t, env, "huge.png", huge)
	if _, err := runRead(env, `{"path":"huge.png"}`); err == nil || !strings.Contains(err.Error(), "MB") {
		t.Errorf("an oversized picture: err = %v, want the size limit named", err)
	}

	writeFile(t, env, "wide.png", encodePNG(t, readImageMaxSide+1, 1))
	if _, err := runRead(env, `{"path":"wide.png"}`); err == nil || !strings.Contains(err.Error(), "8001x1") {
		t.Errorf("a picture over the side limit: err = %v, want its size named", err)
	}

	writeFile(t, env, "broken.png", []byte("\x89PNG\r\n\x1a\nnot really"))
	if _, err := runRead(env, `{"path":"broken.png"}`); err == nil || !strings.Contains(err.Error(), "cannot be decoded") {
		t.Errorf("a broken PNG: err = %v, want it refused as undecodable", err)
	}

	if len(*got) != 0 {
		t.Errorf("a refused picture was attached: %d", len(*got))
	}
}

func TestReadShowsTheFirstFrameOfAnAnimatedGIF(t *testing.T) {
	env, got := imageEnv(t)
	frame := func(c uint8) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 6, 4), palette.Plan9)
		for i := range p.Pix {
			p.Pix[i] = c
		}
		return p
	}
	var anim bytes.Buffer
	if err := gif.EncodeAll(&anim, &gif.GIF{Image: []*image.Paletted{frame(10), frame(200)}, Delay: []int{5, 5}}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env, "anim.gif", anim.Bytes())
	var still bytes.Buffer
	if err := gif.Encode(&still, frame(10), nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env, "still.gif", still.Bytes())

	out, err := runRead(env, `{"path":"anim.gif"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "first frame") || !strings.Contains(out, "6x4") {
		t.Errorf("result %q does not say the model sees the first frame", out)
	}
	if _, err := runRead(env, `{"path":"still.gif"}`); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("attached %d pictures, want 2", len(*got))
	}
	for i, g := range *got {
		if g.mimeType != "image/png" {
			t.Fatalf("GIF %d went as %s, want the PNG of its first frame", i, g.mimeType)
		}
		img, err := png.Decode(bytes.NewReader(g.data))
		if err != nil || img.Bounds().Dx() != 6 || img.Bounds().Dy() != 4 {
			t.Fatalf("GIF %d decodes to %v (%v), want a 6x4 frame", i, img, err)
		}
	}
}

// A GIF is read only as far as its first frame: the frames after it are never
// decoded, so a small file of many large frames cannot fill the memory. The
// proof is a GIF whose second frame is cut short, which decoding every frame
// would refuse.
func TestReadDecodesOnlyTheFirstFrameOfAGIF(t *testing.T) {
	env, got := imageEnv(t)
	frame := func(c uint8) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 6, 4), palette.Plan9)
		for i := range p.Pix {
			p.Pix[i] = c
		}
		return p
	}
	var one bytes.Buffer
	if err := gif.EncodeAll(&one, &gif.GIF{Image: []*image.Paletted{frame(10)}, Delay: []int{5}}); err != nil {
		t.Fatal(err)
	}
	// The one-frame file without its trailer, then the image descriptor of a
	// second 6x4 frame and its LZW code size, and the file ends there.
	cut := append([]byte(nil), one.Bytes()[:one.Len()-1]...)
	cut = append(cut, 0x2C, 0, 0, 0, 0, 6, 0, 4, 0, 0x00, 0x08)
	if _, err := gif.DecodeAll(bytes.NewReader(cut)); err == nil {
		t.Fatal("the fixture decodes whole; it must not")
	}
	writeFile(t, env, "cut.gif", cut)
	if _, err := runRead(env, `{"path":"cut.gif"}`); err != nil {
		t.Fatalf("read of a GIF with a broken second frame: %v", err)
	}
	if len(*got) != 1 || (*got)[0].mimeType != "image/png" {
		t.Fatalf("attached %+v, want the first frame as a PNG", *got)
	}
}

// webpChunk is one RIFF chunk: its name, its size and its payload, padded to
// an even length.
func webpChunk(name string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(name)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(payload)))
	buf.Write(payload)
	if len(payload)%2 == 1 {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// webpFile wraps chunks in the RIFF header of a WebP file.
func webpFile(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
	}
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(body)))
	buf.Write(body)
	return buf.Bytes()
}

// vp8lChunk is a lossless image chunk whose header states w x h; the
// bitstream after it is not decoded by read, only its presence is checked.
func vp8lChunk(w, h int) []byte {
	bits := uint32(w-1) | uint32(h-1)<<14
	payload := []byte{0x2f, byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24), 0, 0, 0}
	return webpChunk("VP8L", payload)
}

// vp8xChunk is the extended header chunk stating the canvas w x h, animated
// when asked.
func vp8xChunk(w, h int, animated bool) []byte {
	payload := make([]byte, 10)
	if animated {
		payload[0] = 0x02
	}
	put24 := func(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
	put24(payload[4:7], w-1)
	put24(payload[7:10], h-1)
	return webpChunk("VP8X", payload)
}

func TestReadHandsTheModelAWebPWithItsSize(t *testing.T) {
	env, got := imageEnv(t)
	writeFile(t, env, "pic.webp", webpFile(vp8lChunk(640, 480)))
	writeFile(t, env, "ext.webp", webpFile(vp8xChunk(320, 200, false), vp8lChunk(320, 200)))
	out, err := runRead(env, `{"path":"pic.webp"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WebP image") || !strings.Contains(out, "640x480") {
		t.Errorf("result %q does not name the WebP and its size", out)
	}
	if out, err := runRead(env, `{"path":"ext.webp"}`); err != nil || !strings.Contains(out, "320x200") {
		t.Fatalf("an extended WebP: %q, %v", out, err)
	}
	if len(*got) != 2 || (*got)[0].mimeType != "image/webp" {
		t.Fatalf("attached %+v, want two image/webp", *got)
	}
	writeFile(t, env, "wide.webp", webpFile(vp8lChunk(9000, 10)))
	if _, err := runRead(env, `{"path":"wide.webp"}`); err == nil || !strings.Contains(err.Error(), "9000x10") {
		t.Errorf("a WebP over the side limit: err = %v", err)
	}
}

// A picture that is not all there - a screenshot still being written, a
// download cut short - would go into the history and fail every later
// request, so read refuses a PNG with no IEND, a JPEG with no end of image,
// a WebP whose chunks run past the file or that carries no image data, and
// an animated WebP, which not every provider takes.
func TestReadRefusesAPictureThatIsNotAllThere(t *testing.T) {
	env, got := imageEnv(t)
	pngData := encodePNG(t, 40, 30)
	writeFile(t, env, "cut.png", pngData[:len(pngData)-20])
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, solidImage(64, 48), nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env, "cut.jpg", jpg.Bytes()[:jpg.Len()-40])
	whole := webpFile(vp8lChunk(64, 48))
	writeFile(t, env, "cut.webp", whole[:len(whole)-4])
	writeFile(t, env, "empty.webp", webpFile(vp8xChunk(64, 48, false)))
	writeFile(t, env, "anim.webp", webpFile(vp8xChunk(64, 48, true), webpChunk("ANIM", make([]byte, 6))))

	for name, want := range map[string]string{
		"cut.png":    "ends before its image does",
		"cut.jpg":    "ends before its image does",
		"cut.webp":   "ends before its image does",
		"empty.webp": "no image data",
		"anim.webp":  "animated",
	} {
		_, err := runRead(env, `{"path":"`+name+`"}`)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
	if len(*got) != 0 {
		t.Errorf("attached %d incomplete pictures", len(*got))
	}

	// The whole files pass, trailing bytes after a JPEG's end included.
	writeFile(t, env, "whole.jpg", append(append([]byte(nil), jpg.Bytes()...), "trailer"...))
	writeFile(t, env, "whole.png", pngData)
	for _, name := range []string{"whole.jpg", "whole.png"} {
		if _, err := runRead(env, `{"path":"`+name+`"}`); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadDescriptionTellsTheModelAboutPictures(t *testing.T) {
	desc := ReadTool().Definition.Description
	for _, want := range []string{"PNG", "JPEG", "GIF", "WebP", "picture"} {
		if !strings.Contains(desc, want) {
			t.Errorf("read description does not mention %q", want)
		}
	}
	var names []string
	RegisterBuiltins(func(tool *tooling.Tool) { names = append(names, tool.Definition.Name) })
	for _, n := range names {
		if n == "view_image" {
			t.Error("view_image is still a built-in: read shows pictures")
		}
	}
}

// A picture over the limit is refused by its size on disk, before read loads
// it: the file is sparse here, 64 MB that were never written, and reading it
// whole would allocate all of it.
func TestReadRefusesAnOversizedPictureWithoutLoadingIt(t *testing.T) {
	env, got := imageEnv(t)
	path := filepath.Join(env.CWD, "huge.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(encodePNG(t, 2, 2)); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err = runRead(env, `{"path":"huge.png"}`)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "64.0 MB") {
		t.Fatalf("err = %v, want the size named", err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 16<<20 {
		t.Errorf("refusing the picture allocated %d MB", grown>>20)
	}
	if len(*got) != 0 {
		t.Error("an oversized picture was attached")
	}
}

// A GIF is small on disk for any canvas: its first frame is decoded to be sent,
// so past 16 megapixels it is refused before that decode, not after it.
func TestReadRefusesAGIFWhoseFrameIsTooLargeToDecode(t *testing.T) {
	env, got := imageEnv(t)
	// GIF89a, a logical screen of 5000x4000 and no colour table, then the
	// trailer: all image.DecodeConfig reads.
	gifHeader := []byte("GIF89a")
	gifHeader = append(gifHeader, 0x88, 0x13, 0xA0, 0x0F, 0x00, 0x00, 0x00, 0x3B)
	writeFile(t, env, "wide.gif", gifHeader)
	_, err := runRead(env, `{"path":"wide.gif"}`)
	if err == nil || !strings.Contains(err.Error(), "5000x4000") {
		t.Fatalf("err = %v, want the frame size refused", err)
	}
	if len(*got) != 0 {
		t.Error("the GIF was attached")
	}
}

// A file cut anywhere is refused with an error, never a panic: every prefix of
// a valid picture of each format goes through the checks.
func TestReadSurvivesEveryPrefixOfAPicture(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, solidImage(8, 6), nil); err != nil {
		t.Fatal(err)
	}
	for name, whole := range map[string][]byte{
		"p.png":  encodePNG(t, 6, 4),
		"p.jpg":  jpg.Bytes(),
		"p.webp": webpFile(vp8xChunk(8, 6, false), vp8lChunk(8, 6)),
	} {
		for n := 0; n <= len(whole); n++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s cut to %d bytes panics: %v", name, n, r)
					}
				}()
				kind := sniffKind(whole[:n])
				if readImageFormats[kind] == "" {
					return
				}
				w, h, err := imageSize(whole[:n], kind)
				if err == nil {
					err = imageComplete(whole[:n], kind)
				}
				if n < len(whole) && err == nil {
					t.Errorf("%s cut to %d of %d bytes passes as %dx%d", name, n, len(whole), w, h)
				}
			}()
		}
	}
}

// pngChunk is one PNG chunk: its length, its name, its payload and the
// checksum over the name and the payload.
func pngChunk(name string, payload []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(payload)))
	buf.WriteString(name)
	buf.Write(payload)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(name), payload...)))
	return buf.Bytes()
}

// A picture that is all there but holds no image - a PNG with no IDAT, a JPEG
// with no scan - or a PNG whose chunks do not match their checksums is refused
// like a cut one: the decoders providers run refuse it too (Pillow does), and
// the history would carry it to every later request.
func TestReadRefusesAPictureWithNoImageInIt(t *testing.T) {
	env, got := imageEnv(t)
	ihdr := []byte{0, 0, 0, 4, 0, 0, 0, 3, 8, 2, 0, 0, 0}
	writeFile(t, env, "empty.png", bytes.Join([][]byte{[]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr), pngChunk("IEND", nil)}, nil))
	damaged := encodePNG(t, 40, 30)
	damaged[len(damaged)-20] ^= 0xFF // a byte of the image data, before its checksum
	writeFile(t, env, "damaged.png", damaged)
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, solidImage(64, 48), nil); err != nil {
		t.Fatal(err)
	}
	// A camera's JPEG opens with a JFIF segment, and with one the header
	// decoder stops at the frame size without looking for a scan.
	jfif := []byte{0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00}
	camera := bytes.Join([][]byte{jpg.Bytes()[:2], jfif, jpg.Bytes()[2:]}, nil)
	sos := bytes.Index(camera, []byte{0xFF, 0xDA})
	writeFile(t, env, "empty.jpg", append(append([]byte(nil), camera[:sos]...), 0xFF, 0xD9))
	writeFile(t, env, "camera.jpg", camera)

	for name, want := range map[string]string{
		"empty.png":   "no image data",
		"damaged.png": "checksum",
		"empty.jpg":   "no image data",
	} {
		_, err := runRead(env, `{"path":"`+name+`"}`)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
	if len(*got) != 0 {
		t.Errorf("attached %d pictures with no image in them", len(*got))
	}
	if _, err := runRead(env, `{"path":"camera.jpg"}`); err != nil {
		t.Errorf("the whole JFIF picture: %v", err)
	}
}

// A WebP whose RIFF header states a size other than what follows it is
// refused, a size larger than the file, as some writers put the whole file's
// length there, included: libwebp does not decode such a file either.
func TestReadRefusesAWebPWhoseSizeIsWrong(t *testing.T) {
	env, got := imageEnv(t)
	whole := webpFile(vp8lChunk(64, 48))
	for name, size := range map[string]int{"total.webp": len(whole), "zero.webp": 0} {
		data := append([]byte(nil), whole...)
		binary.LittleEndian.PutUint32(data[4:8], uint32(size))
		writeFile(t, env, name, data)
		if _, err := runRead(env, `{"path":"`+name+`"}`); err == nil {
			t.Errorf("%s with a RIFF size of %d was taken", name, size)
		}
	}
	if len(*got) != 0 {
		t.Errorf("attached %d WebP files of a wrong size", len(*got))
	}
}

// A model that cannot see pictures is told so before anything else: advice to
// make a picture smaller, or to wait for one still being written, would only
// lead it to a second refusal.
func TestReadTellsAModelWithoutImagesSoFirst(t *testing.T) {
	env, got := imageEnv(t)
	env.ImageRefusal = func() error { return errors.New("the session's model m does not read images") }
	path := filepath.Join(env.CWD, "huge.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(encodePNG(t, 2, 2)); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(readImageMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	writeFile(t, env, "cut.png", encodePNG(t, 40, 30)[:60])
	writeFile(t, env, "shot.png", encodePNG(t, 4, 3))
	for _, name := range []string{"huge.png", "cut.png", "shot.png"} {
		_, err := runRead(env, `{"path":"`+name+`"}`)
		if err == nil || !strings.Contains(err.Error(), "does not read images") || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v, want the model's refusal first", name, err)
		}
	}
	if len(*got) != 0 {
		t.Errorf("attached %d pictures for a model that cannot see them", len(*got))
	}
}

// Stray bytes between the segments of a JPEG are skipped to the next marker,
// as libjpeg does with a warning and Go's decoder after it: such a file
// decodes everywhere, so read takes it.
func TestReadTakesAJPEGWithStrayBytesBetweenSegments(t *testing.T) {
	env, got := imageEnv(t)
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, solidImage(64, 48), nil); err != nil {
		t.Fatal(err)
	}
	data := jpg.Bytes()
	first := 4 + int(binary.BigEndian.Uint16(data[4:6])) // SOI, then the first segment
	stray := bytes.Join([][]byte{data[:first], {0x00, 0x13}, data[first:]}, nil)
	if _, _, err := image.DecodeConfig(bytes.NewReader(stray)); err != nil {
		t.Fatalf("the fixture does not decode: %v", err)
	}
	writeFile(t, env, "stray.jpg", stray)
	if _, err := runRead(env, `{"path":"stray.jpg"}`); err != nil {
		t.Fatalf("a JPEG with stray bytes between segments: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("attached %d pictures, want the JPEG", len(*got))
	}
}
