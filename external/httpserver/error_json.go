//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
)

// errorJSON is the body of an error answer, {"error":{"message":msg}}, with
// msg encoded as JSON. Go's %q is not JSON: it writes a control character
// such as the ESC of a colour code as \x1b and invalid UTF-8 as \xNN, which
// no JSON parser accepts. HTML characters are left as they are, as %q left
// them, so an ordinary message reads the same as before.
func errorJSON(msg string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Message string `json:"message"`
	}{Message: msg}})
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}
