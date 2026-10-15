package swarm

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The reserved label of a node whose join token opens only the shared-model
// routes. The relay reads one exact key and one exact value from a node's
// labels and ignores everything else it finds there. internal/config keeps a
// copy of the key for the config check (it cannot import this package), and a
// test holds the two equal.
const (
	LabelTokenClass        = "coddy.token_class"
	TokenClassSharedModels = "shared_models"
)

// DerivedLabels is the labels a node registers with: the operator's own, with
// the reserved key set from the token the node actually sends. It sets
// coddy.token_class: shared_models when the join's token is one of
// httpserver.shared_models.tokens and the process registers as an agent, and
// removes the key otherwise, so a hand-written value never survives on a node
// whose token is a main one: the label follows the token and cannot drift. The
// result is a copy; cfg and the join's own map are never mutated.
func DerivedLabels(cfg *config.Config, join config.SwarmJoin, kind string) map[string]string {
	out := make(map[string]string, len(join.Labels)+1)
	for k, v := range join.Labels {
		out[k] = v
	}
	delete(out, LabelTokenClass)
	if kind == KindAgent && cfg != nil {
		token := strings.TrimSpace(join.Token)
		if token != "" {
			for _, shared := range cfg.HTTPServer.EffectiveSharedTokens() {
				if shared == token {
					out[LabelTokenClass] = TokenClassSharedModels
					break
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SharedModelsOnly reports whether a node's registration says its token opens
// only the shared-model routes. The label is a node's claim, used for one
// purpose: not to warn about the 401 its sessions route answers. A relay is
// never one (its sessions come through /swarm/sessions), and any other value,
// a case variant or a padded value is ignored.
func SharedModelsOnly(labels map[string]string, kind string) bool {
	return kind == KindAgent && labels[LabelTokenClass] == TokenClassSharedModels
}
