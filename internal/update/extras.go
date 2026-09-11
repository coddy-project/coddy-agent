package update

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The Linux and macOS release archives carry the man page and the shell
// completions beside the binary, and the install script puts them into the
// share directory of the binary's prefix: ~/.local/bin -> ~/.local/share,
// /usr/local/bin -> /usr/local/share. A self-update that replaced only the
// executable left those files describing the release before it, so the
// completer kept offering commands the binary no longer had (issue #188).
// installRelease refreshes every one of them that is there.

// extraFile is one of those files: its name inside the archive and the place
// the installer puts it under the share directory.
type extraFile struct {
	Member string // name inside the release archive
	Rel    string // path under the share directory, slash-separated
	Label  string // how the file is named to the user
}

// extraFiles lists them in the order they are reported.
var extraFiles = []extraFile{
	{Member: "coddy.1", Rel: "man/man1/coddy.1", Label: "man page"},
	{Member: "coddy.bash", Rel: "bash-completion/completions/coddy", Label: "bash completion"},
	{Member: "coddy.zsh", Rel: "zsh/site-functions/_coddy", Label: "zsh completion"},
}

// shareDirFor returns the data directory that pairs with the executable at
// dest under the installer's layout: the "share" beside the "bin" holding it.
// An executable outside a bin directory - a build tree, a bare download - has
// no such pair, and "" says so.
func shareDirFor(dest string) string {
	bin := filepath.Dir(dest)
	if filepath.Base(bin) != "bin" {
		return ""
	}
	return filepath.Join(filepath.Dir(bin), "share")
}

// installedExtras returns the extras present under share, in table order.
func installedExtras(share string) []extraFile {
	if share == "" {
		return nil
	}
	var present []extraFile
	for _, e := range extraFiles {
		info, err := os.Stat(filepath.Join(share, filepath.FromSlash(e.Rel)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		present = append(present, e)
	}
	return present
}

// installRelease replaces the executable at dest with the one in the archive,
// then refreshes the man page and the completions the installer put beside it.
// Only files already there are touched, and none is created: one that was
// never installed was a choice - --no-shell-setup, an install by hand - that
// an update does not revisit. The binary goes first, so a failure half-way
// leaves a new binary with old completions, which is what the user had, and
// never new completions for an old binary.
func installRelease(data []byte, archiveName, dest, tag string, out io.Writer) error {
	binName := BinaryName(runtimeGOOSFromArchive(archiveName))
	share := shareDirFor(dest)
	extras := installedExtras(share)
	want := map[string]bool{binName: true}
	for _, e := range extras {
		want[e.Member] = true
	}
	files, err := filesFromTarGz(data, want)
	if err != nil {
		return err
	}
	body, ok := files[binName]
	if !ok {
		return fmt.Errorf("archive missing %q", binName)
	}
	if err := writeExecutable(dest, body); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Installed %s (%s)\n", tag, dest)

	var refreshed, kept []string
	for _, e := range extras {
		body, ok := files[e.Member]
		if !ok {
			// A release from before the archives carried these files: the
			// installed copies are all there is, and they stay.
			kept = append(kept, e.Label)
			continue
		}
		path := filepath.Join(share, filepath.FromSlash(e.Rel))
		if err := writeFile(path, body, 0o644); err != nil {
			return fmt.Errorf("%s is installed, but refreshing the %s beside it failed: %w", tag, e.Label, err)
		}
		refreshed = append(refreshed, e.Label)
	}
	if len(refreshed) > 0 {
		_, _ = fmt.Fprintf(out, "Refreshed %s under %s\n", joinLabels(refreshed), share)
	}
	if len(kept) > 0 {
		_, _ = fmt.Fprintf(out, "Kept %s under %s: release %s does not carry them\n", joinLabels(kept), share, tag)
	}
	return nil
}

// joinLabels renders "the a", "the a and the b", "the a, the b and the c".
func joinLabels(labels []string) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = "the " + l
	}
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
