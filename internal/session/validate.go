package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var sessionFolderPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// ValidateFolderSessionID rejects path segments that could escape the sessions root (slashes, traversal, dots).
func ValidateFolderSessionID(id string) error {
	if id != filepath.Base(id) {
		return fmt.Errorf("invalid session id: must not contain path separators")
	}
	if strings.HasPrefix(id, ".") {
		return fmt.Errorf("invalid session id: hidden names are not allowed")
	}
	if !sessionFolderPattern.MatchString(id) {
		return fmt.Errorf("invalid session id: only letters, digits, underscore, and hyphen allowed (max 256 chars)")
	}
	return nil
}
