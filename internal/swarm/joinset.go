package swarm

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// JoinSet runs one join client per configured parent relay.
type JoinSet struct {
	clients []*Client
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// StartJoins registers this process into every relay listed in swarm.join and
// keeps those registrations alive until Stop.
//
// Both `coddy http` and `coddy swarm` call it: an agent joins a relay, and a
// relay joins another relay exactly the same way. That symmetry is what makes a
// chain of relays work without a second mechanism.
func StartJoins(ctx context.Context, cfg *config.Config, kind, home string, handler http.Handler, log *slog.Logger) (*JoinSet, error) {
	if cfg == nil || len(cfg.Swarm.Join) == 0 {
		return &JoinSet{}, nil
	}
	if log == nil {
		log = slog.Default()
	}
	var store SecretStore
	if strings.TrimSpace(home) != "" {
		store = NewFileSecretStore(home)
	}

	set := &JoinSet{}
	for _, j := range cfg.Swarm.Join {
		client, err := NewClient(JoinOptions{
			RelayURL:     j.URL,
			Name:         j.Name,
			Kind:         kind,
			PairingToken: j.PairingToken,
			AdvertiseURL: j.AdvertiseURL,
			NodeToken:    j.Token,
			Version:      version.Get(),
			Labels:       j.Labels,
			Dial: netx.Options{
				Proxy:              j.Dial.Proxy,
				CAFile:             j.Dial.CAFile,
				InsecureSkipVerify: j.Dial.InsecureSkipVerify,
			},
			Handler: handler,
			Secrets: store,
			Log:     log,
		})
		if err != nil {
			set.Stop()
			return nil, err
		}
		set.clients = append(set.clients, client)
	}

	runCtx, cancel := context.WithCancel(ctx)
	set.cancel = cancel
	for _, c := range set.clients {
		set.wg.Add(1)
		go func(c *Client) {
			defer set.wg.Done()
			log.Info("swarm: joining relay",
				"relay", c.opts.RelayURL, "node", c.Name(), "kind", c.opts.Kind, "transport", c.Transport())
			_ = c.Run(runCtx)
		}(c)
	}
	return set, nil
}

// Clients exposes the running join clients, for status surfaces and tests.
func (s *JoinSet) Clients() []*Client {
	if s == nil {
		return nil
	}
	return s.clients
}

// Stop ends every registration loop and waits for them.
func (s *JoinSet) Stop() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
