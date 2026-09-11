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
	"slices"
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

// writeExtras lays out the installer's share directory under prefix with the
// given body in every extra, and returns that directory.
func writeExtras(t *testing.T, prefix string, body string, only ...string) string {
	t.Helper()
	share := filepath.Join(prefix, "share")
	for _, e := range extraFiles {
		if len(only) > 0 && !slices.Contains(only, e.Member) {
			continue
		}
		path := filepath.Join(share, filepath.FromSlash(e.Rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return share
}

func readExtra(t *testing.T, share string, member string) string {
	t.Helper()
	for _, e := range extraFiles {
		if e.Member != member {
			continue
		}
		b, err := os.ReadFile(filepath.Join(share, filepath.FromSlash(e.Rel)))
		if err != nil {
			t.Fatalf("read %s: %v", e.Label, err)
		}
		return string(b)
	}
	t.Fatalf("no extra named %q", member)
	return ""
}

func mustReleaseTarGz(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	data, err := tarGzArchiveOf(members)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInstallRelease_refreshesOnlyTheExtrasThatAreInstalled(t *testing.T) {
	t.Parallel()
	prefix := t.TempDir()
	dest := filepath.Join(prefix, "bin", "coddy")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The user kept the zsh completion and nothing else - say, the install ran
	// with --no-shell-setup and they wired one file up by hand.
	share := writeExtras(t, prefix, "installed", "coddy.zsh")
	archive := mustReleaseTarGz(t, map[string][]byte{
		"coddy": []byte("release"), "coddy.1": []byte("man"), "coddy.bash": []byte("bash"), "coddy.zsh": []byte("zsh"),
	})

	var out bytes.Buffer
	if err := installRelease(archive, "coddy_0.9.3_linux_amd64.tar.gz", dest, "0.9.3", &out); err != nil {
		t.Fatal(err)
	}
	if got := readExtra(t, share, "coddy.zsh"); got != "zsh" {
		t.Fatalf("zsh completion = %q, want the release copy", got)
	}
	for _, member := range []string{"coddy.1", "coddy.bash"} {
		for _, e := range extraFiles {
			if e.Member != member {
				continue
			}
			if _, err := os.Stat(filepath.Join(share, filepath.FromSlash(e.Rel))); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s was created, but the update must only refresh what the installer put there (stat: %v)", e.Label, err)
			}
		}
	}
	want := "Refreshed the zsh completion under " + share
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output %q does not contain %q", out.String(), want)
	}
}

func TestInstallRelease_keepsTheExtrasAReleaseDoesNotCarry(t *testing.T) {
	t.Parallel()
	prefix := t.TempDir()
	dest := filepath.Join(prefix, "bin", "coddy")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	share := writeExtras(t, prefix, "installed")
	// A release from before the archives carried the extras: binary only.
	archive := mustTarGz(t, "coddy", []byte("release"))

	var out bytes.Buffer
	if err := installRelease(archive, "coddy_0.9.3_linux_amd64.tar.gz", dest, "0.9.3", &out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "release" {
		t.Fatalf("executable = %q, want the release copy", got)
	}
	for _, e := range extraFiles {
		if got := readExtra(t, share, e.Member); got != "installed" {
			t.Errorf("%s = %q, want the installed copy kept", e.Label, got)
		}
	}
	want := "Kept the man page, the bash completion and the zsh completion under " + share + ": release 0.9.3 does not carry them"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output %q does not contain %q", out.String(), want)
	}
}

func TestInstallRelease_leavesAnExecutableOutsideBinAlone(t *testing.T) {
	t.Parallel()
	prefix := t.TempDir()
	// A build tree: the binary is not under a bin directory, so no share
	// directory pairs with it, whatever happens to sit next door.
	dest := filepath.Join(prefix, "build", "coddy")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	share := writeExtras(t, prefix, "installed")
	archive := mustReleaseTarGz(t, map[string][]byte{"coddy": []byte("release"), "coddy.1": []byte("man")})

	var out bytes.Buffer
	if err := installRelease(archive, "coddy_0.9.3_linux_amd64.tar.gz", dest, "0.9.3", &out); err != nil {
		t.Fatal(err)
	}
	if got := readExtra(t, share, "coddy.1"); got != "installed" {
		t.Fatalf("man page = %q, want it untouched", got)
	}
	if strings.Contains(out.String(), "Refreshed") || strings.Contains(out.String(), "Kept") {
		t.Fatalf("output mentions extras for an executable outside bin: %q", out.String())
	}
}

func TestShareDirFor(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/home/u/.local/bin/coddy": "/home/u/.local/share",
		"/usr/local/bin/coddy":     "/usr/local/share",
		"/opt/coddy/bin/coddy":     "/opt/coddy/share",
		"/home/u/src/build/coddy":  "",
		"/home/u/coddy":            "",
	}
	for dest, want := range cases {
		got := shareDirFor(filepath.FromSlash(dest))
		if want != "" {
			want = filepath.FromSlash(want)
		}
		if got != want {
			t.Errorf("shareDirFor(%q) = %q, want %q", dest, got, want)
		}
	}
}

func TestJoinLabels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"man page"}, "the man page"},
		{[]string{"man page", "zsh completion"}, "the man page and the zsh completion"},
		{[]string{"man page", "bash completion", "zsh completion"}, "the man page, the bash completion and the zsh completion"},
	}
	for _, c := range cases {
		if got := joinLabels(c.in); got != c.want {
			t.Errorf("joinLabels(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFilesFromTarGz_returnsOnlyTheMembersAsked(t *testing.T) {
	t.Parallel()
	archive := mustReleaseTarGz(t, map[string][]byte{
		"coddy": []byte("bin"), "coddy.1": []byte("man"), "README": []byte("notes"),
	})
	files, err := filesFromTarGz(archive, map[string]bool{"coddy": true, "coddy.zsh": true})
	if err != nil {
		t.Fatal(err)
	}
	if string(files["coddy"]) != "bin" {
		t.Errorf("coddy = %q", files["coddy"])
	}
	if _, ok := files["coddy.zsh"]; ok {
		t.Error("a member the archive lacks must be absent, not empty")
	}
	if _, ok := files["coddy.1"]; ok {
		t.Error("a member nobody asked for was read")
	}
}

// notesRelease is the JSON of one release the way GET /releases/latest and
// /releases/tags/{tag} answer, with the notes and the page fields the report
// reads and one linux/amd64 archive served from the same host.
func notesRelease(host, tag, body string) string {
	return fmt.Sprintf(`{"tag_name":%q,"name":%q,"html_url":"https://github.com/coddy-project/coddy-agent/releases/tag/%s","published_at":"2026-09-11T10:00:00Z","body":%q,"assets":[{"name":"coddy_%s_linux_amd64.tar.gz","browser_download_url":"http://%s/asset.tar.gz"}]}`,
		tag, tag, tag, body, tag, host)
}

// notesServer serves a release for tag, its archive, and - when list is not
// empty - the release history at /releases. An empty list answers 404 there,
// which is what a rate-limited or offline API looks like to the report.
func notesServer(t *testing.T, tag string, archive []byte, list string) *httptest.Server {
	t.Helper()
	body := "## What's Changed\n* fix(update): the change in " + tag + " by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/7\n\n**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/prev..." + tag
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/coddy-project/coddy-agent/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(notesRelease(r.Host, tag, body)))
	})
	mux.HandleFunc("/repos/coddy-project/coddy-agent/releases/tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(notesRelease(r.Host, tag, body)))
	})
	mux.HandleFunc("/repos/coddy-project/coddy-agent/releases", func(w http.ResponseWriter, _ *http.Request) {
		if list == "" {
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(list))
	})
	mux.HandleFunc("/asset.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func notesOptions(srv *httptest.Server, dest, current string) Options {
	return Options{
		APIBase:        srv.URL,
		Repo:           "coddy-project/coddy-agent",
		CurrentVersion: current,
		GOOS:           "linux",
		GOARCH:         "amd64",
		InstallPath:    dest,
		Yes:            true,
	}
}

