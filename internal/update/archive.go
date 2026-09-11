package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func installFromArchive(data []byte, archiveName, destPath string) error {
	body, err := executableFromArchive(data, archiveName)
	if err != nil {
		return err
	}
	return writeExecutable(destPath, body)
}

func executableFromArchive(data []byte, archiveName string) ([]byte, error) {
	lower := strings.ToLower(archiveName)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"):
		return executableFromTarGz(data, BinaryName(runtimeGOOSFromArchive(archiveName)))
	case strings.HasSuffix(lower, ".zip"):
		return executableFromZip(data, "coddy.exe")
	default:
		return nil, fmt.Errorf("unsupported archive %q", archiveName)
	}
}

func runtimeGOOSFromArchive(name string) string {
	if strings.Contains(name, "_windows_") {
		return "windows"
	}
	return "linux"
}

func executableFromTarGz(data []byte, binName string) ([]byte, error) {
	files, err := filesFromTarGz(data, map[string]bool{binName: true})
	if err != nil {
		return nil, err
	}
	body, ok := files[binName]
	if !ok {
		return nil, fmt.Errorf("archive missing %q", binName)
	}
	return body, nil
}

// filesFromTarGz reads the regular members whose base name is in want, keyed
// by that name, and stops once every wanted name has been seen. A name the
// archive does not carry is absent from the result rather than an error: the
// caller decides whether it needed it.
func filesFromTarGz(data []byte, want map[string]bool) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	found := make(map[string][]byte, len(want))
	for len(found) < len(want) {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if _, seen := found[base]; !want[base] || seen {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, 128<<20))
		if err != nil {
			return nil, err
		}
		found[base] = body
	}
	return found, nil
}

func executableFromZip(data []byte, binName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != binName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, 128<<20))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	return nil, fmt.Errorf("archive missing %q", binName)
}

func writeExecutable(dest string, body []byte) error {
	return writeFile(dest, body, 0o755)
}

// writeFile replaces dest with body atomically: staged beside it, then renamed
// over it, so a reader never sees a half-written file.
func writeFile(dest string, body []byte, mode os.FileMode) error {
	tmpName, err := stageFile(dest, body, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func stageExecutable(dest string, body []byte) (string, error) {
	return stageFile(dest, body, 0o755)
}

func stageFile(dest string, body []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".coddy-update-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return tmpName, nil
}
