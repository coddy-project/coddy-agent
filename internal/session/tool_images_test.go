package session

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func pngBytes(t *testing.T, w, h int, fill color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSaveToolImageAssetKeepsThePictureUnderAContentName(t *testing.T) {
	dir := t.TempDir()
	red := pngBytes(t, 400, 300, color.NRGBA{R: 255, A: 255})

	asset, thumb, err := SaveToolImageAsset(dir, "shot.png", "image/png", red)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(asset) != AssetsPath(dir) {
		t.Fatalf("asset %s is not in the session's assets directory", asset)
	}
	name := filepath.Base(asset)
	if !strings.HasPrefix(name, "shot-") || !strings.HasSuffix(name, ".png") {
		t.Errorf("asset name %q, want shot-<digest>.png", name)
	}
	if got, _ := os.ReadFile(asset); !bytes.Equal(got, red) {
		t.Error("the asset does not hold the picture")
	}
	if info, err := os.Stat(asset); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Errorf("asset mode = %v, want read-only", info.Mode())
	}
	if thumb != AssetThumbnailPath(dir, name) {
		t.Errorf("thumbnail = %q, want %q", thumb, AssetThumbnailPath(dir, name))
	}
	cfg, _, err := image.DecodeConfig(bytesReader(t, thumb))
	if err != nil || cfg.Width > assetThumbnailMaxEdge || cfg.Height > assetThumbnailMaxEdge {
		t.Errorf("thumbnail %dx%d (%v), want bounded to %d", cfg.Width, cfg.Height, err, assetThumbnailMaxEdge)
	}

	again, _, err := SaveToolImageAsset(dir, "shot.png", "image/png", red)
	if err != nil || again != asset {
		t.Errorf("the same picture read again is stored as %q (%v), want the first copy %q", again, err, asset)
	}
	green := pngBytes(t, 4, 3, color.NRGBA{G: 255, A: 255})
	other, _, err := SaveToolImageAsset(dir, "shot.png", "image/png", green)
	if err != nil || other == asset {
		t.Errorf("a changed picture under the same name is stored as %q (%v), want a copy of its own", other, err)
	}
	if got, _ := os.ReadFile(asset); !bytes.Equal(got, red) {
		t.Error("the earlier copy changed when the file was read again")
	}
}

func bytesReader(t *testing.T, path string) *bytes.Reader {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}

func TestSaveToolImageAssetNamesTheCopyByItsType(t *testing.T) {
	dir := t.TempDir()
	// A GIF reaches the model as the PNG of its first frame.
	asset, _, err := SaveToolImageAsset(dir, "anim.gif", "image/png", pngBytes(t, 2, 2, color.Black))
	if err != nil {
		t.Fatal(err)
	}
	if name := filepath.Base(asset); !strings.HasPrefix(name, "anim-") || filepath.Ext(name) != ".png" {
		t.Errorf("asset name %q, want anim-<digest>.png", name)
	}
	// A name that tries to leave the directory stays a base name.
	asset, _, err = SaveToolImageAsset(dir, "../../etc/x.png", "image/png", pngBytes(t, 2, 2, color.White))
	if err != nil || filepath.Dir(asset) != AssetsPath(dir) {
		t.Errorf("asset %q (%v) left the assets directory", asset, err)
	}
}

func TestSaveToolImageAssetWithoutASessionDirectorySavesNothing(t *testing.T) {
	asset, thumb, err := SaveToolImageAsset("", "shot.png", "image/png", []byte("x"))
	if asset != "" || thumb != "" || err != nil {
		t.Errorf("got %q %q %v, want nothing saved", asset, thumb, err)
	}
}

func TestAssetRoutesEscapeTheNames(t *testing.T) {
	if got := AssetRoute("sess_1", "a b#.png"); got != "/coddy/sessions/sess_1/assets/a%20b%23.png" {
		t.Errorf("AssetRoute = %q", got)
	}
	if got := AssetThumbnailRoute("sess 1", "a.png"); got != "/coddy/sessions/sess%201/assets/a.png/thumbnail" {
		t.Errorf("AssetThumbnailRoute = %q", got)
	}
}