func TestRun_reportsTheComparisonWhenTheReleaseListIsUnavailable(t *testing.T) {
	t.Parallel()
	srv := notesServer(t, "0.9.4", mustTarGz(t, "coddy", []byte("release")), "")
	dest := filepath.Join(t.TempDir(), "coddy")

	var out bytes.Buffer
	opts := notesOptions(srv, dest, "0.9.2")
	opts.Stdout = &out
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Installed 0.9.4") {
		t.Fatalf("the update did not go through: %q", got)
	}
	if !strings.Contains(got, "Full changelog: https://github.com/coddy-project/coddy-agent/compare/0.9.2...0.9.4") {
		t.Fatalf("output does not fall back to the comparison link: %q", got)
	}
	if strings.Contains(got, "Changes since") {
		t.Fatalf("output opens a report it has no notes for: %q", got)
	}
}

func TestRun_reportsTheInstalledReleaseNotesForADevBuild(t *testing.T) {
	t.Parallel()
	srv := notesServer(t, "0.9.4", mustTarGz(t, "coddy", []byte("release")), "")
	dest := filepath.Join(t.TempDir(), "coddy")

	var out bytes.Buffer
	opts := notesOptions(srv, dest, "dev")
	opts.Stdout = &out
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Release notes for 0.9.4:",
		"- fix(update): the change in 0.9.4 (#7)",
		"https://github.com/coddy-project/coddy-agent/releases/tag/0.9.4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "/compare/") {
		t.Fatalf("a dev build has no release to compare from: %q", got)
	}
}

