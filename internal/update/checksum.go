package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/minisig"
)

const (
	// checksumAssetName is the digest list CI publishes next to the archives.
	checksumAssetName = "SHA256SUMS"
	// signatureAssetName is the signature of that list.
	signatureAssetName = checksumAssetName + ".minisig"
	// firstSignedRelease is the first release of DefaultRepo that had to be
	// signed.
	firstSignedRelease = "1.3.68"
)

// releaseSigningKeys are the public keys a release of DefaultRepo is signed by
// (docs/contributing/build.md, Signing releases). A new key goes in front of the
// old one, so a build reads the releases of both while the key changes hands.
var releaseSigningKeys = []string{
	"RWSLolQA8N9GQFw9asOjHG/nEX+oFoPyxf2gulCoDnQFS41gspb5hV46",
}

// verifyAssetChecksum compares a downloaded archive against the digest the
// release publishes for it, and the list of digests against its signature. The
// bytes are about to be unpacked and executed as the user's Coddy, and a resumed
// download stitches its body together from several responses, so this is the
// only thing standing between a truncated or substituted transfer and an
// installed binary. The digests alone do not stop whoever can upload release
// assets, who can upload matching digests too; the signature does.
//
// A release of this repository from firstSignedRelease on has to carry both,
// and is refused without them. A release without a digest list - a fork built by
// hand, an older tag - is reported and installed anyway.
func verifyAssetChecksum(ctx context.Context, client *http.Client, repo string, rel *ghRelease, assetName string, data []byte, out io.Writer) error {
	sums, err := pickAsset(rel, checksumAssetName)
	if err != nil {
		if signatureRequired(repo, rel.TagName) {
			return fmt.Errorf("release %s publishes no %s, which every release from %s on has", rel.TagName, checksumAssetName, firstSignedRelease)
		}
		_, _ = fmt.Fprintf(out, "Release %s publishes no %s; skipping checksum verification.\n", rel.TagName, checksumAssetName)
		return nil
	}
	body, err := downloadURL(ctx, client, sums.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", checksumAssetName, err)
	}
	if err := verifyChecksumSignature(ctx, client, repo, rel, body, out); err != nil {
		return err
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

// verifyChecksumSignature checks the digest list against the signature published
// next to it and the keys this build carries. A signature that does not verify
// always refuses the release. A missing one refuses it from firstSignedRelease
// on, so taking the signature away does not take the check away; before that it
// is reported, since those releases were published without one.
func verifyChecksumSignature(ctx context.Context, client *http.Client, repo string, rel *ghRelease, list []byte, out io.Writer) error {
	if repo != DefaultRepo {
		return nil
	}
	asset, err := pickAsset(rel, signatureAssetName)
	if err != nil {
		if signatureRequired(repo, rel.TagName) {
			return fmt.Errorf("release %s publishes no %s, which every release from %s on has", rel.TagName, signatureAssetName, firstSignedRelease)
		}
		_, _ = fmt.Fprintf(out, "Release %s publishes no %s; its %s is not verified.\n", rel.TagName, signatureAssetName, checksumAssetName)
		return nil
	}
	signature, err := downloadURL(ctx, client, asset.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", signatureAssetName, err)
	}
	if err := VerifyReleaseSignature(rel.TagName, list, signature); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Verified the signature of %s.\n", checksumAssetName)
	return nil
}

// VerifyReleaseSignature authenticates a digest list and its release tag with
// the keys this build trusts. The release signer uses the same check before it
// publishes a signature, so a misconfigured CI secret cannot produce a release
// that the updater refuses.
func VerifyReleaseSignature(tag string, list, signature []byte) error {
	if len(releaseSigningKeys) == 0 {
		return errors.New("this build carries no release signing keys")
	}
	var lastErr error
	for _, line := range releaseSigningKeys {
		key, err := minisig.ParsePublicKey(line)
		if err != nil {
			return fmt.Errorf("the signing key of this build: %w", err)
		}
		comment, err := minisig.Verify(key, list, signature)
		if err != nil {
			lastErr = err
			continue
		}
		if want := minisig.ReleaseComment(tag); comment != want {
			return fmt.Errorf("%s of release %s is signed for %q", checksumAssetName, tag, comment)
		}
		return nil
	}
	return fmt.Errorf("%s of release %s: %w", checksumAssetName, tag, lastErr)
}

// signatureRequired reports whether this repository's release has to be signed.
func signatureRequired(repo, tag string) bool {
	return repo == DefaultRepo && CompareSemver(tag, firstSignedRelease) >= 0
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
