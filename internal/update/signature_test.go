package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/minisig"
)

const signedAssetName = "coddy_2.0.0_linux_amd64.tar.gz"

var signedAssetBody = []byte("the archive")

// signedRelease serves a digest list of signedAssetBody and, when signature is
// not nil, its signature.
func signedRelease(t *testing.T, tag string, signature []byte) (*ghRelease, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sig" {
			_, _ = w.Write(signature)
			return
		}
		_, _ = w.Write(signedList())
	}))
	t.Cleanup(srv.Close)
	rel := &ghRelease{TagName: tag, Assets: []releaseAsset{{Name: checksumAssetName, BrowserDownloadURL: srv.URL + "/sums"}}}
	if signature != nil {
		rel.Assets = append(rel.Assets, releaseAsset{Name: signatureAssetName, BrowserDownloadURL: srv.URL + "/sig"})
	}
	return rel, srv.Client()
}

func signedList() []byte {
	sum := sha256.Sum256(signedAssetBody)
	return fmt.Appendf(nil, "%s  %s\n", hex.EncodeToString(sum[:]), signedAssetName)
}

// withSigningKey makes the build carry a new key for the test, returning its
// secret. Not parallel: the keys are package state.
func withSigningKey(t *testing.T) string {
	t.Helper()
	secret, pub, err := minisig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keys := releaseSigningKeys
	releaseSigningKeys = []string{pub.String()}
	t.Cleanup(func() { releaseSigningKeys = keys })
	return secret
}

func signFor(t *testing.T, secret string, message []byte, tag string) []byte {
	t.Helper()
	signature, err := minisig.Sign(secret, message, minisig.ReleaseComment(tag))
	if err != nil {
		t.Fatal(err)
	}
	return signature
}

func TestASignedReleaseVerifies(t *testing.T) {
	secret := withSigningKey(t)
	rel, client := signedRelease(t, "2.0.0", signFor(t, secret, signedList(), "2.0.0"))
	var out bytes.Buffer
	if err := verifyAssetChecksum(context.Background(), client, DefaultRepo, rel, signedAssetName, signedAssetBody, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Verified the signature") {
		t.Fatalf("output = %q", out.String())
	}
}

// Whoever can upload release assets can upload matching digests too: none of
// these may install.
func TestAReleaseWithoutAValidSignatureIsRefused(t *testing.T) {
	secret := withSigningKey(t)
	otherSecret, _, err := minisig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		tag       string
		signature []byte
		want      string
	}{
		"no signature":             {"2.0.0", nil, signatureAssetName},
		"another key":              {"2.0.0", signFor(t, otherSecret, signedList(), "2.0.0"), "another key"},
		"signature of other bytes": {"2.0.0", signFor(t, secret, []byte("another list"), "2.0.0"), "does not match"},
		"signed for another tag":   {"2.0.0", signFor(t, secret, signedList(), "1.9.0"), "signed for"},
		"not a signature":          {"2.0.0", []byte("<html>"), "not a minisign signature"},
		"a bad one on an old tag":  {"1.0.0", signFor(t, otherSecret, signedList(), "1.0.0"), "another key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rel, client := signedRelease(t, tc.tag, tc.signature)
			var out bytes.Buffer
			err := verifyAssetChecksum(context.Background(), client, DefaultRepo, rel, signedAssetName, signedAssetBody, &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// A signed list says nothing if the list itself can go missing.
func TestARequiredReleaseWithoutAListIsRefused(t *testing.T) {
	withSigningKey(t)
	var out bytes.Buffer
	err := verifyAssetChecksum(context.Background(), nil, DefaultRepo, &ghRelease{TagName: "2.0.0"}, signedAssetName, signedAssetBody, &out)
	if err == nil || !strings.Contains(err.Error(), checksumAssetName) {
		t.Fatalf("err = %v, want a refusal naming the list", err)
	}
}

// Releases from before signing, and the releases of a fork named with --repo,
// which has keys of its own, are reported as they were.
func TestAReleaseNoSignatureWasRequiredOfStillInstalls(t *testing.T) {
	withSigningKey(t)
	t.Run("an old tag", func(t *testing.T) {
		rel, client := signedRelease(t, "1.0.0", nil)
		var out bytes.Buffer
		if err := verifyAssetChecksum(context.Background(), client, DefaultRepo, rel, signedAssetName, signedAssetBody, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "not verified") {
			t.Fatalf("output = %q", out.String())
		}
	})
	t.Run("a fork", func(t *testing.T) {
		rel, client := signedRelease(t, "2.0.0", nil)
		var out bytes.Buffer
		if err := verifyAssetChecksum(context.Background(), client, "someone/fork", rel, signedAssetName, signedAssetBody, &out); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a fork without a list", func(t *testing.T) {
		var out bytes.Buffer
		if err := verifyAssetChecksum(context.Background(), nil, "someone/fork", &ghRelease{TagName: "2.0.0"}, signedAssetName, signedAssetBody, &out); err != nil {
			t.Fatal(err)
		}
	})
}

// While a key changes hands a build carries both, and either verifies.
func TestAnyKeyOfTheBuildVerifies(t *testing.T) {
	secret := withSigningKey(t)
	_, newer, err := minisig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	releaseSigningKeys = append([]string{newer.String()}, releaseSigningKeys...)
	rel, client := signedRelease(t, "2.0.0", signFor(t, secret, signedList(), "2.0.0"))
	var out bytes.Buffer
	if err := verifyAssetChecksum(context.Background(), client, DefaultRepo, rel, signedAssetName, signedAssetBody, &out); err != nil {
		t.Fatal(err)
	}
}
