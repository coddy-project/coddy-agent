package skills

// Edge cases of the archive plugin source. The happy path is
// features/plugin_archive_source.feature; what is here is everything an
// archive must not get past: an entry that leaves the unpack folder, a link or
// a device, the caps on the download, the unpacked size and the entry count, a
// sha256 that does not match, a plain-http address, an address the SSRF guard
// refuses and a redirect to either.

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

// zipEntry is one entry of a test archive: a file with its body, or whatever
// else its mode makes it (a folder, a link, a device).
type zipEntry struct {
	name string
	body string
	mode fs.FileMode // zero is a regular 0644 file
}

// zipOf packs entries into a zip archive, in order.
func zipOf(entries ...zipEntry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if mode.IsDir() {
			fh.Method = zip.Store
		}
		fh.SetMode(mode)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			return nil, err
		}
		if e.body != "" {
			if _, err := io.WriteString(w, e.body); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mustZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	data, err := zipOf(entries...)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// demoSkill is the SKILL.md of the skill "demo".
const demoSkill = "---\nname: demo\ndescription: the demo skill\n---\n\n# demo\n"

// demoArchive is a plugin archive with the skill "demo" in it.
func demoArchive(t *testing.T) []byte {
	return mustZip(t,
		zipEntry{name: ".claude-plugin/plugin.json", body: `{"name":"demo"}`},
		zipEntry{name: "skills/demo/SKILL.md", body: demoSkill},
	)
}

// reachRemote lets remote-source downloads reach test servers on loopback: the
// guard passes the hosts of tlsSrv and also and refuses every other address,
// and the client trusts the test certificate (every httptest TLS server
// shares it).
func reachRemote(t *testing.T, tlsSrv *httptest.Server, also ...*httptest.Server) {
	t.Helper()
	hosts := map[string]bool{tlsSrv.Listener.Addr().String(): true}
	for _, s := range also {
		hosts[s.Listener.Addr().String()] = true
	}
	prevGuard, prevTransport := remoteGuard, remoteTransport
	remoteGuard = func(_ context.Context, raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if !hosts[u.Host] {
			return fmt.Errorf("%w: %s", web.ErrDisallowedURL, u.Host)
		}
		return nil
	}
	remoteTransport = tlsSrv.Client().Transport
	t.Cleanup(func() { remoteGuard, remoteTransport = prevGuard, prevTransport })
}

// serveBytes answers every request with data and counts the requests.
func serveBytes(data []byte, hits *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(data)
	})
}

// installArchive runs the marketplace installer on one archive plugin entry
// into a fresh managed dir.
func installArchive(t *testing.T, p MarketplacePlugin) (string, map[string]RemoteEntry, *SyncResult, error) {
	t.Helper()
	managed := t.TempDir()
	lock := map[string]RemoteEntry{}
	res := &SyncResult{}
	base := RemoteEntry{Source: "https://market.example/marketplace.json", URL: "https://market.example/marketplace.json"}
	err := installPlugin(context.Background(), p, "", base.Source, base, managed, lock, res)
	return managed, lock, res, err
}

func archivePlugin(rawURL, sum string) MarketplacePlugin {
	return MarketplacePlugin{Name: "demo", Source: PluginSource{Kind: "archive", URL: rawURL, SHA256: sum}}
}

// assertNothingInstalled fails when the managed dir holds anything at all: a
// skill, a staging or backup copy, a lock entry.
func assertNothingInstalled(t *testing.T, managed string, lock map[string]RemoteEntry, res *SyncResult) {
	t.Helper()
	entries, err := os.ReadDir(managed)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("managed dir is not empty: %v", names)
	}
	if len(lock) != 0 {
		t.Errorf("lock gained entries: %+v", lock)
	}
	if len(res.Added)+len(res.Updated) != 0 {
		t.Errorf("result reports installs: %+v", res)
	}
}

