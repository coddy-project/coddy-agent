//go:build desktop && linux && !android

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The application entry this user has, written on every launch.
//
// Two things come of it. The task bar matches a window against the entries it
// knows (StartupWMClass, WindowClass in browser.go), so a Coddy run from a
// checkout - with no package installed - gets its own icon and its own menu
// there rather than the browser's. And the entry carries the folders of the
// chats this home has had, each as a desktop action: a right click on the
// icon in the task bar or the launcher offers them, and picking one opens a
// new chat in that folder.
//
// It is written under the user's own data directory and names the running
// binary, so it describes the Coddy that wrote it. A package installs an
// entry of its own under /usr/share/applications; this one shadows it, which
// is what the specification says a user's entry does, and says the same
// things about the same program.

// entryName is the file, in both places. A packaged Coddy and one from a
// checkout are the same application, so they are the same entry.
const entryName = "coddy-desktop.desktop"

// maxMenuFolders is how many recent workspaces the menu offers. A desktop
// menu is read at a glance; a list longer than this is a list nobody reads.
const maxMenuFolders = 6

// writeMenuEntry writes the entry for this user. A failure is a menu that
// looks as it did before, so it is a debug line to the caller and never an
// error that stops a window from opening.
func writeMenuEntry(exe string, folders []string) error {
	path, err := menuEntryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("desktop: the applications folder: %w", err)
	}
	return os.WriteFile(path, []byte(menuEntry(exe, folders)), 0o600)
}

func menuEntryPath() (string, error) {
	dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("desktop: no home directory to put the application entry in: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "applications", entryName), nil
}

// menuEntry is the entry itself: the application, and one action per folder.
//
// An action's name is the folder's own name rather than its path, because a
// menu is read at a glance and a path is read with a finger; the whole path
// is in the command, which is what runs. Two folders of the same name - two
// checkouts of one repository - are told apart by the folder above them.
func menuEntry(exe string, folders []string) string {
	lines := []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=Coddy",
		"GenericName=Coding agent",
		"Comment=Coddy agent in a window",
		"Exec=" + quoteExec(exe) + " desktop",
		"Icon=coddy-desktop",
		"Terminal=false",
		"Categories=Development;IDE;",
		"Keywords=coddy;agent;llm;ai;coding;assistant;",
		"StartupNotify=true",
		// What the task bar matches the window against: the launcher starts
		// the browser with --class=coddy-desktop (browser.go, WindowClass).
		"StartupWMClass=" + WindowClass,
	}
	folders = menuFolders(folders)
	if len(folders) > 0 {
		ids := make([]string, 0, len(folders))
		for i := range folders {
			ids = append(ids, fmt.Sprintf("folder%d", i))
		}
		lines = append(lines, "Actions="+strings.Join(ids, ";")+";")
	}
	for i, folder := range folders {
		lines = append(lines,
			"",
			fmt.Sprintf("[Desktop Action folder%d]", i),
			"Name="+menuFolderName(folder),
			"Exec="+quoteExec(exe)+" desktop --new-chat --cwd "+quoteExec(folder),
		)
	}
	return strings.Join(append(lines, ""), "\n")
}

// menuFolders is the list the entry carries: absolute paths, each folder once,
// in the order they were given, and no more than the menu can be read at.
func menuFolders(folders []string) []string {
	out := make([]string, 0, maxMenuFolders)
	seen := map[string]bool{}
	for _, folder := range folders {
		folder = strings.TrimSpace(folder)
		if folder == "" || !filepath.IsAbs(folder) {
			continue
		}
		folder = filepath.Clean(folder)
		if seen[folder] {
			continue
		}
		seen[folder] = true
		out = append(out, folder)
		if len(out) == maxMenuFolders {
			break
		}
	}
	return out
}

// menuFolderName is what a row of the menu reads: the folder's own name, with
// the one above it in front, which is what tells two checkouts of the same
// repository apart. The name of a row may not carry a newline, and a path may.
func menuFolderName(folder string) string {
	parent, name := filepath.Split(filepath.Clean(folder))
	if name == "" {
		name = folder
	}
	if above := filepath.Base(filepath.Clean(parent)); above != "" && above != "." && above != string(filepath.Separator) {
		name = above + "/" + name
	}
	return strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\r", " ")
}

// removeMenuEntry takes the entry away, for a test and for nothing else yet.
func removeMenuEntry() error {
	path, err := menuEntryPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
