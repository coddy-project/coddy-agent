package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ArtifactMaxBytes        int64 = 25 << 20
	ArtifactSessionMaxBytes int64 = 100 << 20
)

var ErrArtifactSourceUnavailable = errors.New("artifact source is unavailable")

type Artifact struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	SHA256             string `json:"sha256"`
	Size               int64  `json:"size"`
	CreatedAt          string `json:"createdAt"`
	SourcePath         string `json:"sourcePath"`
	SourceRelativePath string `json:"sourceRelativePath"`
}
type artifactManifest struct {
	Artifacts []Artifact `json:"artifacts"`
}

func ArtifactsPath(sessionDir string) string { return filepath.Join(sessionDir, "artifacts") }
func ArtifactPath(sessionDir, digest string) string {
	return filepath.Join(ArtifactsPath(sessionDir), digest)
}
func ArtifactRoute(sessionID, artifactID string) string {
	return "/coddy/sessions/" + sessionID + "/artifacts/" + artifactID
}
func artifactManifestPath(d string) string { return filepath.Join(ArtifactsPath(d), "manifest.json") }

// CaptureArtifact takes a single descriptor snapshot. The source is never followed through
// symlinks, must remain the file Lstat inspected, and must live below the real workspace.
func CaptureArtifact(sessionDir, cwd, requested string) (Artifact, error) {
	root, err := canonicalDir(cwd)
	if err != nil {
		return Artifact{}, fmt.Errorf("workspace: %w", err)
	}
	sd, err := canonicalSessionDir(sessionDir)
	if err != nil {
		return Artifact{}, err
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	if abs, err := filepath.Abs(candidate); err != nil || within(abs, sd) {
		return Artifact{}, errors.New("shared path is inside the session store")
	}
	if err := noSymlinkPath(root, candidate); err != nil {
		return Artifact{}, err
	}
	before, err := os.Lstat(candidate)
	if err != nil || !before.Mode().IsRegular() {
		return Artifact{}, errors.New("shared path must be a regular non-symlink file")
	}
	if before.Size() > ArtifactMaxBytes {
		return Artifact{}, errors.New("shared file exceeds 25 MiB limit")
	}
	return withArtifactLock(sd, func() (Artifact, error) {
		if err := os.MkdirAll(ArtifactsPath(sd), 0700); err != nil {
			return Artifact{}, err
		}
		cleanupArtifactTemps(sd)
		m, err := readArtifactManifest(sd)
		if err != nil {
			return Artifact{}, err
		}
		var total int64
		for _, a := range m.Artifacts {
			total += a.Size
		}
		f, err := os.Open(candidate)
		if err != nil {
			return Artifact{}, err
		}
		defer func() { _ = f.Close() }()
		after, err := f.Stat()
		if err != nil || !sameFile(before, after) {
			return Artifact{}, errors.New("shared file changed while opening")
		}
		tmp, err := os.CreateTemp(ArtifactsPath(sd), ".artifact-")
		if err != nil {
			return Artifact{}, err
		}
		tmpName := tmp.Name()
		defer func() { _ = os.Remove(tmpName) }()
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(f, ArtifactMaxBytes+1))
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil || n > ArtifactMaxBytes {
			return Artifact{}, errors.New("copy shared file failed")
		}
		if again, err := f.Stat(); err != nil || !sameFile(after, again) {
			return Artifact{}, errors.New("shared file changed while copying")
		}
		d := hex.EncodeToString(h.Sum(nil))
		for _, a := range m.Artifacts {
			if a.SHA256 == d {
				return a, nil
			}
		}
		if total+n > ArtifactSessionMaxBytes {
			return Artifact{}, errors.New("session artifact quota exceeds 100 MiB")
		}
		dest := ArtifactPath(sd, d)
		if err := os.Chmod(tmpName, 0400); err != nil {
			return Artifact{}, err
		}
		if err := os.Rename(tmpName, dest); err != nil {
			return Artifact{}, err
		}
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return Artifact{}, errors.New("shared path escapes workspace")
		}
		a := Artifact{ID: d[:24], Name: filepath.Base(candidate), SHA256: d, Size: n, CreatedAt: time.Now().UTC().Format(time.RFC3339), SourcePath: candidate, SourceRelativePath: rel}
		m.Artifacts = append(m.Artifacts, a)
		if err := writeArtifactManifest(sd, m); err != nil {
			_ = os.Remove(dest)
			return Artifact{}, err
		}
		return a, nil
	})
}
func canonicalDir(p string) (string, error) {
	p, err := filepath.Abs(strings.TrimSpace(p))
	if err != nil {
		return "", err
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	i, err := os.Stat(r)
	if err != nil || !i.IsDir() {
		return "", errors.New("not a directory")
	}
	return r, nil
}
func canonicalSessionDir(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("sharing files requires a persisted session")
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(p, 0700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
func within(p, root string) bool {
	r, e := filepath.Rel(root, p)
	return e == nil && (r == "." || (r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))))
}
func noSymlinkPath(root, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil || !within(abs, root) {
		return errors.New("shared path escapes workspace")
	}
	rel, _ := filepath.Rel(root, abs)
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		i, e := os.Lstat(cur)
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return errors.New("shared path contains a symlink")
		}
	}
	return nil
}
func sameFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode().IsRegular() && b.Mode().IsRegular()
}
func withArtifactLock(sd string, fn func() (Artifact, error)) (Artifact, error) {
	d := ArtifactsPath(sd)
	_ = os.MkdirAll(d, 0700)
	lock := filepath.Join(d, ".lock")
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			_ = f.Close()
			defer func() { _ = os.Remove(lock) }()
			return fn()
		}
		if os.IsExist(e) {
			if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > time.Minute {
				_ = os.Remove(lock)
				continue
			}
		}
		if !os.IsExist(e) || time.Now().After(deadline) {
			return Artifact{}, fmt.Errorf("artifact store lock: %w", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func cleanupArtifactTemps(sd string) {
	es, _ := os.ReadDir(ArtifactsPath(sd))
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".artifact-") || strings.HasPrefix(e.Name(), ".manifest-") {
			_ = os.Remove(filepath.Join(ArtifactsPath(sd), e.Name()))
		}
	}
}
func readArtifactManifest(d string) (artifactManifest, error) {
	b, e := os.ReadFile(artifactManifestPath(d))
	if os.IsNotExist(e) {
		return artifactManifest{}, nil
	}
	if e != nil {
		return artifactManifest{}, e
	}
	var m artifactManifest
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	return m, nil
}
func writeArtifactManifest(d string, m artifactManifest) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(ArtifactsPath(d), ".manifest-")
	if e != nil {
		return e
	}
	n := f.Name()
	defer func() { _ = os.Remove(n) }()
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if x := f.Close(); e == nil {
		e = x
	}
	if e != nil {
		return e
	}
	return os.Rename(n, artifactManifestPath(d))
}
func ReadArtifact(sd, id string) (Artifact, string, error) {
	m, err := readArtifactManifest(sd)
	if err != nil {
		return Artifact{}, "", err
	}
	for _, a := range m.Artifacts {
		if a.ID != id {
			continue
		}
		p := ArtifactPath(sd, a.SHA256)
		i, statErr := os.Lstat(p)
		if statErr != nil || !i.Mode().IsRegular() {
			return Artifact{}, "", os.ErrNotExist
		}
		return a, p, nil
	}
	return Artifact{}, "", os.ErrNotExist
}

// ArtifactSourcePath returns the original workspace path recorded while an
// artifact was verified. Both persisted path forms must still agree with cwd;
// callers receive no path supplied by an HTTP client.
func ArtifactSourcePath(sessionDir, cwd, id string) (string, error) {
	m, err := readArtifactManifest(sessionDir)
	if err != nil {
		return "", err
	}
	for _, artifact := range m.Artifacts {
		if artifact.ID != id {
			continue
		}
		root, err := canonicalDir(cwd)
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(artifact.SourcePath)
		rel := strings.TrimSpace(artifact.SourceRelativePath)
		if path == "" || rel == "" || !filepath.IsAbs(path) || filepath.IsAbs(rel) {
			return "", os.ErrNotExist
		}
		want, err := filepath.Rel(root, path)
		if err != nil || want != rel || want == "." || strings.HasPrefix(want, ".."+string(filepath.Separator)) {
			return "", os.ErrNotExist
		}
		if err := noSymlinkPath(root, path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", ErrArtifactSourceUnavailable
			}
			return "", os.ErrNotExist
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", ErrArtifactSourceUnavailable
		}
		return path, nil
	}
	return "", os.ErrNotExist
}
