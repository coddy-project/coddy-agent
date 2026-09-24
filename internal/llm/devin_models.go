package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The Devin catalog lists every model variant as its own uid: a family such
// as Claude Opus 5 comes as claude-opus-5-low, -medium, -high, -xhigh and
// -max, plus paid SKU variants (-fast, -priority, -1m). Coddy offers one
// model per family and reaches the variants through its reasoning level, so
// `devin/claude-opus-5` at level high is sent as claude-opus-5-high. A full
// uid in the config still works: it is sent as written.

// devinLevelOrder is the order reasoning levels are offered in.
var devinLevelOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// devinSKUSuffixes mark variants that differ in price or window, not in
// reasoning; they stay reachable by their full uid only.
var devinSKUSuffixes = []string{"fast", "priority", "1m"}

// devinFamily is one model family of the catalog.
type devinFamily struct {
	id    string
	label string
	// levels maps a reasoning level to the variant uid serving it.
	levels map[string]string
	// defaultUID is the variant the catalog marks as the family default.
	defaultUID     string
	contextWindow  int
	maxOutput      int
	supportsImages bool
}

// orderedLevels lists the family's levels in devinLevelOrder.
func (f *devinFamily) orderedLevels() []string {
	var out []string
	for _, lv := range devinLevelOrder {
		if _, ok := f.levels[lv]; ok {
			out = append(out, lv)
		}
	}
	return out
}

// defaultLevel names the level the default variant serves, "" when it
// serves none of them.
func (f *devinFamily) defaultLevel() string {
	for _, lv := range devinLevelOrder {
		if f.levels[lv] == f.defaultUID {
			return lv
		}
	}
	return ""
}

// devinCatalog is a fetched catalog: its families in the catalog's own order
// and every usable variant by uid.
type devinCatalog struct {
	families []*devinFamily
	byID     map[string]*devinFamily
	variants map[string]devinCatalogEntry
}

// devinVariantSuffix splits a variant uid's trailing level and SKU tokens off.
// "claude-opus-5-high-fast" gives level "high" and sku true;
// "claude-opus-4-6-thinking" gives level "high" and thinking true; "glm-5-2"
// gives nothing.
func devinVariantSuffix(uid string) (level string, sku, thinking bool) {
	rest := uid
	for {
		trimmed := false
		for _, s := range devinSKUSuffixes {
			if strings.HasSuffix(rest, "-"+s) {
				rest = strings.TrimSuffix(rest, "-"+s)
				sku, trimmed = true, true
			}
		}
		if !trimmed {
			break
		}
	}
	if strings.HasSuffix(rest, "-thinking") {
		return "high", sku, true
	}
	for _, lv := range devinLevelOrder {
		if strings.HasSuffix(rest, "-"+lv) {
			return lv, sku, false
		}
	}
	return "", sku, false
}

