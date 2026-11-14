//go:build desktop && linux && !android

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The application entry this user gets, and the folders in its menu.

func TestTheEntryIsWrittenWhereTheDesktopReadsIt(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	if err := writeMenuEntry("/usr/bin/coddy", []string{"/home/p/work", "/home/p/notes"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "applications", entryName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no entry was written: %v", err)
	}
	text := string(b)
	for _, want := range []string{
		"[Desktop Entry]",
		"Exec=/usr/bin/coddy desktop",
		"Icon=coddy-desktop",
		// Without this line the task bar matches the window against the
		// browser's entry and draws the browser's icon and menu.
		"StartupWMClass=" + WindowClass,
		"Actions=folder0;folder1;",
		"[Desktop Action folder0]",
		"Name=p/work",
		"Exec=/usr/bin/coddy desktop --new-chat --cwd /home/p/work",
		"[Desktop Action folder1]",
		"Name=p/notes",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the entry has no %q in it:\n%s", want, text)
		}
	}
	if err := removeMenuEntry(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the entry is still there: %v", err)
	}
}

// A home with no chats yet has no folders to offer, and an entry with an
// empty Actions line is one some desktops refuse to read at all.
func TestAnEntryWithNoFoldersNamesNoActions(t *testing.T) {
	entry := menuEntry("/usr/bin/coddy", nil)
	if strings.Contains(entry, "Actions=") {
		t.Fatalf("an entry with no folders still names actions:\n%s", entry)
	}
}

func TestTheMenuTakesEachFolderOnceAndOnlyAFew(t *testing.T) {
	many := []string{
		"/a/one", "/a/one", "/a/two", "", "relative/path", "/a/three",
		"/a/four", "/a/five", "/a/six", "/a/seven", "/a/eight",
	}
	got := menuFolders(many)
	if len(got) != maxMenuFolders {
		t.Fatalf("the menu offers %d folders: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, folder := range got {
		if seen[folder] {
			t.Fatalf("the menu offers %q twice: %v", folder, got)
		}
		seen[folder] = true
		if !filepath.IsAbs(folder) {
			t.Fatalf("the menu offers %q, which names no folder", folder)
		}
	}
	if got[0] != "/a/one" || got[1] != "/a/two" {
		t.Fatalf("the menu lost the order it was given: %v", got)
	}
}

// A path with a space in it is still a path, and an unquoted Exec line would
// start something else with the rest of it as arguments.
func TestAFolderWithASpaceIsQuotedInTheCommand(t *testing.T) {
	entry := menuEntry("/usr/bin/coddy", []string{"/home/p/My Work"})
	if !strings.Contains(entry, `Exec=/usr/bin/coddy desktop --new-chat --cwd "/home/p/My Work"`) {
		t.Fatalf("the folder is not quoted:\n%s", entry)
	}
}
