package logger

import (
	"context"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// ComponentKey is the attribute a logger carries to name the subsystem that
// emitted a record. It is what logger.levels keys on, and it stays in the
// output so a file can be filtered by subsystem after the fact.
const ComponentKey = "component"

// Component names wired into the process today. A name is a dotted path and a
// parent covers everything under it, so configuring "gateway" also raises
// "gateway.telegram" unless that name carries its own level.
//
// Only a component set through Component (that is, through WithAttrs) scopes a
// level. A "component" attribute passed inline on a single record reads the
// same in a log file but cannot scope anything: slog asks Enabled whether to
// build the record before any of its attributes exist.
const (
	ComponentGateway         = "gateway"
	ComponentGatewayTelegram = "gateway.telegram"
	ComponentSession         = "session"
	ComponentAgent           = "agent"
	ComponentScheduler       = "scheduler"
)

// Component returns a logger tagged as belonging to name, so logger.levels can
// raise or lower verbosity for that subsystem alone.
//
// The tag is an ordinary slog attribute: a logger built without the component
// handler still prints it, it just does not filter on it. Tag a logger once, at
// the point the subsystem is constructed - calling this on an already-tagged
// logger resolves the level from the newer name but prints both attributes.
func Component(l *slog.Logger, name string) *slog.Logger {
	// nil in, nil out: a caller that was handed no logger keeps whatever
	// contract it already had, rather than silently gaining slog.Default.
	if l == nil {
		return nil
	}
	name = normalizeComponent(name)
	if name == "" {
		return l
	}
	return l.With(ComponentKey, name)
}

// componentHandler applies a per-component minimum severity on top of an inner
// handler.
//
// slog decides whether to build a record from Enabled(ctx, level) alone, which
// never sees attributes, so the inner handler is opened at the lowest level any
// component asks for and this wrapper does the real filtering. The component of
// a given handler instance is fixed by the WithAttrs call that tagged it, so
// resolution happens once per logger rather than once per record.
type componentHandler struct {
	inner  slog.Handler
	levels map[string]slog.Level
	root   slog.Level

	// component is the name inherited from WithAttrs, "" for an untagged logger.
	component string
	// min is the level resolved for component; records below it are dropped.
	min slog.Level
}

// newComponentHandler wraps inner with the level map from cfg.
func newComponentHandler(inner slog.Handler, cfg config.Logger) slog.Handler {
	levels := make(map[string]slog.Level, len(cfg.Levels))
	for _, entry := range cfg.Levels {
		if name := normalizeComponent(entry.Component); name != "" {
			levels[name] = levelOf(entry.Level)
		}
	}
	root := levelOf(cfg.Level)
	if len(levels) == 0 {
		// Nothing to scope: the inner handler's own threshold is the whole story.
		return inner
	}
	return &componentHandler{inner: inner, levels: levels, root: root, min: root}
}

func (h *componentHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.min
}

func (h *componentHandler) Handle(ctx context.Context, rec slog.Record) error {
	// Enabled is advisory: a caller holding the handler directly, or a record
	// replayed from elsewhere, can still arrive below the threshold.
	if rec.Level < h.min {
		return nil
	}
	return h.inner.Handle(ctx, rec)
}

func (h *componentHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.inner = h.inner.WithAttrs(attrs)
	for _, a := range attrs {
		if a.Key != ComponentKey {
			continue
		}
		if name := normalizeComponent(a.Value.String()); name != "" {
			next.component = name
			next.min = h.levelFor(name)
		}
	}
	return &next
}

func (h *componentHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	// A group nests subsequent attributes, so a component set after this point
	// is a different key entirely; the level resolved so far still applies.
	next := *h
	next.inner = h.inner.WithGroup(name)
	return &next
}

// levelFor resolves the minimum severity for a component: the longest
// configured prefix of its dotted path, falling back to the root level.
func (h *componentHandler) levelFor(component string) slog.Level {
	for name := component; name != ""; {
		if lvl, ok := h.levels[name]; ok {
			return lvl
		}
		dot := strings.LastIndexByte(name, '.')
		if dot < 0 {
			break
		}
		name = name[:dot]
	}
	return h.root
}

// normalizeComponent canonicalises a dotted component path the same way the
// config loader does, so a name written either side matches the other.
func normalizeComponent(name string) string {
	segments := strings.Split(strings.ToLower(strings.TrimSpace(name)), ".")
	kept := make([]string, 0, len(segments))
	for _, s := range segments {
		if s = strings.TrimSpace(s); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, ".")
}

// minLevel is the lowest severity any component asks for, and therefore the
// threshold the inner handler must be opened at for componentHandler to have
// anything left to filter.
func minLevel(cfg config.Logger) slog.Level {
	lowest := levelOf(cfg.Level)
	for _, entry := range cfg.Levels {
		if l := levelOf(entry.Level); l < lowest {
			lowest = l
		}
	}
	return lowest
}
