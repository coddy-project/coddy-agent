package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInstallFromArchive_zip(t *testing.T) {
	t.Parallel()
	payload := mustZip(t, "nested/coddy.exe", []byte("Windows Coddy"))
	dir := t.TempDir()
	dest := filepath.Join(dir, "coddy.exe")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(payload, "coddy_0.9.3_windows_amd64.zip", dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "Windows Coddy" {
		t.Fatalf("unexpected content: %q", b)
	}
}

func TestDownloadURL_resumesAfterInterruptedResponse(t *testing.T) {
	t.Parallel()
	payload := []byte(strings.Repeat("Coddy", 32<<10))
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:len(payload)/2])
			return
		}
		wantRange := "bytes=" + strconv.Itoa(len(payload)/2) + "-"
		if got := r.Header.Get("Range"); got != wantRange {
			t.Errorf("Range = %q, want %q", got, wantRange)
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-len(payload)/2))
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(len(payload)/2)+"-"+strconv.Itoa(len(payload)-1)+"/"+strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[len(payload)/2:])
	}))
	defer srv.Close()

	reporter := &recordingDownloadReporter{}
	got, err := downloadURL(context.Background(), srv.Client(), srv.URL, reporter)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded bytes differ: got %d, want %d", len(got), len(payload))
	}
	if reporter.retries != 1 {
		t.Fatalf("retries = %d, want 1", reporter.retries)
	}
}

type recordingDownloadReporter struct {
	retries int
}

func (*recordingDownloadReporter) Complete(int64)        {}
func (*recordingDownloadReporter) Progress(int64, int64) {}
func (r *recordingDownloadReporter) Retry(int, int, error) {
	r.retries++
}

func TestInstallFromArchive_tarGz(t *testing.T) {
	t.Parallel()
	payload := mustTarGz(t, "coddy", []byte("#!/bin/sh\necho ok\n"))
	dir := t.TempDir()
	dest := filepath.Join(dir, "coddy")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(payload, "coddy_0.9.3_linux_amd64.tar.gz", dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("echo ok")) {
		t.Fatalf("unexpected content: %q", b)
	}
}

func TestRun_checkUpdateAvailable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"0.9.5","assets":[{"name":"coddy_0.9.5_linux_amd64.tar.gz","browser_download_url":"http://example.invalid/x.tar.gz"}]}`))
	}))
	defer srv.Close()

	err := Run(context.Background(), Options{
		APIBase:        srv.URL,
		Repo:           "coddy-project/coddy-agent",
		CurrentVersion: "0.9.2",
		GOOS:           "linux",
		GOARCH:         "amd64",
		CheckOnly:      true,
	})
	if !errors.Is(err, ErrUpdateAvailable) {
		t.Fatalf("got %v, want ErrUpdateAvailable", err)
	}
}

func TestRun_checkUpToDate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/coddy-project/coddy-agent/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"0.9.3","assets":[{"name":"coddy_0.9.3_linux_amd64.tar.gz","browser_download_url":"http://example.invalid/bin.tar.gz"}]}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		APIBase:        srv.URL,
		Repo:           "coddy-project/coddy-agent",
		CurrentVersion: "0.9.3",
		GOOS:           "linux",
		GOARCH:         "amd64",
		CheckOnly:      true,
		Stdout:         &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "up to date") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestRun_downloadAndInstall(t *testing.T) {
	t.Parallel()
	binBody := []byte("#!/bin/sh\necho release\n")
	archive := mustTarGz(t, "coddy", binBody)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/coddy-project/coddy-agent/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		url := "http://" + r.Host + "/asset.tar.gz"
		body := `{"tag_name":"0.9.4","assets":[{"name":"coddy_0.9.4_linux_amd64.tar.gz","browser_download_url":"` + url + `"}]}`
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/asset.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "coddy")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		APIBase:        srv.URL,
		Repo:           "coddy-project/coddy-agent",
		CurrentVersion: "0.9.2",
		GOOS:           "linux",
		GOARCH:         "amd64",
		InstallPath:    dest,
		Yes:            true,
		Stdout:         &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binBody) {
		t.Fatalf("installed bytes mismatch: %q", got)
	}
	if !strings.Contains(out.String(), "0.9.4") {
		t.Fatalf("output: %s", out.String())
	}
}

func mustTarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	data, err := tarGzArchive(name, body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustZip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	data, err := zipArchive(name, body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDownloadURL_rejectsAMisalignedResume(t *testing.T) {
	t.Parallel()
	payload := []byte(strings.Repeat("Coddy", 32<<10))
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:len(payload)/2])
			return
		}
		// A byte off is enough to splice a gap into the archive, and nothing
		// downstream would notice it.
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[1:])
	}))
	defer srv.Close()

	_, err := downloadURL(context.Background(), srv.Client(), srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "resumed at byte 1") {
		t.Fatalf("error = %v, want a misaligned resume", err)
	}
}

func TestDownloadURL_restartsWhenTheServerIgnoresRange(t *testing.T) {
	t.Parallel()
	payload := []byte(strings.Repeat("Coddy", 32<<10))
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:len(payload)/2])
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	got, err := downloadURL(context.Background(), srv.Client(), srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(payload))
	}
}

func TestFindChecksum(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("ab", 32)
	list := "0000  coddy_1.0.0_linux_amd64.tar.gz\n" + digest + " *coddy_1.0.0_windows_amd64.zip\n"

	got, err := findChecksum(list, "coddy_1.0.0_windows_amd64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if got != digest {
		t.Fatalf("digest = %q, want %q", got, digest)
	}
	if _, err := findChecksum(list, "coddy_1.0.0_darwin_arm64.tar.gz"); err == nil {
		t.Fatal("expected an error for an asset the list does not cover")
	}
	if _, err := findChecksum(list, "coddy_1.0.0_linux_amd64.tar.gz"); err == nil {
		t.Fatal("expected an error for a malformed digest")
	}
}

func TestVerifyAssetChecksum_rejectsAMismatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  coddy_1.0.0_linux_amd64.tar.gz\n", strings.Repeat("ab", 32))
	}))
	defer srv.Close()

	rel := &ghRelease{TagName: "1.0.0", Assets: []releaseAsset{{Name: checksumAssetName, BrowserDownloadURL: srv.URL}}}
	err := verifyAssetChecksum(context.Background(), srv.Client(), rel, "coddy_1.0.0_linux_amd64.tar.gz", []byte("tampered"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want a checksum mismatch", err)
	}
}

func TestVerifyAssetChecksum_skipsAReleaseWithoutAList(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	rel := &ghRelease{TagName: "1.0.0"}
	if err := verifyAssetChecksum(context.Background(), nil, rel, "coddy_1.0.0_linux_amd64.tar.gz", []byte("anything"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skipping checksum verification") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDownloadProgress_drawsNoBarOutsideATerminal(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := newDownloadProgress(&out, "coddy_1.0.0_linux_amd64.tar.gz")
	for i := int64(1); i <= 100; i++ {
		p.Progress(i, 100)
	}
	if strings.Contains(out.String(), "\r") {
		t.Fatalf("redrew a progress bar into a plain writer: %q", out.String())
	}
	p.Complete(100)
	if !strings.Contains(out.String(), "Downloaded") {
		t.Fatalf("output = %q", out.String())
	}
}