func TestRun_reportsTheInstalledReleaseNotesOnADowngrade(t *testing.T) {
	t.Parallel()
	srv := notesServer(t, "0.9.4", mustTarGz(t, "coddy", []byte("release")), "")
	dest := filepath.Join(t.TempDir(), "coddy")

	var out bytes.Buffer
	opts := notesOptions(srv, dest, "0.9.9")
	opts.TargetVersion = "0.9.4"
	opts.Stdout = &out
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Release notes for 0.9.4:") {
		t.Fatalf("output lacks the notes of the release installed: %q", got)
	}
	if strings.Contains(got, "/compare/") || strings.Contains(got, "Changes since") {
		t.Fatalf("a downgrade has no range to report: %q", got)
	}
}

func TestRun_reportsTheChangesBeforeTheWindowsHelperRuns(t *testing.T) {
	t.Parallel()
	list := `[{"tag_name":"0.9.4","published_at":"2026-09-11T10:00:00Z","body":"* feat(a): one by @x in https://github.com/coddy-project/coddy-agent/pull/1"},` +
		`{"tag_name":"0.9.3","published_at":"2026-09-10T10:00:00Z","body":"* fix(b): two by @x in https://github.com/coddy-project/coddy-agent/pull/2"},` +
		`{"tag_name":"0.9.2","published_at":"2026-09-09T10:00:00Z","body":"* fix(c): the running one by @x in https://github.com/coddy-project/coddy-agent/pull/3"}]`
	archive := mustZip(t, "coddy.exe", []byte("release"))
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/coddy-project/coddy-agent/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":"0.9.4","assets":[{"name":"coddy_0.9.4_windows_amd64.zip","browser_download_url":"http://%s/asset.zip"}]}`, r.Host)
	})
	mux.HandleFunc("/repos/coddy-project/coddy-agent/releases", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(list))
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "coddy.exe")
	var out bytes.Buffer
	var staged string
	err := Run(context.Background(), Options{
		APIBase:        srv.URL,
		Repo:           "coddy-project/coddy-agent",
		CurrentVersion: "0.9.2",
		GOOS:           "windows",
		GOARCH:         "amd64",
		InstallPath:    dest,
		Yes:            true,
		Stdout:         &out,
		windowsInstaller: func(req windowsUpdateRequest) error {
			staged = req.StagedPath
			return nil
		},
	})
	if staged != "" {
		_ = os.Remove(staged)
	}
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	ready := strings.Index(got, "Update downloaded")
	report := strings.Index(got, "Changes since 0.9.2:")
	if ready < 0 || report < ready {
		t.Fatalf("the report should follow the handoff line: %q", got)
	}
	for _, want := range []string{
		"0.9.3 (2026-09-10)",
		"  - fix(b): two (#2)",
		"0.9.4 (2026-09-11)",
		"  - feat(a): one (#1)",
		"Full changelog: https://github.com/coddy-project/coddy-agent/compare/0.9.2...0.9.4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "the running one") {
		t.Fatalf("the running release is not news: %q", got)
	}
	if strings.Index(got, "0.9.3 (") > strings.Index(got, "0.9.4 (") {
		t.Fatalf("releases should read oldest first: %q", got)
	}
}

func TestRun_upToDatePrintsNoNotes(t *testing.T) {
	t.Parallel()
	srv := notesServer(t, "0.9.4", nil, "")
	var out bytes.Buffer
	opts := notesOptions(srv, filepath.Join(t.TempDir(), "coddy"), "0.9.4")
	opts.Stdout = &out
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); strings.Contains(got, "Release notes") || strings.Contains(got, "Changes since") || strings.Contains(got, "/compare/") {
		t.Fatalf("nothing was installed, so nothing changed: %q", got)
	}
}

