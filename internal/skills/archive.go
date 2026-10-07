package skills

// A marketplace can publish a plugin as a zip archive instead of a git
// repository, in the shape Claude Code reads:
//
//	{"source": "archive", "url": "https://host/plugins/demo.zip", "sha256": "<64 hex, optional>"}
//
// The archive is fetched over https only, and every address on the way - the
// first one and each redirect - goes through the SSRF guard. A declared sha256
// is checked on every download. The archive is unpacked under the caps below
// into a temporary folder, and one entry that could land outside that folder,
// or that is a link, a device or a pipe rather than a file or a folder, refuses
// the whole archive before anything is written. The plugin root is the top of
// the archive or the one folder that wraps everything. When it carries a plugin
// manifest (.claude-plugin/plugin.json, else .codex-plugin/plugin.json), the
// skills installed are the ones the manifest declares in "skills", or the
// folders under skills/ when it declares none, or the SKILL.md at the plugin
// root when there are none of those either, which is what Claude Code installs
// from a plugin; an archive without a manifest is searched for every SKILL.md,
// the way a cloned repository is. git is never started.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// maxArchiveBytes caps the download of one plugin archive.
	maxArchiveBytes = 50 << 20
	// maxArchiveUnpackedBytes caps what one archive unpacks to, every file together.
	maxArchiveUnpackedBytes = 200 << 20
	// maxArchiveEntries caps the entries (files and folders) of one archive.
	maxArchiveEntries = 10_000
	// archiveDownloadTimeout bounds one archive download, redirects and body included.
	archiveDownloadTimeout = 5 * time.Minute
	// archiveVersionPrefix and archiveVersionDigits make the version recorded
	// for an archive plugin whose entry declares none: "sha256:" and the first
	// twelve hex digits of the archive's digest.
	archiveVersionPrefix = "sha256:"
	archiveVersionDigits = 12
)

// archiveLimits are the caps one archive is held to. Installs pass
// defaultArchiveLimits; tests pass smaller ones.
type archiveLimits struct {
	download int64  // bytes downloaded
	unpacked uint64 // bytes unpacked, every file together
	entries  int    // entries in the archive
}

var defaultArchiveLimits = archiveLimits{
	download: maxArchiveBytes,
	unpacked: maxArchiveUnpackedBytes,
	entries:  maxArchiveEntries,
}

var (
	// errUnsafeArchive refuses an archive with an entry that could land outside
	// the unpack folder or that is neither a file nor a folder.
	errUnsafeArchive = errors.New("unsafe archive entry")
	// errArchiveTooLarge refuses an archive over one of the caps.
	errArchiveTooLarge = errors.New("archive over the size limits")
	// errArchiveSHA256 refuses a declared sha256 that is malformed or that the
	// downloaded bytes do not match.
	errArchiveSHA256 = errors.New("archive sha256 check failed")
	// errArchiveNotHTTPS refuses an archive address, or a redirect, that is not https.
	errArchiveNotHTTPS = errors.New("archive url is not https")
)

// pluginManifestFiles are the plugin manifests a plugin root may carry; the
// first one found is read.
var pluginManifestFiles = []string{
	filepath.Join(".claude-plugin", "plugin.json"),
	filepath.Join(".codex-plugin", "plugin.json"),
}

