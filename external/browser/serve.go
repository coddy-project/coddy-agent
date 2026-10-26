//go:build browser

package browser

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Available reports whether this binary carries the browser module.
const Available = true

// Serve runs the extension listener until ctx ends. It follows the manager's
// turns, so a browser hears when a chat it holds tabs of stops working, and
// names each chat's tab group after the chat's title.
func Serve(ctx context.Context, opts Options) error {
	log := logger.Component(opts.Log, logger.ComponentBrowser)
	if mgr := opts.Mgr; mgr != nil {
		Default().SetTitles(func(id string) string {
			if st := mgr.SessionByID(id); st != nil {
				return st.ConversationTitle()
			}
			return ""
		})
		defer Default().SetTitles(nil)
		remove := mgr.AddTurnObserver(func(event session.TurnEvent) {
			if event.Phase == session.TurnPhaseEnded {
				Default().TurnEnded(event.SessionID)
			}
		})
		defer remove()
	}
	b := opts.Cfg.Browser
	srv := NewServer(Default(), b.Token, log)
	log.Info("browser listener", "addr", b.ListenAddr(), "tls", b.CertFile != "")
	return srv.Serve(ctx, b.ListenAddr(), b.CertFile, b.KeyFile)
}