func TestToolImagesFromMetaReadsTheValueAndItsJSON(t *testing.T) {
	images := []ToolImage{{Name: "shot.png", MIMEType: "image/png", Asset: "shot-1.png", URL: "/u", PreviewURL: "/p"}}
	meta := ToolImagesMeta(nil, images)

	if got := ToolImagesFromMeta(meta); len(got) != 1 || got[0] != images[0] {
		t.Errorf("in process: %v, want %v", got, images)
	}

	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"images":[{"name":"shot.png","mime_type":"image/png","asset":"shot-1.png","url":"/u","preview_url":"/p"}]`) {
		t.Errorf("wire form %s", raw)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := ToolImagesFromMeta(decoded); len(got) != 1 || got[0] != images[0] {
		t.Errorf("after JSON: %v, want %v", got, images)
	}

	if got := ToolImagesFromMeta(nil); got != nil {
		t.Errorf("no meta: %v", got)
	}
	if got := ToolImagesFromMeta(map[string]interface{}{"coddy": map[string]interface{}{"todoPlan": 1}}); got != nil {
		t.Errorf("no images: %v", got)
	}
}

func TestToolImagesMetaKeepsWhatTheMetaAlreadyHolds(t *testing.T) {
	meta := map[string]interface{}{"coddy": map[string]interface{}{"todoPlan": "kept"}}
	meta = ToolImagesMeta(meta, []ToolImage{{Name: "a.png"}})
	coddy := meta["coddy"].(map[string]interface{})
	if coddy["todoPlan"] != "kept" || coddy["images"] == nil {
		t.Errorf("meta = %v", meta)
	}
	if got := ToolImagesMeta(nil, nil); got != nil {
		t.Errorf("no images still made meta %v", got)
	}
}

// A file name may be as long as the file system allows; the copy adds a
// digest and must still be one name the file system takes, and valid UTF-8.
func TestSaveToolImageAssetKeepsALongNameWithinOneFileName(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("скриншот-", 25) + ".png" // 250 runes, over 400 bytes
	asset, _, err := SaveToolImageAsset(dir, long, "image/png", pngBytes(t, 2, 2, color.Black))
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(asset)
	if len(name) > 120 || !utf8.ValidString(name) || !strings.HasPrefix(name, "скриншот-") || filepath.Ext(name) != ".png" {
		t.Errorf("asset name %q (%d bytes), want at most 120 valid bytes keeping the start of the name", name, len(name))
	}
}

// pngHeaderOnly is a PNG signature and an IHDR chunk stating w x h: enough for
// image.DecodeConfig, nothing to decode.
func pngHeaderOnly(w, h int) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	buf.WriteString("IHDR")
	buf.Write(ihdr)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	return buf.Bytes()
}

// A thumbnail decodes the whole picture, and a read may bring many large
// ones: past toolImageThumbnailMaxPixels the copy gets no thumbnail (the web
// UI previews the original) rather than a decode of hundreds of megabytes.
func TestSaveToolImageAssetMakesNoThumbnailOfAHugePicture(t *testing.T) {
	dir := t.TempDir()
	asset, thumb, err := SaveToolImageAsset(dir, "big.png", "image/png", pngHeaderOnly(5000, 4000))
	if err != nil || asset == "" {
		t.Fatalf("asset %q, %v", asset, err)
	}
	if thumb != "" {
		t.Errorf("a 20-megapixel picture got a thumbnail %q", thumb)
	}
	if toolImageThumbnailMaxPixels >= assetThumbnailMaxPixels {
		t.Errorf("the tool picture cap %d is not below the attachment cap %d", toolImageThumbnailMaxPixels, assetThumbnailMaxPixels)
	}
}

// A name is a file name on the host, but the asset name is also a segment of
// the asset address, which refuses a backslash on every host.
func TestSaveToolImageAssetNamesTheCopyForItsAddress(t *testing.T) {
	dir := t.TempDir()
	asset, _, err := SaveToolImageAsset(dir, `a\b.png`, "image/png", pngBytes(t, 2, 2, color.Black))
	if err != nil {
		t.Fatal(err)
	}
	if name := filepath.Base(asset); strings.ContainsAny(name, `\/`) {
		t.Errorf("asset name %q keeps a separator", name)
	}
}

// The copy is named by its content, so a file already under that name that
// holds other bytes - stale, damaged, planted - is replaced, never shown.
func TestSaveToolImageAssetReplacesAFileThatIsNotThePicture(t *testing.T) {
	dir := t.TempDir()
	picture := pngBytes(t, 3, 2, color.White)
	asset, _, err := SaveToolImageAsset(dir, "shot.png", "image/png", picture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(asset, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte("not the picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, _, err := SaveToolImageAsset(dir, "shot.png", "image/png", picture)
	if err != nil || again != asset {
		t.Fatalf("saved again as %q (%v)", again, err)
	}
	if got, _ := os.ReadFile(asset); !bytes.Equal(got, picture) {
		t.Error("the file under the picture's name still holds other bytes")
	}
}

// A thumbnail that cannot be written costs the preview, not the copy: the
// original is on disk and its address is still given.
func TestSaveToolImageAssetKeepsTheCopyWhenTheThumbnailFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(AssetsPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file where the thumbnails directory should be.
	if err := os.WriteFile(AssetThumbnailsPath(dir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	asset, thumb, err := SaveToolImageAsset(dir, "shot.png", "image/png", pngBytes(t, 3, 2, color.White))
	if asset == "" || thumb != "" || err == nil {
		t.Fatalf("got asset %q thumb %q err %v, want the asset kept and the thumbnail error reported", asset, thumb, err)
	}
	if _, statErr := os.Stat(asset); statErr != nil {
		t.Fatalf("the asset is not on disk: %v", statErr)
	}
}

// A copy is read back only as it was saved: under a bare name that carries
// its digest, as a regular file, within the size asked for, and holding the
// bytes the digest names.
func TestReadToolImageAssetReadsOnlyTheCopyThatWasSaved(t *testing.T) {
	dir := t.TempDir()
	picture := pngBytes(t, 3, 2, color.White)
	asset, _, err := SaveToolImageAsset(dir, "shot.png", "image/png", picture)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(asset)
	if got, err := ReadToolImageAsset(dir, name, 1<<20); err != nil || !bytes.Equal(got, picture) {
		t.Fatalf("the saved copy reads as %d bytes (%v)", len(got), err)
	}
	if _, err := ReadToolImageAsset(dir, name, int64(len(picture)-1)); err == nil {
		t.Error("a copy larger than asked for was read")
	}
	for _, bad := range []string{"", "..", "../" + name, `..\` + name, "shot.png"} {
		if _, err := ReadToolImageAsset(dir, bad, 1<<20); err == nil {
			t.Errorf("the name %q was read", bad)
		}
	}
	if _, err := ReadToolImageAsset("", name, 1<<20); err == nil {
		t.Error("a copy was read with no session directory")
	}

	if err := os.Chmod(asset, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, pngBytes(t, 3, 2, color.Black), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToolImageAsset(dir, name, 1<<20); err == nil {
		t.Error("a copy whose bytes changed was read")
	}

	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "picture.png")
	if err := os.WriteFile(elsewhere, picture, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, asset); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadToolImageAsset(dir, name, 1<<20); err == nil {
		t.Error("a link under the copy's name was followed")
	}
}

// A file a tool returned is kept under the name it came with, made safe for
// every file system and the asset address, plus the digest of its bytes.
func TestSaveToolFileAssetNamesTheCopySafely(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveToolFileAsset(dir, "q3:report\n?.pdf", []byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	if filepath.Dir(path) != AssetsPath(dir) || !strings.HasPrefix(name, "q3_report__-") || !strings.HasSuffix(name, ".pdf") {
		t.Fatalf("the copy is %s", path)
	}
	if again, err := SaveToolFileAsset(dir, "q3:report\n?.pdf", []byte("%PDF-1.4")); err != nil || again != path {
		t.Fatalf("the same file saved again: %s, %v", again, err)
	}
	if _, err := SaveToolFileAsset("", "x.pdf", []byte("x")); err == nil {
		t.Fatal("a session that keeps no files took one")
	}
}