// installArchivePlugin installs the skills of a plugin published as a zip
// archive. entry already carries the plugin name and the version its
// marketplace entry declares.
func installArchivePlugin(ctx context.Context, p MarketplacePlugin, entry RemoteEntry, managedDir string, lock map[string]RemoteEntry, res *SyncResult) error {
	archiveURL := strings.TrimSpace(p.Source.URL)
	if archiveURL == "" {
		return fmt.Errorf("plugin %q: empty archive url", p.Name)
	}
	wantSHA, err := normalizeSHA256(p.Source.SHA256)
	if err != nil {
		return fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	tmp, cleanup, err := remoteStagingDir(managedDir, "coddy-plugin-archive-")
	if err != nil {
		return err
	}
	defer cleanup()

	zipPath, sum, err := downloadArchive(ctx, archiveURL, wantSHA, tmp, defaultArchiveLimits)
	if err != nil {
		return fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	unpacked := filepath.Join(tmp, "plugin")
	if err := extractArchive(zipPath, unpacked, defaultArchiveLimits); err != nil {
		return fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	root, manifest, err := archivePluginRoot(unpacked)
	if err != nil {
		return fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	hits, err := archiveSkills(root, manifest)
	if err != nil {
		return fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	entry.Archive = archiveURL
	if entry.Version == "" {
		// The digest, which is what a declared sha256 advertises, so the update
		// check compares like with like. A skill's own SKILL.md version is not
		// used here: the check could only see it by downloading the archive, and
		// it need not change when the archive does.
		entry.Version = archiveVersion(sum)
	}
	return installSkillDirs(hits, entry, managedDir, lock, res)
}

// checkArchiveURL admits an archive address: https, and past the SSRF guard.
func checkArchiveURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("archive url: %w", err)
	}
	// url.Parse lower-cases the scheme.
	if u.Scheme != "https" {
		return fmt.Errorf("%w (scheme %q)", errArchiveNotHTTPS, u.Scheme)
	}
	if err := remoteGuard(ctx, u.String()); err != nil {
		return fmt.Errorf("archive url not allowed: %w", err)
	}
	return nil
}

// downloadArchive fetches the archive at rawURL into dir and returns the file
// and the sha256 of its bytes. Only https is fetched, every address goes
// through checkArchiveURL, the body stops at the download cap, and bytes that
// do not match a declared digest (wantSHA, lower-case hex, "" for none) fail
// the download.
func downloadArchive(ctx context.Context, rawURL, wantSHA, dir string, lim archiveLimits) (string, string, error) {
	if err := checkArchiveURL(ctx, rawURL); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/zip, application/octet-stream;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "coddy-agent-skills")
	resp, err := remoteClient(archiveDownloadTimeout, checkArchiveURL).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download archive: %s", resp.Status)
	}
	if resp.ContentLength > lim.download {
		return "", "", fmt.Errorf("%w: the archive is %d bytes, the limit is %d", errArchiveTooLarge, resp.ContentLength, lim.download)
	}

	zipPath := filepath.Join(dir, "plugin.zip")
	f, err := os.OpenFile(zipPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a fixed name inside our own temp dir
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, lim.download+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", fmt.Errorf("download archive: %w", err)
	}
	if n > lim.download {
		return "", "", fmt.Errorf("%w: the archive is over %d bytes", errArchiveTooLarge, lim.download)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && sum != wantSHA {
		return "", "", fmt.Errorf("%w: the marketplace declares %s, the archive is %s", errArchiveSHA256, wantSHA, sum)
	}
	return zipPath, sum, nil
}

// extractArchive unpacks the zip at zipPath into dest, which it creates. Every
// entry is checked before the first one is written: an entry that could land
// outside dest, that is neither a file nor a folder, or that names a file a
// second time refuses the whole archive, as do more entries or more unpacked
// bytes than lim allows. A file
// comes out 0755 when the archive marks it executable for anyone and 0644
// otherwise. The __MACOSX folder the macOS archiver adds is left out.
func extractArchive(zipPath, dest string, lim archiveLimits) error {
	// The zip reader builds every central directory header before anything
	// can look at the count: a 50 MiB archive made of headers alone holds 1.1
	// million of them, a quarter of a gigabyte of heap. Each header it builds
	// starts with the header signature, and lies after where it starts reading
	// the directory, so the signatures from there on bound the entries it can
	// build, and the cap is checked on them first.
	if n, err := countZipHeaderSignatures(zipPath, lim.entries+1); err != nil {
		return fmt.Errorf("read archive: %w", err)
	} else if n > lim.entries {
		return fmt.Errorf("%w: more than %d entries", errArchiveTooLarge, lim.entries)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) > lim.entries {
		return fmt.Errorf("%w: %d entries, the limit is %d", errArchiveTooLarge, len(zr.File), lim.entries)
	}

	type item struct {
		f   *zip.File
		rel string
	}
	plan := make([]item, 0, len(zr.File))
	files := make(map[string]bool, len(zr.File))
	var declared uint64
	for _, f := range zr.File {
		rel, err := archiveEntryPath(f.Name)
		if err != nil {
			return err
		}
		if kind := archiveEntryKind(f.Mode()); kind != "" {
			return fmt.Errorf("%w: %q is %s, not a file or a folder", errUnsafeArchive, f.Name, kind)
		}
		// Checked like the rest, but neither written nor counted.
		if rel == "." || isMacMetadata(rel) {
			continue
		}
		// A file named twice leaves which copy is meant to the unpacker.
		if !f.Mode().IsDir() {
			if files[rel] {
				return fmt.Errorf("%w: %q is in the archive twice", errUnsafeArchive, f.Name)
			}
			files[rel] = true
		}
		if f.UncompressedSize64 > lim.unpacked-declared {
			return fmt.Errorf("%w: it unpacks to more than %d bytes", errArchiveTooLarge, lim.unpacked)
		}
		declared += f.UncompressedSize64
		plan = append(plan, item{f: f, rel: rel})
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var written uint64
	for _, it := range plan {
		target := filepath.Join(dest, it.rel)
		if it.f.Mode().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		// The zip reader already stops at the size the entry declares; the
		// running total is the second line against a header that lies.
		n, err := unpackArchiveFile(it.f, target, lim.unpacked-written)
		if errors.Is(err, errArchiveTooLarge) {
			return fmt.Errorf("%w: it unpacks to more than %d bytes", errArchiveTooLarge, lim.unpacked)
		}
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}

// zipHeaderSignature opens every central directory header of a zip archive.
var zipHeaderSignature = []byte("PK\x01\x02")

// countZipHeaderSignatures counts the central directory header signatures in
// the file at path from where the zip reader can start reading the directory
// (zipDirectoryScanStart), stopping once it reaches stopAt. The signature
// cannot overlap itself, so a match never hides another.
func countZipHeaderSignatures(path string, stopAt int) (int, error) {
	f, err := os.Open(path) //nolint:gosec // the archive in our own temp dir
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	start, err := zipDirectoryScanStart(f, fi.Size())
	if err != nil {
		return 0, err
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	buf := make([]byte, 64<<10)
	carry := 0 // bytes of the signature matched at the end of the last chunk
	count := 0
	for {
		n, err := f.Read(buf)
		for _, b := range buf[:n] {
			switch b {
			case zipHeaderSignature[carry]:
				carry++
				if carry == len(zipHeaderSignature) {
					count++
					carry = 0
					if count >= stopAt {
						return count, nil
					}
				}
			case zipHeaderSignature[0]:
				carry = 1
			default:
				carry = 0
			}
		}
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
	}
}

// zipDirectoryScanStart is the earliest offset the zip reader may start
// reading central directory headers at. The reader takes the last
// end-of-directory record in the file's last 65 KiB, which for an archive
// holding another archive is the outer one, and starts at its directory
// offset, or at the record's position less the directory size; the lower of
// the two is at or before wherever it starts, so the headers it reads all lie
// after it, while the entry data before the directory does not. A zip64
// marker, or no record, starts the count at the top of the file.
func zipDirectoryScanStart(f *os.File, size int64) (int64, error) {
	const recordLen = 22
	tail := int64(65 << 10)
	if tail > size {
		tail = size
	}
	buf := make([]byte, tail)
	if _, err := f.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	for i := len(buf) - recordLen; i >= 0; i-- {
		if buf[i] != 'P' || buf[i+1] != 'K' || buf[i+2] != 0x05 || buf[i+3] != 0x06 {
			continue
		}
		records := binary.LittleEndian.Uint16(buf[i+10:])
		dirSize := binary.LittleEndian.Uint32(buf[i+12:])
		dirOffset := binary.LittleEndian.Uint32(buf[i+16:])
		// The markers the reader takes for a zip64 end record (it compares the
		// directory size with 0xffff, too).
		if records == 0xffff || dirSize == 0xffff || dirSize == 0xffffffff || dirOffset == 0xffffffff {
			return 0, nil
		}
		at := size - tail + int64(i)
		return max(0, min(int64(dirOffset), at-int64(dirSize))), nil
	}
	return 0, nil
}

// unpackArchiveFile writes one file entry to target and returns the bytes
// written. It writes at most budget bytes and reports errArchiveTooLarge when
// the entry holds more.
func unpackArchiveFile(f *zip.File, target string, budget uint64) (uint64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	perm := os.FileMode(0o644)
	if f.Mode().Perm()&0o111 != 0 {
		perm = 0o755
	}
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("archive entry %q: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	// O_EXCL: an archive that names one file twice is refused, not resolved.
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) //nolint:gosec // target was checked by archiveEntryPath
	if err != nil {
		return 0, fmt.Errorf("archive entry %q: %w", f.Name, err)
	}
	n, err := io.CopyN(out, rc, int64(budget))
	switch {
	case errors.Is(err, io.EOF):
		// The entry ended within the budget: the usual case.
		err = nil
	case err == nil:
		// The budget is spent; one more byte means the entry is over it. The
		// end of the entry is where the reader checks its CRC, so an error other
		// than io.EOF from this read counts too.
		var one [1]byte
		m, readErr := io.ReadFull(rc, one[:])
		switch {
		case m > 0:
			err = errArchiveTooLarge
		case readErr != nil && !errors.Is(readErr, io.EOF):
			err = readErr
		}
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return uint64(n), fmt.Errorf("archive entry %q: %w", f.Name, err)
	}
	return uint64(n), nil
}

// archiveEntryPath turns a zip entry name into a path relative to the unpack
// folder, or refuses it when it could land outside: an empty name, a NUL
// byte, an absolute path, a drive letter, a ".." element, or a name the
// platform does not take as local. A backslash counts as a separator, the way
// a zip written on Windows means it.
func archiveEntryPath(name string) (string, error) {
	refuse := func(why string) (string, error) {
		return "", fmt.Errorf("%w: %q %s", errUnsafeArchive, name, why)
	}
	n := strings.ReplaceAll(name, `\`, "/")
	switch {
	case n == "":
		return refuse("has no name")
	case strings.ContainsRune(n, 0):
		return refuse("carries a NUL byte")
	case strings.HasPrefix(n, "/"):
		return refuse("is an absolute path")
	case len(n) >= 2 && n[1] == ':' && isASCIILetter(n[0]):
		return refuse("names a drive")
	}
	for _, el := range strings.Split(n, "/") {
		switch {
		case el == "..":
			return refuse("climbs out with ..")
		case el != "" && el != "." && strings.TrimRight(el, ". ") == "":
			// Windows drops the trailing dots and spaces of a path element,
			// so ".. " or "..." would climb out there all the same.
			return refuse("has an element made of dots and spaces")
		}
	}
	rel := filepath.FromSlash(path.Clean(n))
	if rel != "." && !filepath.IsLocal(rel) {
		return refuse("is not a local path")
	}
	return rel, nil
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// archiveEntryKind names what an entry is when it is neither a regular file
// nor a folder, and returns "" for those two.
func archiveEntryKind(mode fs.FileMode) string {
	t := mode.Type()
	switch {
	case t == 0, t == fs.ModeDir:
		return ""
	case t&fs.ModeSymlink != 0:
		return "a symbolic link"
	case t&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		return "a device"
	case t&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case t&fs.ModeSocket != 0:
		return "a socket"
	default:
		return "an irregular entry"
	}
}

// isMacMetadata reports whether rel lies in the __MACOSX folder the macOS
// archiver adds beside the real content: resource forks, never plugin files.
func isMacMetadata(rel string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return first == "__MACOSX"
}

// archivePluginRoot finds the plugin root in an unpacked archive and its
// manifest: the unpack folder when a manifest is at its top, else the folder
// that wraps everything when it is the only top-level entry (the way GitHub and
// most archivers pack a tree), with the manifest in it if there is one, else
// the unpack folder with no manifest ("").
func archivePluginRoot(dir string) (root, manifest string, err error) {
	if m := pluginManifestPath(dir); m != "" {
		return dir, m, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		wrapped := filepath.Join(dir, entries[0].Name())
		return wrapped, pluginManifestPath(wrapped), nil
	}
	return dir, "", nil
}

// pluginManifestPath returns the manifest of the plugin rooted at dir, or "".
func pluginManifestPath(dir string) string {
	for _, rel := range pluginManifestFiles {
		p := filepath.Join(dir, rel)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// archiveSkills lists the skill folders to install from a plugin root.
//
// With a manifest they are what its "skills" declares - a path or a list of
// paths, relative to the root, each either a skill folder or a folder of skill
// folders - or, when it declares none, the skill folders under skills/, or,
// when there are none of those either, the root itself if it holds a SKILL.md:
// a plugin that is one skill. A path that would leave the root is dropped, as
// an archive entry would be. So a SKILL.md elsewhere in a plugin (test data,
// examples) is never taken for a skill of the plugin.
//
// Without a manifest every SKILL.md under the root counts, the way a cloned
// repository is searched. Finding no skill at all is an error.
func archiveSkills(root, manifest string) ([]skillHit, error) {
	if manifest == "" {
		hits := locateSkillDirs(root)
		if len(hits) == 0 {
			return nil, fmt.Errorf("no SKILL.md found in the archive")
		}
		return hits, nil
	}
	paths, declared, err := manifestSkillPaths(manifest)
	if err != nil {
		return nil, err
	}
	byName := map[string]skillHit{}
	var clash error
	add := func(dir string) {
		name := skillNameForDir(dir, root)
		if name == "" {
			return
		}
		if prev, dup := byName[name]; dup && prev.dir != dir && clash == nil {
			rel := func(p string) string { r, _ := filepath.Rel(root, p); return filepath.ToSlash(r) }
			clash = fmt.Errorf("skill %q comes from two folders %s names, %s and %s", name, manifestName(manifest), rel(prev.dir), rel(dir))
			return
		}
		byName[name] = skillHit{dir: dir, name: name}
	}
	addFolder := func(dir string, itself bool) {
		if itself && hasSkillFile(dir) {
			add(dir)
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if sub := filepath.Join(dir, e.Name()); e.IsDir() && hasSkillFile(sub) {
				add(sub)
			}
		}
	}
	if !declared {
		addFolder(filepath.Join(root, "skills"), false)
		// No skill folder under skills/: a plugin that is one skill keeps it at
		// its root (EvilFreelancer/logika does), and Claude Code installs that.
		if len(byName) == 0 && hasSkillFile(root) {
			if name := rootSkillName(root, manifest); name != "" {
				byName[name] = skillHit{dir: root, name: name}
			}
		}
	}
	for _, p := range paths {
		rel, err := archiveEntryPath(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		if dir := filepath.Join(root, rel); isDir(dir) {
			addFolder(dir, true)
		}
	}
	if clash != nil {
		return nil, clash
	}
	if len(byName) == 0 {
		if declared {
			return nil, fmt.Errorf("no SKILL.md in the skill folders %s names", manifestName(manifest))
		}
		return nil, fmt.Errorf("no SKILL.md in skills/<name>/ or at the plugin root, and %s names no skill folders", manifestName(manifest))
	}
	out := make([]skillHit, 0, len(byName))
	for _, h := range byName {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// rootSkillName names the skill a plugin keeps at its root: the name its
// SKILL.md gives, else the plugin's name from its manifest.
func rootSkillName(root, manifest string) string {
	if name := skillNameForDir(root, root); name != "" {
		return name
	}
	data, err := os.ReadFile(manifest) //nolint:gosec // a manifest inside our own unpack folder
	if err != nil {
		return ""
	}
	var m struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.Name)
}

// manifestSkillPaths reads the "skills" field of a plugin manifest: declared
// is false when the field is absent, null or an empty string, and a value that
// is neither a string nor a list of strings is an error.
func manifestSkillPaths(manifest string) (paths []string, declared bool, err error) {
	data, err := os.ReadFile(manifest) //nolint:gosec // a manifest inside our own unpack folder
	if err != nil {
		return nil, false, err
	}
	var m struct {
		Skills json.RawMessage `json:"skills"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false, fmt.Errorf("plugin manifest %s: %w", manifestName(manifest), err)
	}
	raw := strings.TrimSpace(string(m.Skills))
	if raw == "" || raw == "null" {
		return nil, false, nil
	}
	var one string
	if json.Unmarshal(m.Skills, &one) == nil {
		if strings.TrimSpace(one) == "" {
			// An empty path names nothing: the same as no field.
			return nil, false, nil
		}
		return []string{one}, true, nil
	}
	var many []string
	if json.Unmarshal(m.Skills, &many) == nil {
		return many, true, nil
	}
	return nil, true, fmt.Errorf("plugin manifest %s: skills is neither a path nor a list of paths", manifestName(manifest))
}

// manifestName is a manifest's path as a plugin author writes it.
func manifestName(manifest string) string {
	return filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(manifest)), filepath.Base(manifest)))
}

func hasSkillFile(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && fi.Mode().IsRegular()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// normalizeSHA256 checks a declared digest: "" when none is declared, else 64
// hex digits returned in lower case. Anything else is refused, not ignored: a
// marketplace that meant to pin an archive must not have the pin dropped.
func normalizeSHA256(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	if _, err := hex.DecodeString(s); err != nil || len(s) != 2*sha256.Size {
		return "", fmt.Errorf("%w: %q is not 64 hex digits", errArchiveSHA256, s)
	}
	return s, nil
}

// archiveVersion is the version an archive plugin without a declared one is
// recorded at: a short form of the archive's sha256 (lower-case hex).
func archiveVersion(sum string) string {
	return archiveVersionPrefix + sum[:archiveVersionDigits]
}

// isArchiveVersion reports whether v has the exact form archiveVersion makes:
// the prefix and twelve lower-case hex digits. A declared version that merely
// starts with "sha256:" keeps being compared as a version.
func isArchiveVersion(v string) bool {
	digits, ok := strings.CutPrefix(v, archiveVersionPrefix)
	if !ok || len(digits) != archiveVersionDigits {
		return false
	}
	for _, c := range digits {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