func TestArchiveEntryPath(t *testing.T) {
	ok := map[string]string{
		"skills/demo/SKILL.md": filepath.Join("skills", "demo", "SKILL.md"),
		"./skills/demo/":       filepath.Join("skills", "demo"),
		"skills//demo":         filepath.Join("skills", "demo"),
		`skills\demo\run.sh`:   filepath.Join("skills", "demo", "run.sh"),
		"a/b..c/..d":           filepath.Join("a", "b..c", "..d"),
		"notes./.hidden/x":     filepath.Join("notes.", ".hidden", "x"),
	}
	for name, want := range ok {
		got, err := archiveEntryPath(name)
		if err != nil || got != want {
			t.Errorf("archiveEntryPath(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{
		"",
		"../evil",
		"skills/../../evil",
		"skills/demo/..",
		`..\evil`,
		`skills\..\..\evil`,
		"/etc/passwd",
		`\evil`,
		"C:/evil",
		`C:\evil`,
		"c:evil",
		"skills/\x00evil",
		// Windows trims trailing dots and spaces off an element: these climb there.
		".. /evil",
		"skills/.. /.. /evil",
		"... /evil",
		".../evil",
		"skills/. ./evil",
		" ../evil",
	} {
		if got, err := archiveEntryPath(name); !errors.Is(err, errUnsafeArchive) {
			t.Errorf("archiveEntryPath(%q) = %q, %v; want errUnsafeArchive", name, got, err)
		}
	}
}

func TestExtractArchiveRefusesEntriesThatCouldEscape(t *testing.T) {
	outside := t.TempDir()
	absolute := filepath.ToSlash(filepath.Join(outside, "evil.txt"))
	cases := []struct {
		name string
		bad  zipEntry
	}{
		{"a parent element", zipEntry{name: "../evil.txt", body: "owned"}},
		{"a parent element deeper in", zipEntry{name: "skills/../../evil.txt", body: "owned"}},
		{"a parent element with backslashes", zipEntry{name: `..\evil.txt`, body: "owned"}},
		{"an absolute path", zipEntry{name: absolute, body: "owned"}},
		{"a drive letter", zipEntry{name: "C:/evil.txt", body: "owned"}},
		{"a symlink", zipEntry{name: "skills/demo/evil", body: "../../../evil.txt", mode: fs.ModeSymlink | 0o777}},
		{"a device", zipEntry{name: "skills/demo/disk", mode: fs.ModeDevice | 0o644}},
		{"a character device", zipEntry{name: "skills/demo/tty", mode: fs.ModeDevice | fs.ModeCharDevice | 0o644}},
		{"a named pipe", zipEntry{name: "skills/demo/fifo", mode: fs.ModeNamedPipe | 0o644}},
		{"a socket", zipEntry{name: "skills/demo/sock", mode: fs.ModeSocket | 0o644}},
		{"a file named twice", zipEntry{name: `skills\demo\SKILL.md`, body: "---\nname: other\n---\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			zipPath := filepath.Join(root, "plugin.zip")
			data := mustZip(t,
				zipEntry{name: "skills/demo/SKILL.md", body: demoSkill},
				tc.bad,
				zipEntry{name: "skills/demo/after.txt", body: "after"},
			)
			if err := os.WriteFile(zipPath, data, 0o644); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(root, "unpack")
			err := extractArchive(zipPath, dest, defaultArchiveLimits)
			if !errors.Is(err, errUnsafeArchive) {
				t.Fatalf("extractArchive = %v, want errUnsafeArchive", err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(tc.bad.name)) {
				t.Errorf("error %q does not name the entry %q", err, tc.bad.name)
			}
			// Every entry is checked before the first is written, so the unpack
			// folder gets nothing at all, and nothing lands beside it.
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Errorf("unpack folder was created: %v", err)
			}
			for _, p := range []string{filepath.Join(root, "evil.txt"), filepath.Join(outside, "evil.txt")} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Errorf("%s was written: %v", p, err)
				}
			}
		})
	}
}

func TestExtractArchiveCaps(t *testing.T) {
	lim := archiveLimits{download: 1 << 20, unpacked: 1000, entries: 3}
	lying := func(t *testing.T) []byte {
		// The central directory says 10 bytes, the data inflates to 2000.
		payload := bytes.Repeat([]byte("a"), 2000)
		var packed bytes.Buffer
		fw, err := flate.NewWriter(&packed, flate.BestCompression)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(payload)
		_ = fw.Close()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		fh := &zip.FileHeader{
			Name:               "skills/demo/big.txt",
			Method:             zip.Deflate,
			CRC32:              crc32.ChecksumIEEE(payload),
			CompressedSize64:   uint64(packed.Len()),
			UncompressedSize64: 10,
		}
		fh.SetMode(0o644)
		w, err := zw.CreateRaw(fh)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(packed.Bytes())
		_ = zw.Close()
		return buf.Bytes()
	}
	cases := []struct {
		name   string
		data   func(t *testing.T) []byte
		causes []error // any of them
	}{
		{"too many entries", func(t *testing.T) []byte {
			return mustZip(t,
				zipEntry{name: "a.txt", body: "a"}, zipEntry{name: "b.txt", body: "b"},
				zipEntry{name: "c.txt", body: "c"}, zipEntry{name: "d.txt", body: "d"})
		}, []error{errArchiveTooLarge}},
		{"one file over the unpacked cap", func(t *testing.T) []byte {
			return mustZip(t, zipEntry{name: "big.txt", body: strings.Repeat("x", 1001)})
		}, []error{errArchiveTooLarge}},
		{"files together over the unpacked cap", func(t *testing.T) []byte {
			return mustZip(t,
				zipEntry{name: "a.txt", body: strings.Repeat("x", 400)},
				zipEntry{name: "b.txt", body: strings.Repeat("x", 400)},
				zipEntry{name: "c.txt", body: strings.Repeat("x", 400)})
		}, []error{errArchiveTooLarge}},
		// The zip reader stops at the declared size itself; the cap is the
		// second line.
		{"a header that understates the size", lying, []error{zip.ErrFormat, errArchiveTooLarge}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			zipPath := filepath.Join(root, "plugin.zip")
			if err := os.WriteFile(zipPath, tc.data(t), 0o644); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(root, "unpack")
			err := extractArchive(zipPath, dest, lim)
			refused := false
			for _, cause := range tc.causes {
				refused = refused || errors.Is(err, cause)
			}
			if !refused {
				t.Fatalf("extractArchive = %v, want one of %v", err, tc.causes)
			}
			var total int64
			_ = filepath.Walk(dest, func(_ string, fi os.FileInfo, err error) error {
				if err == nil && fi.Mode().IsRegular() {
					total += fi.Size()
				}
				return nil
			})
			if total > int64(lim.unpacked) {
				t.Errorf("%d bytes were unpacked, over the cap of %d", total, lim.unpacked)
			}
		})
	}
}

