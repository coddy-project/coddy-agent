package config

import (
	"strings"
	"sync/atomic"
)

// What a model of a remote Coddy (provider type coddy) can do is decided by the
// remote's own listing, which the session manager's cache holds and a
// configuration never does: a listing refresh writes no key of any models[]
// row. This file is the configuration's side of that: the shape of what the
// cache knows (ListedModel), the narrow view of the cache the readers need
// (ListingSource), the cell that carries a source on config.Paths so that every
// configuration of one lineage reaches it (ListingCell, Paths.WithListing,
// Paths.AttachListing, Paths.AttachListingIfEmpty), and the one place that asks
// it (listedFor).
//
// The rules, each of which a test of the decided model (p2-d1-config-source)
// fails without:
//   - one cell per lineage: every loader allocates it when the Paths it is
//     handed has none and every constructor hands its Paths on, so a config
//     loaded from c.Paths shares c's cell and is bound the moment it exists;
//   - the bind is one pointer store, made before the configuration is
//     published (session.NewManager, Manager.storeConfig);
//   - the row comes from the reader's own configuration: the source receives
//     the receiver of the read, never "the current configuration";
//   - nothing resolved is memoised in a Config, a turn or a registry.

// ListedModel is what a model-listing cache knows about one model of one
// provider row.
type ListedModel struct {
	Multimodal        bool
	ReasoningLevels   []string // without "off"
	ReasoningDefault  string
	AllowReasoningOff bool
}

// ListingSource is the manager's cache seen from the configuration. ListedModel
// finds the provider row in the cfg it is given (cfg.FindProvider, then the
// cache key of that row) and in no other configuration, and answers from the
// cache as it is at the moment of the call.
type ListingSource interface {
	ListedModel(cfg *Config, providerName, apiModel string) (ListedModel, bool)
}

// ListingCell holds the source of one lineage of configurations: one atomic
// pointer, no lock. It is reached through Paths, which stays a copyable value
// because it holds only a pointer to the cell.
type ListingCell struct {
	src atomic.Pointer[ListingSource]
}

func (c *ListingCell) source() ListingSource {
	if c == nil {
		return nil
	}
	if p := c.src.Load(); p != nil {
		return *p
	}
	return nil
}

// WithListing returns p with a fresh cell when it has none; a Paths that has a
// cell comes back unchanged, so everything loaded through it shares the cell.
// Every loader calls it on the Paths it is handed.
func (p Paths) WithListing() Paths {
	if p.listing == nil {
		p.listing = &ListingCell{}
	}
	return p
}

// AttachListing stores src as the source of p's lineage; the last attach wins
// and a nil src detaches. A Paths with no cell has nothing to attach to and the
// call does nothing: it never panics.
func (p Paths) AttachListing(src ListingSource) {
	if p.listing == nil {
		return
	}
	if src == nil {
		p.listing.src.Store(nil)
		return
	}
	p.listing.src.Store(&src)
}

// AttachListingIfEmpty stores src only into a cell that holds no source, and
// reports whether it did: a source another manager attached is never taken
// over. A Paths with no cell, and a nil src, report false.
func (p Paths) AttachListingIfEmpty(src ListingSource) bool {
	if p.listing == nil || src == nil {
		return false
	}
	return p.listing.src.CompareAndSwap(nil, &src)
}

// listedFor is the one read of the listing every capability reader goes
// through. It is false unless the row's provider is of type coddy in this very
// configuration, a source is attached to its lineage and the source's cache
// holds the model: no source, no listing that has answered yet and a model the
// listing does not carry are all "not known", and a failed refresh keeps the
// last record that answered (the source's cache does). It passes its own
// receiver to the source, so a configuration an agent or a request still holds
// after a reload is answered for its own row, and it reads live on every call.
func (c *Config) listedFor(ent *ModelEntry) (ListedModel, bool) {
	if c == nil || ent == nil {
		return ListedModel{}, false
	}
	prov := c.FindProvider(ent.ProviderName())
	if prov == nil || strings.TrimSpace(prov.Type) != "coddy" {
		return ListedModel{}, false
	}
	src := c.Paths.listing.source()
	if src == nil {
		return ListedModel{}, false
	}
	lm, ok := src.ListedModel(c, prov.Name, ent.APIModel())
	if !ok {
		return ListedModel{}, false
	}
	// The source's cache copies records out; this copy keeps a caller of the
	// resolver, whatever it does to the list, away from the source's memory.
	lm.ReasoningLevels = append([]string(nil), lm.ReasoningLevels...)
	return lm, true
}

// isCoddyProviderType reports whether a provider type is the remote-Coddy one.
func isCoddyProviderType(t string) bool { return strings.TrimSpace(t) == "coddy" }

// isCoddyRow reports whether the row's provider is of type coddy in this
// configuration. A nil configuration, a nil row and a provider the
// configuration does not have are not.
func (c *Config) isCoddyRow(ent *ModelEntry) bool {
	if c == nil || ent == nil {
		return false
	}
	prov := c.FindProvider(ent.ProviderName())
	return prov != nil && isCoddyProviderType(prov.Type)
}

// coddyCapabilities is what the five capability readers answer for one row of a
// provider of type coddy, resolved from the row's own keys and one record of the
// remote's listing.
type coddyCapabilities struct {
	levels     []string
	off        bool
	def        string
	multimodal bool
}

// resolveCoddy resolves a coddy row key by key. A key written on the row wins,
// the listing answers an absent one, and with no listing (no source, no answer
// yet, a model the listing does not carry) an absent key is "not known": no
// levels, no off, no default, no images. The levels are never detected from the
// alias. One call reads the listing once, so the four facts of a call come from
// one record.
//
//	levels   reasoning_levels as written (an explicit [] hides the selector),
//	         else the listing's levels (the remote already applied its own
//	         mapping, so there is no remap), else none
//	default  reasoning_default when written, else the listing's; either only
//	         when it is one of the resolved levels (a written default the levels
//	         do not offer resolves to none, the listing's does not step in)
//	off      allow_reasoning_off as written, else the listing's; a model without
//	         levels has nothing to switch
//	images   multimodal as written, else the listing's
func (c *Config) resolveCoddy(ent *ModelEntry) coddyCapabilities {
	lm, known := c.listedFor(ent)
	var r coddyCapabilities

	switch {
	case ent.ReasoningLevels != nil:
		r.levels = ent.ResolvedReasoningLevels()
	case known:
		for _, lv := range lm.ReasoningLevels {
			if lv != ReasoningOff {
				r.levels = append(r.levels, lv)
			}
		}
	}

	switch {
	case ent.AllowReasoningOff != nil:
		r.off = *ent.AllowReasoningOff
	case known:
		r.off = lm.AllowReasoningOff
	}
	r.off = r.off && len(r.levels) > 0

	def := strings.TrimSpace(ent.ReasoningDefault)
	if def == "" && known {
		def = strings.TrimSpace(lm.ReasoningDefault)
	}
	for _, lv := range r.levels {
		if def != "" && lv == def {
			r.def = def
			break
		}
	}

	switch {
	case ent.Multimodal != nil:
		r.multimodal = *ent.Multimodal
	case known:
		r.multimodal = lm.Multimodal
	}
	return r
}
