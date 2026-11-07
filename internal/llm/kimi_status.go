package llm

import (
	"io"
	"net/http"
)

// kimiStatusBodyLimit bounds how much of a refused answer travels in the
// error. Kimi reports what is wrong with a request in the body, and without
// it a 400 says only that something was refused.
const kimiStatusBodyLimit = 2048

// kimiStatusError turns a non-2xx inference answer into an httpStatusError
// carrying the provider's own explanation, and closes the body.
func kimiStatusError(label string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, kimiStatusBodyLimit))
	_ = resp.Body.Close()
	return &httpStatusError{label: label, status: resp.StatusCode, header: resp.Header.Clone(), body: streamErrorSnippet(snippet)}
}
