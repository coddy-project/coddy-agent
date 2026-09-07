package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// checksumAssetName is the digest list CI publishes next to the archives.
const checksumAssetName = "SHA256SUMS"

// verifyAssetChecksum compares a downloaded archive against the digest the
// release publishes for it. The bytes are about to be unpacked and executed as
// the user's Coddy, and a resumed download stitches its body together from
// several responses, so this is the only thing standing between a truncated or
// substituted transfer and an installed binary.
//
// A release without a digest list - a fork built by hand, an older tag - is
// reported and installed anyway; the check tightens what is published, it does
// not add a new requirement to publishing.
func verifyAssetChecksum(ctx context.Context, client *http.Client, rel *ghRelease, assetName string, data []byte, out io.Writer) error {
	sums, err := pickAsset(rel, checksumAssetName)
	if err != nil {
		_, _ = fmt.Fprintf(out, "Release %s publishes no %s; skipping checksum verification.\n", rel.TagName, checksumAssetName)
		return nil
	}
	body, err := downloadURL(ctx, client, sums.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", checksumAssetName, err)
	}
	want, err := findChecksum(string(body), assetName)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, %s lists %s", assetName, got, checksumAssetName, want)
	}
	_, _ = fmt.Fprintf(out, "Verified %s against %s.\n", assetName, checksumAssetName)
	return nil
}

// findChecksum reads the sha256sum output format: a hex digest, whitespace, and
// a file name that binary mode prefixes with an asterisk.
func findChecksum(list, assetName string) (string, error) {
	for _, line := range strings.Split(list, "\n") {
		digest, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if strings.TrimPrefix(strings.TrimSpace(name), "*") != assetName {
			continue
		}
		digest = strings.ToLower(digest)
		if len(digest) != sha256.Size*2 {
			return "", fmt.Errorf("%s lists a malformed digest for %s", checksumAssetName, assetName)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("%s lists a malformed digest for %s", checksumAssetName, assetName)
		}
		return digest, nil
	}
	return "", fmt.Errorf("%s does not list %s", checksumAssetName, assetName)
}
