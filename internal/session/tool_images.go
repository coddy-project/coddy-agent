package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ToolImage is a picture a tool call returned (for example, read or MCP),
// as the surfaces find it on the call's final tool_call_update
// (_meta.coddy.images) and on the call's result in the transcript: the file
// name the model was told, the type it was sent as, and the copy kept with the
// session's assets. The web UI previews it from URL and PreviewURL, a
// Telegram chat is sent the Asset file; the console and an editor show only
// the call's text. The keys match the files of a user message
// (GET /coddy/sessions/{id}/messages), so one reader serves both.
type ToolImage struct {
	Name       string `json:"name"`
	MIMEType   string `json:"mime_type,omitempty"`
	Asset      string `json:"asset,omitempty"`
	URL        string `json:"url,omitempty"`
	PreviewURL string `json:"preview_url,omitempty"`
}

// AssetRoute is where coddy serve answers with a session asset
// (GET /coddy/sessions/{id}/assets/{name}).
func AssetRoute(sessionID, assetName string) string {
	return "/coddy/sessions/" + url.PathEscape(sessionID) + "/assets/" + url.PathEscape(assetName)
}

// AssetThumbnailRoute is where coddy serve answers with an asset's bounded
// preview (GET /coddy/sessions/{id}/assets/{name}/thumbnail).
func AssetThumbnailRoute(sessionID, assetName string) string {
	return AssetRoute(sessionID, assetName) + "/thumbnail"
}

// toolImageStemMax bounds the part of a copy's name taken from the file's.
const toolImageStemMax = 80

// toolImageThumbnailMaxPixels bounds the pictures a tool result gets a thumbnail
// for. Making one decodes the whole picture, and a session may read many
// large ones, so past 16 megapixels (64 MB decoded) the copy gets none and
// the web UI previews the original.
const toolImageThumbnailMaxPixels = 16_000_000

// toolImageExt names a copy by the type the model was sent, which is not
// always the file's own: a GIF goes as the PNG of its first frame.
var toolImageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// ToolImageExt is the extension a picture of mimeType is named with, or ""
// for a type a model is never sent.
func ToolImageExt(mimeType string) string { return toolImageExt[mimeType] }

// SaveToolImageAsset keeps a copy of a picture a tool call handed the model
// with the session's assets, read-only, under the file's name plus a digest
// of its content. The digest is what makes the copy the picture the model was
// shown: the workspace file may change after the call, and a later preview of
// that call must not follow it. A picture read again unchanged is stored once.
// It returns the copy's path and its thumbnail's, or no thumbnail when none
// could be made (a WebP, which the standard library cannot decode); with no
// session directory nothing is saved.
func SaveToolImageAsset(sessionDir, name, mimeType string, data []byte) (assetPath, thumbPath string, err error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", "", nil
	}
	base := filepath.Base(filepath.Clean(strings.TrimSpace(name)))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "image"
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	// The asset name is also a segment of the asset address, which refuses a
	// backslash on every host, while a Unix file name may hold one.
	stem = strings.ReplaceAll(stem, "\\", "_")
	if stem == "" {
		stem = "image"
	}
	// The digest and the extension come on top of the name, and a file name
	// is 255 bytes on most file systems.
	for len(stem) > toolImageStemMax {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	ext := toolImageExt[mimeType]
	if ext == "" {
		ext = filepath.Ext(base)
	}
	assetName := stem + "-" + toolImageDigest(data) + ext

	assetsDir := AssetsPath(sessionDir)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return "", "", fmt.Errorf("assets dir: %w", err)
	}
	assetPath = filepath.Join(assetsDir, assetName)
	// The name promises these bytes; a file under it holding others - stale,
	// damaged, planted - is replaced rather than shown.
	wrote := false
	if !holds(assetPath, data) {
		if err := writeReadOnly(assetPath, data); err != nil {
			return "", "", fmt.Errorf("write asset %s: %w", assetName, err)
		}
		wrote = true
	}
	thumbPath = AssetThumbnailPath(sessionDir, assetName)
	if info, statErr := os.Lstat(thumbPath); !wrote && statErr == nil && info.Mode().IsRegular() {
		return assetPath, thumbPath, nil
	}
	// From here on a failure costs the preview, never the copy.
	thumb, ok := makeImageThumbnailWithin(data, toolImageThumbnailMaxPixels)
	if !ok {
		return assetPath, "", nil
	}
	if err := os.MkdirAll(AssetThumbnailsPath(sessionDir), 0o755); err != nil {
		return assetPath, "", fmt.Errorf("thumbnail dir: %w", err)
	}
	if err := writeReadOnly(thumbPath, thumb); err != nil {
		return assetPath, "", fmt.Errorf("write thumbnail %s: %w", assetName, err)
	}
	return assetPath, thumbPath, nil
}

