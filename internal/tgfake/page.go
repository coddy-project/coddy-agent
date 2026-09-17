package tgfake

import (
	_ "embed"
	"net/http"
)

//go:embed page.html
var pageHTML []byte

// servePage is the chat page: the person's side of the conversation in a
// browser, with the bot's keyboards as buttons and the Bot API calls
// alongside. It is plain HTML and script with no outside asset, so it works
// on a machine with no network.
func (s *Server) servePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(pageHTML)
}
