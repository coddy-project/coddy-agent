package update

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// ErrUpdateAvailable is returned from Run when CheckOnly is set and a newer release exists.
var ErrUpdateAvailable = errors.New("update available")

// Options configures a self-update run.
type Options struct {
	APIBase        string
	Repo           string
	CurrentVersion string
	TargetVersion  string // empty = latest release
	GOOS           string
	GOARCH         string
	InstallPath    string // empty = replace os.Executable()
	CheckOnly      bool
	Yes            bool
	Stdout         io.Writer
	HTTPClient     *http.Client
}

// Run checks GitHub releases and optionally installs a newer binary.
func Run(ctx context.Context, opts Options) error {
	if opts.APIBase == "" {
		opts.APIBase = DefaultAPIURL
	}
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.CurrentVersion == "" {
		opts.CurrentVersion = version.Get()
	}
	if opts.GOOS == "" || opts.GOARCH == "" {
		opts.GOOS, opts.GOARCH = CurrentPlatform()
	}
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	rel, err := fetchRelease(ctx, client, opts.APIBase, opts.Repo, strings.TrimSpace(opts.TargetVersion))
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")

	cmp := CompareSemver(opts.CurrentVersion, latest)
	if cmp >= 0 && opts.TargetVersion == "" {
		_, _ = fmt.Fprintf(out, "coddy is up to date (%s)\n", opts.CurrentVersion)
		return nil
	}
	if opts.CheckOnly {
		_, _ = fmt.Fprintf(out, "update available: %s (current %s)\n", latest, opts.CurrentVersion)
		return ErrUpdateAvailable
	}

	assetName, err := AssetFileName(latest, opts.GOOS, opts.GOARCH)
	if err != nil {
		return err
	}
	asset, err := pickAsset(rel, assetName)
	if err != nil {
		return err
	}

	dest := strings.TrimSpace(opts.InstallPath)
	if dest == "" {
		dest, err = resolveExecutablePath()
		if err != nil {
			return err
		}
	}

	if !opts.Yes {
		_, _ = fmt.Fprintf(out, "Update coddy %s -> %s (%s)? [y/N] ", opts.CurrentVersion, latest, dest)
		line, err := readYesNo(os.Stdin)
		if err != nil {
			return err
		}
		if !line {
			_, _ = fmt.Fprintln(out, "cancelled")
			return nil
		}
	}

	_, _ = fmt.Fprintf(out, "Downloading %s ...\n", asset.Name)
	data, err := downloadURL(ctx, client, asset.BrowserDownloadURL)
	if err != nil {
		return err
	}
	if err := installFromArchive(data, asset.Name, dest); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Installed %s (%s)\n", latest, dest)
	return nil
}

// resolveExecutablePath returns the path to replace (symlink-resolved).
func resolveExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return resolved, nil
}

func readYesNo(r io.Reader) (bool, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}
