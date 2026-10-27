//go:build browser

package browser

import (
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Available reports whether this binary carries the browser module.
const Available = true

// Mount puts the extension's endpoint on the HTTP server's mux: the socket
// and the file transfers, over the process hub the browser tool calls
// through.
func Mount(mux *http.ServeMux, opts MountOptions) Mounted {
	log := logger.Component(opts.Log, logger.ComponentBrowser)
	srv := NewServer(Default(), opts.Key, log)
	mux.Handle(ConnectPattern, srv)
	mux.Handle(FilesPattern, srv)
	api := &API{Hub: Default(), Allow: opts.ServeRemote, Log: log}
	api.Register(mux)
	Default().setServing(true)
	detach := func() {}
	if mgr := opts.Mgr; mgr != nil {
		Default().SetTitles(func(id string) string {
			if st := mgr.SessionByID(id); st != nil {
				return st.ConversationTitle()
			}
			return ""
		})
		remove := mgr.AddTurnObserver(func(event session.TurnEvent) {
			if event.Phase == session.TurnPhaseEnded {
				Default().TurnEnded(event.SessionID)
			}
		})
		detach = func() {
			remove()
			Default().SetTitles(nil)
		}
	}
	return Mounted{
		Disconnect: srv.CloseSockets,
		Close: func() {
			detach()
			Default().setServing(false)
			api.close()
			srv.CloseSockets()
		},
	}
}