// toolImageDigest is the digest a copy's name carries: the first 8 bytes of
// the SHA-256 of its content, in hex.
func toolImageDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// toolImageDigestOf reads the digest out of a copy's name, the 16 hex digits
// between the last '-' and the extension; "" when the name carries none.
func toolImageDigestOf(assetName string) string {
	stem := strings.TrimSuffix(assetName, filepath.Ext(assetName))
	i := strings.LastIndexByte(stem, '-')
	if i < 0 {
		return ""
	}
	digest := stem[i+1:]
	if len(digest) != 16 {
		return ""
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return ""
	}
	return digest
}

// ReadToolImageAsset reads the copy SaveToolImageAsset kept under assetName
// with the session's assets, and only that copy: a bare name, a regular file
// (a link planted under the name is not followed), at most maxBytes, and
// bytes whose digest is the one the name carries. A copy that changed since
// it was saved is not the picture the call showed, and sending it would
// change a request the provider has cached, so it is refused like a missing
// one.
func ReadToolImageAsset(sessionDir, assetName string, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return nil, fmt.Errorf("the session has no directory to keep copies in")
	}
	if assetName == "" || assetName == "." || assetName == ".." || strings.ContainsAny(assetName, `/\`) {
		return nil, fmt.Errorf("%q is not the name of a copy", assetName)
	}
	want := toolImageDigestOf(assetName)
	if want == "" {
		return nil, fmt.Errorf("the name %s carries no digest of its content", assetName)
	}
	path := filepath.Join(AssetsPath(sessionDir), assetName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("the copy %s is not a regular file", assetName)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("the copy %s is larger than %d bytes", assetName, maxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("the copy %s is larger than %d bytes", assetName, maxBytes)
	}
	// Whatever the path led to by the time it was opened, only the bytes the
	// name was given for go on.
	if toolImageDigest(data) != want {
		return nil, fmt.Errorf("the copy %s no longer holds the picture it was saved with", assetName)
	}
	return data, nil
}

// holds reports whether path is a regular file with exactly data in it.
func holds(path string, data []byte) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
		return false
	}
	existing, err := os.ReadFile(path)
	return err == nil && bytes.Equal(existing, data)
}

// ToolImagesMeta returns meta with the pictures of a tool call under
// coddy.images, the _meta of its final tool_call_update. Whatever meta already
// carries stays; with no pictures meta is returned as it was.
func ToolImagesMeta(meta map[string]interface{}, images []ToolImage) map[string]interface{} {
	if len(images) == 0 {
		return meta
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	coddy, _ := meta["coddy"].(map[string]interface{})
	if coddy == nil {
		coddy = map[string]interface{}{}
		meta["coddy"] = coddy
	}
	coddy["images"] = append([]ToolImage(nil), images...)
	return meta
}

// ToolImagesFromMeta reads the pictures of a tool_call_update's _meta, both
// the value the agent published in this process and its JSON on the wire.
func ToolImagesFromMeta(meta map[string]interface{}) []ToolImage {
	coddy, _ := meta["coddy"].(map[string]interface{})
	raw, ok := coddy["images"]
	if !ok || raw == nil {
		return nil
	}
	if images, ok := raw.([]ToolImage); ok {
		return append([]ToolImage(nil), images...)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var images []ToolImage
	if json.Unmarshal(data, &images) != nil || len(images) == 0 {
		return nil
	}
	return images
}