func TestExtractArchiveModesAndMacMetadata(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "plugin.zip")
	data := mustZip(t,
		zipEntry{name: "demo-main/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "demo-main/.claude-plugin/plugin.json", body: `{"name":"demo"}`},
		zipEntry{name: "demo-main/skills/demo/SKILL.md", body: demoSkill},
		zipEntry{name: "demo-main/skills/demo/scripts/run.sh", body: "#!/bin/sh\n", mode: 0o755},
		zipEntry{name: "demo-main/skills/demo/scripts/only-owner.sh", body: "#!/bin/sh\n", mode: 0o700},
		zipEntry{name: "demo-main/skills/demo/notes.txt", body: "notes", mode: 0o666},
		zipEntry{name: "__MACOSX/demo-main/skills/demo/._SKILL.md", body: "resource fork"},
	)
	if err := os.WriteFile(zipPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "unpack")
	if err := extractArchive(zipPath, dest, defaultArchiveLimits); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "__MACOSX")); !os.IsNotExist(err) {
		t.Errorf("__MACOSX was unpacked: %v", err)
	}
	if got, _, err := archivePluginRoot(dest); err != nil || got != filepath.Join(dest, "demo-main") {
		t.Errorf("archivePluginRoot = %q, %v; want the wrapping folder", got, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for rel, want := range map[string]os.FileMode{
		"scripts/run.sh":        0o755,
		"scripts/only-owner.sh": 0o755,
		"notes.txt":             0o644,
		"SKILL.md":              0o644,
	} {
		fi, err := os.Stat(filepath.Join(dest, "demo-main", "skills", "demo", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		// The process umask may take bits away, never add them.
		if got := fi.Mode().Perm(); got&^want != 0 || (want&0o100 != 0) != (got&0o100 != 0) {
			t.Errorf("%s: mode %v, want %v", rel, got, want)
		}
	}
}

// writeTree writes files (slash paths) under dir; a SKILL.md gets a
// frontmatter name after its folder, anything else a placeholder body.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for f, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "" && filepath.Base(p) == "SKILL.md" {
			body = "---\nname: " + filepath.Base(filepath.Dir(p)) + "\ndescription: d\n---\n"
		}
		if body == "" {
			body = "x"
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchivePluginRoot(t *testing.T) {
	cases := []struct {
		name     string
		files    []string
		root     string
		manifest string // "" when the plugin root carries none
	}{
		{"a manifest at the top", []string{".claude-plugin/plugin.json", "skills/demo/SKILL.md"}, ".", ".claude-plugin/plugin.json"},
		{"a Codex manifest at the top", []string{".codex-plugin/plugin.json", "skills/demo/SKILL.md"}, ".", ".codex-plugin/plugin.json"},
		{"both manifests: the Claude one is read", []string{".claude-plugin/plugin.json", ".codex-plugin/plugin.json"}, ".", ".claude-plugin/plugin.json"},
		{"one wrapping folder with a manifest", []string{"demo-main/.claude-plugin/plugin.json", "demo-main/skills/demo/SKILL.md"}, "demo-main", "demo-main/.claude-plugin/plugin.json"},
		{"one wrapping folder without a manifest", []string{"demo-main/skills/demo/SKILL.md"}, "demo-main", ""},
		{"no manifest, several entries at the top", []string{"skills/demo/SKILL.md", "README.md"}, ".", ""},
		{"a wrapping folder beside a file is no wrapper", []string{"demo-main/.claude-plugin/plugin.json", "README.md"}, ".", ""},
		{"a manifest two folders deep is not seen", []string{"outer/inner/.claude-plugin/plugin.json"}, "outer", ""},
		{"a manifest folder without plugin.json", []string{".claude-plugin/marketplace.json", "skills/demo/SKILL.md"}, ".", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{}
			for _, f := range tc.files {
				files[f] = ""
			}
			writeTree(t, dir, files)
			root, manifest, err := archivePluginRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(dir, tc.root); root != want {
				t.Errorf("root = %q, want %q", root, want)
			}
			want := ""
			if tc.manifest != "" {
				want = filepath.Join(dir, filepath.FromSlash(tc.manifest))
			}
			if manifest != want {
				t.Errorf("manifest = %q, want %q", manifest, want)
			}
		})
	}
}

func TestArchiveSkillsFollowThePluginManifest(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		want    []string
		wantErr bool
		errHas  string // a part of the error the case expects
	}{
		{
			name: "no skills field: the folders under skills/ only",
			files: map[string]string{
				".claude-plugin/plugin.json":         `{"name":"ru-text"}`,
				"skills/ru-text/SKILL.md":            "",
				"tools/testdata/corpus/SKILL.md":     "",
				"SKILL.md":                           "---\nname: at-the-top\ndescription: d\n---\n",
				"skills/not-a-skill/README.md":       "",
				"skills/ru-text/references/guide.md": "",
			},
			want: []string{"ru-text"},
		},
		{
			name: "a declared list",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":["./.claude/skills/a"]}`,
				".claude/skills/a/SKILL.md":  "",
				"skills/b/SKILL.md":          "",
			},
			want: []string{"a"},
		},
		{
			name: "a declared string naming a folder of skills",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":"./custom"}`,
				"custom/a/SKILL.md":          "",
				"custom/b/SKILL.md":          "",
				"skills/c/SKILL.md":          "",
			},
			want: []string{"a", "b"},
		},
		{
			name: "a declared string naming one skill folder",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":"one"}`,
				"one/SKILL.md":               "",
				"one/nested/SKILL.md":        "",
			},
			want: []string{"one"},
		},
		{
			name: "paths that leave the root are dropped",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":["../outside","/etc","C:/x","./skills/a"]}`,
				"skills/a/SKILL.md":          "",
			},
			want: []string{"a"},
		},
		{
			name: "a Codex manifest",
			files: map[string]string{
				".codex-plugin/plugin.json": `{"name":"p","skills":["./s/a"]}`,
				"s/a/SKILL.md":              "",
				"skills/b/SKILL.md":         "",
			},
			want: []string{"a"},
		},
		{
			name: "an empty skills string counts as absent",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":""}`,
				"skills/a/SKILL.md":          "",
			},
			want: []string{"a"},
		},
		{
			name: "a null skills field counts as absent",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":null}`,
				"skills/a/SKILL.md":          "",
			},
			want: []string{"a"},
		},
		{
			name: "no manifest: every SKILL.md",
			files: map[string]string{
				"skills/a/SKILL.md":         "",
				"tools/testdata/x/SKILL.md": "",
			},
			want: []string{"a", "x"},
		},
		{
			name: "the root declared as the skill folder",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":"."}`,
				"SKILL.md":                   "---\nname: at-the-top\ndescription: d\n---\n",
				"skills/b/SKILL.md":          "",
			},
			want: []string{"at-the-top"},
		},
		{
			name: "a folder declared twice, alone and inside its container",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":["skills","./skills/a","skills/a/"]}`,
				"skills/a/SKILL.md":          "",
			},
			want: []string{"a"},
		},
		{
			name: "two declared folders giving one skill name",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":["one","two"]}`,
				"one/SKILL.md":               "---\nname: same\ndescription: d\n---\n",
				"two/SKILL.md":               "---\nname: same\ndescription: d\n---\n",
			},
			wantErr: true,
		},
		{
			name: "a skills field of another type",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":5}`,
				"skills/a/SKILL.md":          "",
			},
			wantErr: true,
		},
		{
			name: "a manifest that is not JSON",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":`,
				"skills/a/SKILL.md":          "",
			},
			wantErr: true,
		},
		{
			name: "declared folders without a SKILL.md",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":["./nothing","./skills/a/SKILL.md"]}`,
				"nothing/README.md":          "",
				"skills/a/SKILL.md":          "",
			},
			wantErr: true,
		},
		{
			name: "no skills/ and no skills field: the root SKILL.md",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p"}`,
				"SKILL.md":                   "---\nname: at-the-top\ndescription: d\n---\n",
			},
			want: []string{"at-the-top"},
		},
		{
			// Shaped like EvilFreelancer/logika, rpa-init and rpa-gen-rules.
			name: "a plugin that is one skill, its command and references beside it",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"logika","version":"2.0.1"}`,
				".codex-plugin/plugin.json":  `{"name":"logika","skills":"./."}`,
				"SKILL.md":                   "---\nname: logika\ndescription: d\n---\n",
				"commands/review.md":         "Review the argument.",
				"references/concepts.md":     "",
				"docs/konspekt.md":           "",
			},
			want: []string{"logika"},
		},
		{
			name: "a root SKILL.md without a name takes the plugin's",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"the-plugin"}`,
				"SKILL.md":                   "---\ndescription: d\n---\n",
			},
			want: []string{"the-plugin"},
		},
		{
			name: "a root SKILL.md without a name takes the name of a Codex manifest",
			files: map[string]string{
				".codex-plugin/plugin.json": `{"name":"cx"}`,
				"SKILL.md":                  "---\ndescription: d\n---\n",
			},
			want: []string{"cx"},
		},
		{
			name: "skills/ with no skill folder in it falls through to the root",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p"}`,
				"skills/flat.md":             "",
				"SKILL.md":                   "---\nname: at-the-top\ndescription: d\n---\n",
			},
			want: []string{"at-the-top"},
		},
		{
			name: "a declared skills field is not topped up from the root",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"p","skills":["./nothing"]}`,
				"nothing/README.md":          "",
				"SKILL.md":                   "---\nname: at-the-top\ndescription: d\n---\n",
			},
			wantErr: true,
		},
		{
			// Shaped like stitch-design-md: a flat skills/<name>.md is no skill.
			name: "no skill folder under skills/ and no root SKILL.md",
			files: map[string]string{
				".claude-plugin/plugin.json": `{"name":"stitch-design-md"}`,
				"skills/create-design-md.md": "",
				"commands/create.md":         "",
			},
			wantErr: true,
			errHas:  "no SKILL.md in skills/<name>/ or at the plugin root",
		},
		{
			name:    "no manifest and no SKILL.md",
			files:   map[string]string{"README.md": ""},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tc.files)
			root, manifest, err := archivePluginRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			hits, err := archiveSkills(root, manifest)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("archiveSkills = %+v, want an error", hits)
				}
				if tc.errHas != "" && !strings.Contains(err.Error(), tc.errHas) {
					t.Errorf("error %q does not say %q", err, tc.errHas)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hits {
				got = append(got, h.name)
				if rel, err := filepath.Rel(dir, h.dir); err != nil || strings.HasPrefix(rel, "..") {
					t.Errorf("skill %q comes from %q, outside the plugin root", h.name, h.dir)
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("skills = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDownloadArchiveCaps(t *testing.T) {
	lim := archiveLimits{download: 1000, unpacked: 1 << 20, entries: 10}
	cases := map[string]http.HandlerFunc{
		"declared length over the cap": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1001")
			_, _ = w.Write(bytes.Repeat([]byte("z"), 1001))
		},
		"streamed body over the cap": func(w http.ResponseWriter, _ *http.Request) {
			for i := 0; i < 11; i++ {
				_, _ = w.Write(bytes.Repeat([]byte("z"), 100))
				w.(http.Flusher).Flush()
			}
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewTLSServer(h)
			t.Cleanup(srv.Close)
			reachRemote(t, srv)
			_, _, err := downloadArchive(context.Background(), srv.URL+"/demo.zip", "", t.TempDir(), lim)
			if !errors.Is(err, errArchiveTooLarge) {
				t.Fatalf("downloadArchive = %v, want errArchiveTooLarge", err)
			}
		})
	}
}

func TestInstallArchivePluginRefusesADownloadOverTheCap(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(maxArchiveBytes+1))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	reachRemote(t, srv)
	managed, lock, res, err := installArchive(t, archivePlugin(srv.URL+"/demo.zip", ""))
	if !errors.Is(err, errArchiveTooLarge) {
		t.Fatalf("install = %v, want errArchiveTooLarge", err)
	}
	assertNothingInstalled(t, managed, lock, res)
}

func TestInstallArchivePluginRefusesAnArchiveOverTheEntryCap(t *testing.T) {
	entries := []zipEntry{{name: "skills/demo/SKILL.md", body: demoSkill}}
	for i := 0; len(entries) <= maxArchiveEntries; i++ {
		entries = append(entries, zipEntry{name: fmt.Sprintf("skills/demo/pad/%05d.txt", i)})
	}
	srv := httptest.NewTLSServer(serveBytes(mustZip(t, entries...), nil))
	t.Cleanup(srv.Close)
	reachRemote(t, srv)
	managed, lock, res, err := installArchive(t, archivePlugin(srv.URL+"/demo.zip", ""))
	if !errors.Is(err, errArchiveTooLarge) {
		t.Fatalf("install = %v, want errArchiveTooLarge", err)
	}
	assertNothingInstalled(t, managed, lock, res)
}

func TestInstallArchivePluginChecksTheDeclaredSHA256(t *testing.T) {
	archive := demoArchive(t)
	srv := httptest.NewTLSServer(serveBytes(archive, nil))
	t.Cleanup(srv.Close)
	reachRemote(t, srv)
	archiveURL := srv.URL + "/demo.zip"

	t.Run("a different digest cancels the install", func(t *testing.T) {
		declared := sha256Hex([]byte("another archive"))
		managed, lock, res, err := installArchive(t, archivePlugin(archiveURL, declared))
		if !errors.Is(err, errArchiveSHA256) {
			t.Fatalf("install = %v, want errArchiveSHA256", err)
		}
		if !strings.Contains(err.Error(), declared) || !strings.Contains(err.Error(), sha256Hex(archive)) {
			t.Errorf("error %q does not name both digests", err)
		}
		assertNothingInstalled(t, managed, lock, res)
	})
	t.Run("a digest that is not 64 hex digits cancels the install", func(t *testing.T) {
		for _, declared := range []string{"abc", strings.Repeat("g", 64), sha256Hex(archive) + "00"} {
			managed, lock, res, err := installArchive(t, archivePlugin(archiveURL, declared))
			if !errors.Is(err, errArchiveSHA256) {
				t.Fatalf("install with sha256 %q = %v, want errArchiveSHA256", declared, err)
			}
			assertNothingInstalled(t, managed, lock, res)
		}
	})
	t.Run("the matching digest installs, in any case", func(t *testing.T) {
		managed, lock, _, err := installArchive(t, archivePlugin(archiveURL, strings.ToUpper(sha256Hex(archive))))
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := os.Stat(filepath.Join(managed, "demo", "SKILL.md")); err != nil {
			t.Fatalf("demo not installed: %v", err)
		}
		if got, want := lock["demo"].Version, "sha256:"+sha256Hex(archive)[:12]; got != want {
			t.Errorf("lock version = %q, want %q", got, want)
		}
	})
}

func TestInstallArchivePluginOnlyOverHTTPSToAllowedAddresses(t *testing.T) {
	// The real guard: nothing here is ever contacted.
	for _, raw := range []string{
		"http://plugins.example/demo.zip",
		"HTTP://plugins.example/demo.zip",
		"ftp://plugins.example/demo.zip",
		"file:///tmp/demo.zip",
		"plugins.example/demo.zip",
	} {
		managed, lock, res, err := installArchive(t, archivePlugin(raw, ""))
		if !errors.Is(err, errArchiveNotHTTPS) {
			t.Errorf("install from %q = %v, want errArchiveNotHTTPS", raw, err)
		}
		assertNothingInstalled(t, managed, lock, res)
	}
	for _, raw := range []string{
		"https://127.0.0.1:9/demo.zip",
		"https://localhost/demo.zip",
		"https://[::1]/demo.zip",
		"https://169.254.169.254/latest/meta-data/demo.zip",
		"https://10.0.0.7/demo.zip",
	} {
		managed, lock, res, err := installArchive(t, archivePlugin(raw, ""))
		if !errors.Is(err, web.ErrDisallowedURL) {
			t.Errorf("install from %q = %v, want the guard's refusal", raw, err)
		}
		assertNothingInstalled(t, managed, lock, res)
	}
	if _, _, _, err := installArchive(t, archivePlugin("", "")); err == nil {
		t.Error("install with an empty archive url succeeded")
	}
}

func TestInstallArchivePluginHoldsRedirectsToTheSameRules(t *testing.T) {
	archive := demoArchive(t)
	redirectTo := func(target string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		})
	}

	t.Run("to an address the guard refuses", func(t *testing.T) {
		var hits atomic.Int32
		private := httptest.NewTLSServer(serveBytes(archive, &hits))
		t.Cleanup(private.Close)
		public := httptest.NewTLSServer(redirectTo(private.URL + "/demo.zip"))
		t.Cleanup(public.Close)
		reachRemote(t, public)
		managed, lock, res, err := installArchive(t, archivePlugin(public.URL+"/demo.zip", ""))
		if !errors.Is(err, web.ErrDisallowedURL) {
			t.Fatalf("install = %v, want the guard's refusal", err)
		}
		if hits.Load() != 0 {
			t.Error("the redirect reached the refused address")
		}
		assertNothingInstalled(t, managed, lock, res)
	})
	t.Run("to plain http", func(t *testing.T) {
		var hits atomic.Int32
		plain := httptest.NewServer(serveBytes(archive, &hits))
		t.Cleanup(plain.Close)
		public := httptest.NewTLSServer(redirectTo(plain.URL + "/demo.zip"))
		t.Cleanup(public.Close)
		reachRemote(t, public, plain)
		managed, lock, res, err := installArchive(t, archivePlugin(public.URL+"/demo.zip", ""))
		if !errors.Is(err, errArchiveNotHTTPS) {
			t.Fatalf("install = %v, want errArchiveNotHTTPS", err)
		}
		if hits.Load() != 0 {
			t.Error("the redirect reached the plain http address")
		}
		assertNothingInstalled(t, managed, lock, res)
	})
	t.Run("to https on an allowed address", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle("/old.zip", redirectTo("/demo.zip"))
		mux.Handle("/demo.zip", serveBytes(archive, nil))
		srv := httptest.NewTLSServer(mux)
		t.Cleanup(srv.Close)
		reachRemote(t, srv)
		managed, lock, _, err := installArchive(t, archivePlugin(srv.URL+"/old.zip", ""))
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := os.Stat(filepath.Join(managed, "demo", "SKILL.md")); err != nil {
			t.Fatalf("demo not installed: %v", err)
		}
		if got := lock["demo"].Archive; got != srv.URL+"/old.zip" {
			t.Errorf("lock archive = %q, want the address of the entry", got)
		}
	})
}

func TestInstallArchivePluginVersion(t *testing.T) {
	// The skill declares its own version; the lock still takes the plugin
	// entry's, else the archive digest, so the update check can compare it
	// with what the marketplace advertises.
	archive := mustZip(t,
		zipEntry{name: ".claude-plugin/plugin.json", body: `{"name":"demo"}`},
		zipEntry{name: "skills/demo/SKILL.md", body: "---\nname: demo\nversion: 9.9.9\ndescription: d\n---\n"},
	)
	srv := httptest.NewTLSServer(serveBytes(archive, nil))
	t.Cleanup(srv.Close)
	reachRemote(t, srv)

	p := archivePlugin(srv.URL+"/demo.zip", "")
	p.Version = "1.4.0"
	_, lock, _, err := installArchive(t, p)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := lock["demo"].Version; got != "1.4.0" {
		t.Errorf("with a declared version the lock has %q, want 1.4.0", got)
	}

	_, lock, _, err = installArchive(t, archivePlugin(srv.URL+"/demo.zip", ""))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got, want := lock["demo"].Version, "sha256:"+sha256Hex(archive)[:12]; got != want {
		t.Errorf("without a declared version the lock has %q, want %q", got, want)
	}
}

func TestInstallArchivePluginInstallsWhatItsManifestDeclares(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipEntry
		want    []string
	}{
		{
			// An author's plugin packed as it is: a SKILL.md in its test data
			// is not one of its skills.
			name: "a manifest without a skills field",
			entries: []zipEntry{
				{name: "ru-text-main/.claude-plugin/plugin.json", body: `{"name":"ru-text"}`},
				{name: "ru-text-main/skills/ru-text/SKILL.md", body: "---\nname: ru-text\ndescription: d\n---\n"},
				{name: "ru-text-main/tools/testdata/corpus/SKILL.md", body: "---\nname: corpus\ndescription: test data\n---\n"},
			},
			want: []string{"ru-text"},
		},
		{
			name: "a manifest that declares its skills",
			entries: []zipEntry{
				{name: ".claude-plugin/plugin.json", body: `{"name":"p","skills":["./.claude/skills/a"]}`},
				{name: ".claude/skills/a/SKILL.md", body: "---\nname: a\ndescription: d\n---\n"},
				{name: "skills/b/SKILL.md", body: "---\nname: b\ndescription: d\n---\n"},
			},
			want: []string{"a"},
		},
		{
			name: "a plugin that is one skill at its root, wrapped in a folder",
			entries: []zipEntry{
				{name: "logika-main/.claude-plugin/plugin.json", body: `{"name":"logika"}`},
				{name: "logika-main/SKILL.md", body: "---\nname: logika\ndescription: d\n---\n"},
				{name: "logika-main/references/concepts.md", body: "concepts"},
				{name: "logika-main/commands/review.md", body: "review"},
			},
			want: []string{"logika"},
		},
		{
			name: "no manifest: every SKILL.md found",
			entries: []zipEntry{
				{name: "skills/demo/SKILL.md", body: demoSkill},
				{name: "unrelated/skills/extra/SKILL.md", body: "---\nname: extra\ndescription: e\n---\n"},
				{name: "README.md", body: "no manifest"},
			},
			want: []string{"demo", "extra"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(serveBytes(mustZip(t, tc.entries...), nil))
			t.Cleanup(srv.Close)
			reachRemote(t, srv)
			managed, lock, res, err := installArchive(t, archivePlugin(srv.URL+"/p.zip", ""))
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if strings.Join(res.Added, ",") != strings.Join(tc.want, ",") {
				t.Errorf("added %v, want %v", res.Added, tc.want)
			}
			entries, err := os.ReadDir(managed)
			if err != nil {
				t.Fatal(err)
			}
			var onDisk []string
			for _, e := range entries {
				onDisk = append(onDisk, e.Name())
			}
			if strings.Join(onDisk, ",") != strings.Join(tc.want, ",") {
				t.Errorf("managed dir holds %v, want %v", onDisk, tc.want)
			}
			if len(lock) != len(tc.want) {
				t.Errorf("lock = %+v, want entries for %v", lock, tc.want)
			}
		})
	}
}

func TestInstallArchivePluginWithoutSkillsFails(t *testing.T) {
	empty := httptest.NewTLSServer(serveBytes(mustZip(t, zipEntry{name: ".claude-plugin/plugin.json", body: `{"name":"demo"}`}), nil))
	t.Cleanup(empty.Close)
	reachRemote(t, empty)
	managed, lock, res, err := installArchive(t, archivePlugin(empty.URL+"/demo.zip", ""))
	if err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Fatalf("install = %v, want no SKILL.md found", err)
	}
	assertNothingInstalled(t, managed, lock, res)

	broken := httptest.NewTLSServer(serveBytes([]byte("this is not a zip"), nil))
	t.Cleanup(broken.Close)
	reachRemote(t, broken)
	managed, lock, res, err = installArchive(t, archivePlugin(broken.URL+"/demo.zip", ""))
	if err == nil {
		t.Fatal("install of a file that is not a zip succeeded")
	}
	assertNothingInstalled(t, managed, lock, res)

	missing := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)
	reachRemote(t, missing)
	if _, _, _, err := installArchive(t, archivePlugin(missing.URL+"/demo.zip", "")); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("install from a 404 = %v, want the status in the error", err)
	}
}

func TestUpdateDetectionComparesArchiveDigestsByEquality(t *testing.T) {
	low, high := "sha256:0000000000aa", "sha256:ffffffffff00"
	cases := []struct {
		latest, installed string
		want              bool
	}{
		{high, low, true},
		{low, high, true}, // a digest has no order: a lower one is still new
		{low, low, false},
		{"1.2.0", low, true}, // the marketplace started declaring versions
		{low, "1.2.0", true}, // or stopped
		{"2.0.0", "1.0.0", true},
		{"1.0.0", "2.0.0", false},
		// Only the exact form archiveVersion makes is a digest: a declared
		// version that merely starts with "sha256:" keeps its order.
		{"sha256:1", "sha256:2", false},
		{"sha256:0000000000AA", "sha256:0000000000aa", true},
	}
	for _, tc := range cases {
		if got := isUpdate(tc.latest, tc.installed); got != tc.want {
			t.Errorf("isUpdate(%q, %q) = %v, want %v", tc.latest, tc.installed, got, tc.want)
		}
	}

	sum := sha256Hex([]byte("archive"))
	got := marketplaceVersions(&Marketplace{Plugins: []MarketplacePlugin{
		{Name: "declared", Version: "1.0.0", Source: PluginSource{Kind: "archive", URL: "https://x/a.zip", SHA256: sum}},
		{Name: "digest", Source: PluginSource{Kind: "archive", URL: "https://x/b.zip", SHA256: strings.ToUpper(sum)}},
		{Name: "unknown", Source: PluginSource{Kind: "archive", URL: "https://x/c.zip"}},
		{Name: "malformed", Source: PluginSource{Kind: "archive", URL: "https://x/d.zip", SHA256: "abc"}},
		{Name: "git", Source: PluginSource{Kind: "url", URL: "https://x/e.git", SHA256: sum}},
	}})
	want := map[string]string{"declared": "1.0.0", "digest": "sha256:" + sum[:12]}
	if len(got) != len(want) || got["declared"] != want["declared"] || got["digest"] != want["digest"] {
		t.Errorf("marketplaceVersions = %v, want %v", got, want)
	}
}

func TestIsArchiveVersionTakesOnlyTheGeneratedForm(t *testing.T) {
	for v, want := range map[string]bool{
		"sha256:0123456789ab":  true,
		"sha256:0123456789AB":  false,
		"sha256:0123456789":    false,
		"sha256:0123456789abc": false,
		"sha256:0123456789ag":  false,
		"sha256:":              false,
		"sha256:2":             false,
		"1.0.0":                false,
		"":                     false,
	} {
		if got := isArchiveVersion(v); got != want {
			t.Errorf("isArchiveVersion(%q) = %v, want %v", v, got, want)
		}
	}
	if v := archiveVersion(sha256Hex([]byte("x"))); !isArchiveVersion(v) {
		t.Errorf("archiveVersion made %q, which isArchiveVersion does not take", v)
	}
}

func TestExtractArchiveDoesNotCountMacMetadata(t *testing.T) {
	// __MACOSX is checked like any entry but never unpacked, so its size does
	// not count against the cap.
	lim := archiveLimits{download: 1 << 20, unpacked: 1000, entries: 10}
	root := t.TempDir()
	zipPath := filepath.Join(root, "plugin.zip")
	data := mustZip(t,
		zipEntry{name: ".claude-plugin/plugin.json", body: `{"name":"demo"}`},
		zipEntry{name: "skills/demo/SKILL.md", body: demoSkill},
		zipEntry{name: "__MACOSX/skills/demo/._SKILL.md", body: strings.Repeat("r", 5000)},
	)
	if err := os.WriteFile(zipPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "unpack")
	if err := extractArchive(zipPath, dest, lim); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "__MACOSX")); !os.IsNotExist(err) {
		t.Errorf("__MACOSX was unpacked: %v", err)
	}
	unsafe := mustZip(t, zipEntry{name: "__MACOSX/../../evil", body: "x"})
	if err := os.WriteFile(zipPath, unsafe, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(zipPath, filepath.Join(root, "unpack2"), lim); !errors.Is(err, errUnsafeArchive) {
		t.Errorf("an unsafe entry under __MACOSX: %v, want errUnsafeArchive", err)
	}
}

func TestUnpackArchiveFileStopsAtTheBudget(t *testing.T) {
	body := strings.Repeat("x", 1001)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, body)
	// An entry whose CRC is wrong: the reader says so only at its end.
	fh := &zip.FileHeader{Name: "corrupt.txt", Method: zip.Store, CRC32: 1, CompressedSize64: 5, UncompressedSize64: 5}
	rw, err := zw.CreateRaw(fh)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(rw, "hello")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	n, err := unpackArchiveFile(zr.File[0], filepath.Join(dir, "over"), 1000)
	if !errors.Is(err, errArchiveTooLarge) {
		t.Fatalf("one byte over the budget: n=%d err=%v, want errArchiveTooLarge", n, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "over")); err != nil || fi.Size() > 1000 {
		t.Errorf("wrote %v bytes (err %v), want no more than the budget", fi.Size(), err)
	}
	if n, err := unpackArchiveFile(zr.File[0], filepath.Join(dir, "exact"), 1001); err != nil || n != 1001 {
		t.Errorf("exactly the budget: n=%d err=%v", n, err)
	}
	if _, err := unpackArchiveFile(zr.File[1], filepath.Join(dir, "corrupt-exact"), 5); !errors.Is(err, zip.ErrChecksum) {
		t.Errorf("a bad CRC with the budget spent: %v, want zip.ErrChecksum", err)
	}
	if _, err := unpackArchiveFile(zr.File[1], filepath.Join(dir, "corrupt-room"), 100); !errors.Is(err, zip.ErrChecksum) {
		t.Errorf("a bad CRC within the budget: %v, want zip.ErrChecksum", err)
	}
}

func TestCountZipHeaderSignatures(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	count := func(path string, stopAt int) int {
		n, err := countZipHeaderSignatures(path, stopAt)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	archive := mustZip(t, zipEntry{name: "a.txt", body: "a"}, zipEntry{name: "b.txt", body: "b"}, zipEntry{name: "c/", mode: fs.ModeDir | 0o755})
	if got := count(write("three.zip", archive), 100); got != 3 {
		t.Errorf("a zip of three entries: %d signatures", got)
	}
	if got := count(write("three-stop.zip", archive), 2); got != 2 {
		t.Errorf("stopAt 2: counted %d", got)
	}

	// A signature split across two reads of the scanner's buffer still counts.
	split := bytes.Repeat([]byte{0}, 64<<10-2)
	split = append(split, zipHeaderSignature...)
	split = append(split, bytes.Repeat([]byte("PK"), 3)...)
	if got := count(write("split.bin", split), 100); got != 1 {
		t.Errorf("a signature across the buffer boundary: %d", got)
	}

	// A zip stored uncompressed inside is entry data, before the directory:
	// its signatures do not count.
	inner := mustZip(t, zipEntry{name: "x.txt", body: "x"}, zipEntry{name: "y.txt", body: "y"})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "template.docx", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(inner)
	_ = zw.Close()
	if got := count(write("nested.zip", buf.Bytes()), 100); got != 1 {
		t.Errorf("one entry holding a stored zip of two: %d signatures, want 1", got)
	}
}

func TestExtractArchiveCountsNoSignaturesInEntryData(t *testing.T) {
	// Twenty header signatures inside a stored file, a cap of ten entries: the
	// archive has two entries and unpacks.
	payload := strings.Repeat("PK\x01\x02", 20)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "skills/demo/data.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, payload)
	w, err = zw.Create("skills/demo/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, demoSkill)
	_ = zw.Close()
	root := t.TempDir()
	zipPath := filepath.Join(root, "plugin.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	lim := archiveLimits{download: 1 << 20, unpacked: 1 << 20, entries: 10}
	if err := extractArchive(zipPath, filepath.Join(root, "unpack"), lim); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
}

func TestDownloadArchiveFollowsAtMostFiveRedirects(t *testing.T) {
	archive := demoArchive(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
		if err != nil || hops == 0 {
			_, _ = w.Write(archive)
			return
		}
		// A path on this test server, built from a parsed integer.
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", hops-1), http.StatusFound) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	}))
	t.Cleanup(srv.Close)
	reachRemote(t, srv)
	lim := defaultArchiveLimits
	if _, _, err := downloadArchive(context.Background(), srv.URL+"/hop/5", "", t.TempDir(), lim); err != nil {
		t.Fatalf("five redirects: %v", err)
	}
	if _, _, err := downloadArchive(context.Background(), srv.URL+"/hop/6", "", t.TempDir(), lim); err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("six redirects: %v, want too many redirects", err)
	}
}

func TestInstallArchivePluginTakesTheSchemeInAnyCase(t *testing.T) {
	srv := httptest.NewTLSServer(serveBytes(demoArchive(t), nil))
	t.Cleanup(srv.Close)
	reachRemote(t, srv)
	upper := "HTTPS://" + strings.TrimPrefix(srv.URL, "https://") + "/demo.zip"
	managed, _, _, err := installArchive(t, archivePlugin(upper, ""))
	if err != nil {
		t.Fatalf("install from %q: %v", upper, err)
	}
	if _, err := os.Stat(filepath.Join(managed, "demo", "SKILL.md")); err != nil {
		t.Fatalf("demo not installed: %v", err)
	}
}
