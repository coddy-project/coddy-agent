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
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	for {
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
		if base != binName {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, 128<<20))
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	return nil, fmt.Errorf("archive missing %q", binName)
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
	tmpName, err := stageExecutable(dest, body)
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
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return tmpName, nil
}