// buildDevinCatalog groups catalog entries into families. Legacy and disabled
// entries are dropped, as the Devin clients drop them.
func buildDevinCatalog(entries []devinCatalogEntry) *devinCatalog {
	c := &devinCatalog{byID: map[string]*devinFamily{}, variants: map[string]devinCatalogEntry{}}
	type member struct {
		e        devinCatalogEntry
		level    string
		sku      bool
		thinking bool
	}
	members := map[string][]member{}
	for _, e := range entries {
		uid := strings.TrimSpace(e.uid)
		if uid == "" || e.legacy || e.disabled {
			continue
		}
		e.uid = uid
		c.variants[uid] = e
		fam := strings.TrimSpace(e.family)
		if fam == "" {
			fam = uid
		}
		if _, seen := c.byID[fam]; !seen {
			label := strings.TrimSpace(e.familyLabel)
			if label == "" {
				label = e.label
			}
			f := &devinFamily{id: fam, label: label, levels: map[string]string{}}
			c.byID[fam] = f
			c.families = append(c.families, f)
		}
		level, sku, thinking := devinVariantSuffix(uid)
		members[fam] = append(members[fam], member{e: e, level: level, sku: sku, thinking: thinking})
	}
	for _, f := range c.families {
		ms := members[f.id]
		hasPlain := false
		for _, m := range ms {
			if !m.sku {
				hasPlain = true
				break
			}
		}
		var plainNoLevel string
		thinkingOnly := true
		// A level named in the uid wins over the "high" a "-thinking"
		// variant stands for, whichever the catalog lists first.
		fromThinking := map[string]bool{}
		for _, m := range ms {
			if m.sku && hasPlain {
				continue
			}
			if m.e.familyDefault && f.defaultUID == "" {
				f.defaultUID = m.e.uid
			}
			switch {
			case m.level == "":
				if plainNoLevel == "" {
					plainNoLevel = m.e.uid
				}
			case f.levels[m.level] == "" || (fromThinking[m.level] && !m.thinking):
				f.levels[m.level] = m.e.uid
				fromThinking[m.level] = m.thinking
			}
			if m.level != "" && !m.thinking {
				thinkingOnly = false
			}
			if m.e.contextWindow > 0 && (f.contextWindow == 0 || m.e.contextWindow < f.contextWindow) {
				f.contextWindow = m.e.contextWindow
			}
			if m.e.maxOutput > 0 && (f.maxOutput == 0 || m.e.maxOutput < f.maxOutput) {
				f.maxOutput = m.e.maxOutput
			}
			f.supportsImages = f.supportsImages || m.e.supportsImages
		}
		// A family that pairs a plain variant with a "-thinking" one only
		// (Claude Opus 4.6: claude-opus-4-6 and claude-opus-4-6-thinking)
		// offers thinking on and off: the plain variant is its "none".
		if plainNoLevel != "" && len(f.levels) > 0 && thinkingOnly && f.levels["none"] == "" {
			f.levels["none"] = plainNoLevel
		}
		if f.defaultUID == "" {
			switch {
			case plainNoLevel != "":
				f.defaultUID = plainNoLevel
			case f.levels["medium"] != "":
				f.defaultUID = f.levels["medium"]
			default:
				for _, m := range ms {
					if !m.sku || !hasPlain {
						f.defaultUID = m.e.uid
						break
					}
				}
			}
		}
	}
	return c
}

// resolveUID picks the uid a request for model at the given reasoning level
// is sent as. A known uid is sent as is; a family id maps the level onto its
// variant ("off" onto "none", "minimal" onto the lowest level the family
// has), falling back to the family default; anything else is sent verbatim
// for the server to judge.
func (c *devinCatalog) resolveUID(model, effort string) string {
	model = strings.TrimSpace(model)
	if c == nil {
		return model
	}
	if _, ok := c.variants[model]; ok {
		return model
	}
	f := c.byID[model]
	if f == nil {
		return model
	}
	switch lv := strings.ToLower(strings.TrimSpace(effort)); lv {
	case "":
	case reasoningOff:
		if uid := f.levels["none"]; uid != "" {
			return uid
		}
	case "minimal":
		for _, alt := range []string{"minimal", "none", "low"} {
			if uid := f.levels[alt]; uid != "" {
				return uid
			}
		}
	default:
		if uid := f.levels[lv]; uid != "" {
			return uid
		}
	}
	return f.defaultUID
}

// devinCatalogTTL is how long a fetched catalog serves model resolution.
const devinCatalogTTL = 30 * time.Minute

var devinCatalogCache = struct {
	mu sync.Mutex
	m  map[string]*devinCatalogCacheEntry
}{m: map[string]*devinCatalogCacheEntry{}}

// devinCatalogCacheEntry holds one account's catalog. Its lock serializes
// the refresh, so turns that start together fetch once.
type devinCatalogCacheEntry struct {
	mu        sync.Mutex
	cat       *devinCatalog
	fetchedAt time.Time
}

// devinCatalogFor returns the account's catalog, fetched at most once per TTL.
// A failed refresh keeps serving the last catalog it has.
func devinCatalogFor(ctx context.Context, hc *http.Client, cred devinCredential) (*devinCatalog, error) {
	key := devinJWTKey(cred)
	devinCatalogCache.mu.Lock()
	entry := devinCatalogCache.m[key]
	if entry == nil {
		entry = &devinCatalogCacheEntry{}
		devinCatalogCache.m[key] = entry
	}
	devinCatalogCache.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.cat != nil && time.Since(entry.fetchedAt) < devinCatalogTTL {
		return entry.cat, nil
	}
	entries, err := fetchDevinCatalog(ctx, hc, cred)
	if err != nil {
		if entry.cat != nil {
			return entry.cat, nil
		}
		return nil, err
	}
	entry.cat, entry.fetchedAt = buildDevinCatalog(entries), time.Now()
	return entry.cat, nil
}

