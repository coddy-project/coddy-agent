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

var toolCallFolderPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ValidateToolCallID reports whether a tool call id can be used verbatim as the
// name of its folder under tool_calls/. The id is whatever the model's provider
// sent, so nothing about it is trusted: "../x" or an id carrying a separator
// would put the folder outside the session bundle, and one past the filesystem's
// name limit could not be created at all. Ids that fail this are not refused -
// ToolCallDirName derives a safe name for them - so the set is deliberately
// narrow.
func ValidateToolCallID(id string) error {
	if id == "" {
		return fmt.Errorf("invalid tool call id: must not be empty")
	}
	if id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid tool call id: must not contain path separators")
	}
	if strings.HasPrefix(id, ".") {
		return fmt.Errorf("invalid tool call id: hidden names and traversal are not allowed")
	}
	if !toolCallFolderPattern.MatchString(id) {
		return fmt.Errorf("invalid tool call id: only letters, digits, underscore, dot, and hyphen allowed (max 128 chars)")
	}
	return nil
}