func TestFetchReleasesBetween_walksThePagesAndKeepsTheRange(t *testing.T) {
	t.Parallel()
	// Two full pages of releases, newest first, the way GitHub pages them,
	// with a draft and a prerelease inside the range that must not count.
	page := func(from, to int) string {
		var items []string
		for n := from; n >= to; n-- {
			extra := ""
			switch n {
			case 180:
				extra = `,"draft":true`
			case 170:
				extra = `,"prerelease":true`
			}
			items = append(items, fmt.Sprintf(`{"tag_name":"0.9.%d","body":"change %d"%s}`, n, n, extra))
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(page(200, 101)))
		case "2":
			_, _ = w.Write([]byte(page(100, 1)))
		default:
			_, _ = w.Write([]byte("[]"))
		}
	}))
	t.Cleanup(srv.Close)

	got, err := fetchReleasesBetween(context.Background(), srv.Client(), srv.URL, "coddy-project/coddy-agent", "0.9.150", "0.9.190")
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, rel := range got {
		tags = append(tags, rel.TagName)
	}
	if len(tags) != 38 || tags[0] != "0.9.151" || tags[len(tags)-1] != "0.9.190" {
		t.Fatalf("tags = %v", tags)
	}
	for _, skipped := range []string{"0.9.150", "0.9.170", "0.9.180", "0.9.191"} {
		if slices.Contains(tags, skipped) {
			t.Fatalf("%s should not be listed: %v", skipped, tags)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("the first page already reached the running release, requests = %v", requests)
	}

	got, err = fetchReleasesBetween(context.Background(), srv.Client(), srv.URL, "coddy-project/coddy-agent", "0.9.50", "0.9.52")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TagName != "0.9.51" || got[1].TagName != "0.9.52" {
		t.Fatalf("got %+v", got)
	}
	if len(requests) != 3 {
		t.Fatalf("the range on the second page takes two requests, got %v", requests)
	}
}

func TestNotesLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "generated notes lose the heading, the author trailer and the footer",
			body: "## What's Changed\r\n* feat(config): --dry-run probes what config.yaml points at by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/194\r\n\r\n\r\n**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.36...1.0.37",
			want: []string{"- feat(config): --dry-run probes what config.yaml points at (#194)"},
		},
		{
			name: "hand-written notes keep their sections as plain text",
			body: "Coddy 1.1 is the week after 1.0.\n\n## One process for every surface\n\n- #174 feat(serve): run every enabled subsystem in one process\n- #178 feat(serve): keep the daemon alive\n\n### Swarm\n- #155 feat(swarm): a stateless relay\n",
			want: []string{
				"Coddy 1.1 is the week after 1.0.",
				"One process for every surface:",
				"- #174 feat(serve): run every enabled subsystem in one process",
				"- #178 feat(serve): keep the daemon alive",
				"Swarm:",
				"- #155 feat(swarm): a stateless relay",
			},
		},
		{
			name: "an issue reference at the start of a line is not a heading",
			body: "#123 is fixed\n* @newcomer made their first contribution in https://github.com/coddy-project/coddy-agent/pull/5",
			want: []string{"#123 is fixed", "- @newcomer made their first contribution in https://github.com/coddy-project/coddy-agent/pull/5"},
		},
		{
			name: "empty notes",
			body: "\n\n  \n",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := notesLines(tc.body); !slices.Equal(got, tc.want) {
				t.Fatalf("notesLines(%q)\n got %q\nwant %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestCapLines(t *testing.T) {
	t.Parallel()
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = strconv.Itoa(i)
	}
	kept, more := capLines(lines, 20)
	if len(kept) != 20 || kept[19] != "19" || more != 5 {
		t.Fatalf("kept %d (last %q), more %d", len(kept), kept[len(kept)-1], more)
	}
	kept, more = capLines(lines[:3], 20)
	if len(kept) != 3 || more != 0 {
		t.Fatalf("kept %d, more %d", len(kept), more)
	}
}

func TestNotesDisabled(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]bool{
		"": false, "1": false, "true": false, "yes": false, "on": false,
		"0": true, "false": true, "no": true, "off": true, " OFF ": true, "False": true,
	} {
		if got := notesDisabled(value); got != want {
			t.Errorf("notesDisabled(%q) = %v, want %v", value, got, want)
		}
	}
}