// fetchDevinCatalog calls GetCliModelConfigs. The catalog is asked for as a
// Windsurf client: that is the identity the server answers with the full
// family list.
func fetchDevinCatalog(ctx context.Context, hc *http.Client, cred devinCredential) ([]devinCatalogEntry, error) {
	jwt, err := devinJWTFor(ctx, hc, cred)
	if err != nil {
		return nil, err
	}
	meta := devinMetadata{ide: devinCatalogIDE, apiKey: cred.token, userJWT: jwt.jwt, sessionID: newCodexSessionID(), requestID: uint64(time.Now().UnixMilli()), triggerID: newCodexSessionID()}
	var w pbWriter
	w.msg(1, meta.encode())
	body, err := devinUnary(ctx, hc, jwt.chatURL+"/exa.api_server_pb.ApiServerService/GetCliModelConfigs", w.buf)
	if err != nil {
		return nil, fmt.Errorf("devin model catalog: %w", err)
	}
	entries, err := decodeDevinCatalog(body)
	if err != nil {
		return nil, fmt.Errorf("devin model catalog: %w", err)
	}
	return entries, nil
}

// listDevinModels answers ListModels for a devin provider: one entry per
// family, in the catalog's own order.
func listDevinModels(ctx context.Context, in ProviderInput) ([]ModelEntry, error) {
	cred, err := resolveDevinCredential(in.APIKey, in.AuthPath, !in.NoCLILogin)
	if err != nil {
		return nil, err
	}
	hc, err := HTTPClientForProviderProxy(in.ProxyURL)
	if err != nil {
		return nil, err
	}
	cat, err := devinCatalogFor(ctx, hc, cred)
	if err != nil {
		return nil, err
	}
	out := make([]ModelEntry, 0, len(cat.families))
	for _, f := range cat.families {
		out = append(out, ModelEntry{ID: f.id, Name: f.label, ContextWindow: f.contextWindow})
	}
	return out, nil
}

// ApplyDevinLoginToConfig publishes what the signed-in account serves into
// config.yaml: the provider row, one model per family with the reasoning
// levels its variants offer, and agent.model when none is set - the same way
// ApplyCodexLoginToConfig publishes a subscription catalog. It only ever
// adds, and returns the names of what it added.
func ApplyDevinLoginToConfig(ctx context.Context, cfg *config.Config, name, explicitKey, authPath, proxyURL string) ([]string, error) {
	if cfg == nil {
		return nil, fmt.Errorf("devin: no config to update")
	}
	cred, err := resolveDevinCredential(explicitKey, authPath, cfg.ProviderMayUseCLILogin(name, "devin"))
	if err != nil {
		return nil, err
	}
	hc, err := HTTPClientForProviderProxy(proxyURL)
	if err != nil {
		return nil, err
	}
	entries, err := fetchDevinCatalog(ctx, hc, cred)
	if err != nil {
		return nil, err
	}
	cat := buildDevinCatalog(entries)

	var cmds []config.UCICommand
	var added []string
	if cfg.FindProvider(name) == nil {
		prov, _ := json.Marshal(map[string]string{"name": name, "type": "devin"})
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("providers[name=%s]", name), Value: string(prov)})
		added = append(added, "provider "+name)
	}
	var defaultRef string
	for _, f := range cat.families {
		if !catalogModelIDRe.MatchString(f.id) {
			// The id feeds a config path; the server must not be able to
			// smuggle path syntax into the staged commands.
			continue
		}
		ref := name + "/" + f.id
		if defaultRef == "" {
			defaultRef = ref
		}
		if cfg.FindModelEntry(ref) != nil {
			continue
		}
		// The levels are always written, an empty list included: detection by
		// model id would offer levels the family does not have.
		levels := f.orderedLevels()
		if levels == nil {
			levels = []string{}
		}
		entry := map[string]any{"model": ref, "reasoning_levels": levels}
		if lv := f.defaultLevel(); lv != "" {
			entry["reasoning_default"] = lv
		}
		if f.contextWindow > 0 {
			entry["max_context_tokens"] = f.contextWindow
		}
		if f.supportsImages {
			entry["multimodal"] = true
		}
		raw, _ := json.Marshal(entry)
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("models[model=%s]", ref), Value: string(raw)})
		added = append(added, "model "+ref)
	}
	if strings.TrimSpace(cfg.Agent.Model) == "" && defaultRef != "" {
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: "agent.model", Value: defaultRef})
		added = append(added, "agent.model "+defaultRef)
	}
	if len(cmds) == 0 {
		return nil, nil
	}
	if _, err := config.CommitUCICommands(cfg.Paths, cmds); err != nil {
		return nil, err
	}
	return added, nil
}
