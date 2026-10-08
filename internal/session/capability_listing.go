package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The manager is the model-listing source of every configuration it serves
// (config.ListingSource): what a model of a remote Coddy can do - images,
// reasoning levels, the default level, off - is read from the listing cache of
// context_window.go by the readers of internal/config, so a surface that asks a
// configuration for a capability gets the remote's own answer when the row
// writes no key of its own. Nothing here writes a key: a refresh of the listing
// changes the cache and nothing else.
//
// The source reaches a configuration through config.Paths (listing.go there):
// NewManager attaches the manager to the cell of its configuration before it
// stores it, and storeConfig attaches it into the cell of a configuration no
// loader made before publishing it; every configuration loaded from a live
// configuration's Paths shares that cell and is bound the moment it exists.

// ListedModel implements config.ListingSource. It finds the provider row in cfg,
// the configuration that reads, and in no other, then answers from the cache as
// it is at this moment: false until the row's listing has answered once, for a
// model the listing does not carry and for a row that is not of type coddy (the
// listing of every other type holds a window and nothing a capability reader
// uses). A listing refresh that failed after one that answered leaves the last
// record in place, so it is still the one reported. The levels are a copy.
func (m *Manager) ListedModel(cfg *config.Config, providerName, apiModel string) (config.ListedModel, bool) {
	if cfg == nil {
		return config.ListedModel{}, false
	}
	prov := cfg.FindProvider(strings.TrimSpace(providerName))
	if prov == nil || strings.TrimSpace(prov.Type) != "coddy" {
		return config.ListedModel{}, false
	}
	rec, ok := m.ProviderModelEntry(cfg, providerName, apiModel)
	if !ok {
		return config.ListedModel{}, false
	}
	return config.ListedModel{
		Multimodal:        rec.Multimodal,
		ReasoningLevels:   rec.ReasoningLevels,
		ReasoningDefault:  rec.ReasoningDefault,
		AllowReasoningOff: rec.AllowReasoningOff,
	}, true
}

// bindListing makes the manager the listing source of cfg's lineage. A Paths
// with no cell (a configuration no loader made) gets one first, the only plain
// write of the binding: it happens before the configuration is published, and
// never for a configuration that already has its cell, which a loader gave it
// and which other holders may be reading. takeOver is NewManager's: the manager
// built last on a configuration object owns it (last attach wins). Otherwise
// (storeConfig) the manager attaches only into an empty cell, so a cell another
// manager attached is never taken over.
func (m *Manager) bindListing(cfg *config.Config, takeOver bool) {
	if cfg == nil {
		return
	}
	if paths := cfg.Paths.WithListing(); paths != cfg.Paths {
		cfg.Paths = paths
	}
	if takeOver {
		cfg.Paths.AttachListing(m)
		return
	}
	cfg.Paths.AttachListingIfEmpty(m)
}
