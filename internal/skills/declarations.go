package skills

// The skill marketplaces a workspace has. They are declared in two files of
// one shape (config.MarketplacesFile): the operator's
// <home>/marketplaces.json, and the project's .coddy/marketplaces.json, which
// arrives with the checkout. Beside both sits the system source, always in
// effect and always trusted. An entry is one of two kinds:
//
//   - a source, installed whole: every plugin it publishes (or every skill of
//     a repository without a marketplace.json) is installed and kept in sync;
//   - a marketplace, a catalog: its plugins are installed one by one with
//     `plugin install <plugin>@<name>`, and an update touches only those.
//
// A project entry takes effect only as skills.project_trust allows: under ask
// once the operator approved that exact entry for that workspace (a receipt
// in <home>/skills-trust.json, trust.go), under allow at once, under deny
// never. Whatever an entry installs goes to the managed dir,
// ${CODDY_HOME}/skills.

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// Where an entry is declared.
const (
	OriginSystem  = "system"  // built into Coddy (SystemSources)
	OriginHome    = "home"    // <home>/marketplaces.json
	OriginProject = "project" // <cwd>/.coddy/marketplaces.json
)

// What an entry is.
const (
	KindSource      = "source"      // installed whole
	KindMarketplace = "marketplace" // a catalog, plugins installed one by one
)

// The state of an entry for a workspace.
const (
	StateReady         = "ready"          // in effect
	StateNeedsApproval = "needs_approval" // a project entry under ask with no receipt
	StateDenied        = "denied"         // a project entry under deny
)

// Scopes an entry is written to.
const (
	ScopeGlobal = "global" // <home>/marketplaces.json
	ScopeLocal  = "local"  // <cwd>/.coddy/marketplaces.json
)

// Declaration is one source or marketplace declared for a workspace.
type Declaration struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"` // a marketplace's name
	Source string `json:"source"`
	Origin string `json:"origin"`
	// Path is the file the entry is declared in; empty for the system source.
	Path string `json:"source_path,omitempty"`
	// Gated says the workspace trust gate decides on the entry: a project one.
	Gated   bool   `json:"gated"`
	Trusted bool   `json:"trusted"`
	State   string `json:"status"`
	// Fingerprint is the digest an approval of the entry binds to.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Key names the entry on every surface: a marketplace by its name, a source
// by its address.
func (d Declaration) Key() string {
	if d.Kind == KindMarketplace && strings.TrimSpace(d.Name) != "" {
		return d.Name
	}
	return d.Source
}

// declarationsMu serialises the read-modify-write cycles of the declaration
// files within the process.
var declarationsMu sync.Mutex

// Declarations lists every source and marketplace declared for cwd, in the
// order they take effect: the system source, the home file, the project file
// (with its state under skills.project_trust). An entry that repeats one
// declared before it is left out: a project repeating the operator's source
// adds nothing, and a marketplace name the operator's file declares already
// stays the operator's. A declaration file that cannot be read is reported in
// errs and declares nothing; the rest still applies. An empty cwd reads no
// project file.
func Declarations(cfg *config.Config, cwd string) (decls []Declaration, errs []error) {
	migrateLegacyMarketplaceCache(cfg)
	add := func(d Declaration) {
		d.Source = strings.TrimSpace(d.Source)
		d.Name = strings.TrimSpace(d.Name)
		if d.Source == "" || declaredAlready(decls, d) {
			return
		}
		decls = append(decls, d)
	}
	for _, s := range SystemSources {
		add(Declaration{Kind: KindSource, Source: s, Origin: OriginSystem, Trusted: true, State: StateReady})
	}
	homePath := config.GlobalMarketplacesPath(cfg.Paths.Home)
	home, err := config.ReadMarketplacesFile(homePath)
	if err != nil {
		errs = append(errs, err)
	}
	for _, s := range home.Sources {
		add(Declaration{Kind: KindSource, Source: s, Origin: OriginHome, Path: homePath, Trusted: true, State: StateReady})
	}
	for _, m := range home.Marketplaces {
		add(Declaration{Kind: KindMarketplace, Name: m.Name, Source: m.Source, Origin: OriginHome, Path: homePath, Trusted: true, State: StateReady})
	}
	if strings.TrimSpace(cwd) == "" {
		return decls, errs
	}
	projectPath := config.ProjectMarketplacesPath(cwd)
	project, err := config.ReadMarketplacesFile(projectPath)
	if err != nil {
		errs = append(errs, err)
	}
	policy := cfg.Skills.ResolvedProjectTrust()
	store := NewTrustStore(cfg.Paths.Home)
	workspace := mcp.CanonicalWorkspace(cwd)
	gate := func(d Declaration) Declaration {
		d.Origin, d.Path, d.Gated = OriginProject, projectPath, true
		d.Fingerprint = declarationDigest(d.Kind, strings.TrimSpace(d.Name), strings.TrimSpace(d.Source))
		switch {
		case policy == config.ProjectTrustDeny:
			d.State = StateDenied
		case policy == config.ProjectTrustAllow || store.Approved(workspace, d.Fingerprint):
			d.Trusted, d.State = true, StateReady
		default:
			d.State = StateNeedsApproval
		}
		return d
	}
	for _, s := range project.Sources {
		add(gate(Declaration{Kind: KindSource, Source: s}))
	}
	for _, m := range project.Marketplaces {
		add(gate(Declaration{Kind: KindMarketplace, Name: m.Name, Source: m.Source}))
	}
	return decls, errs
}

// declaredAlready reports whether d repeats an entry of decls: a source the
// same source, a marketplace the same name or the same source.
func declaredAlready(decls []Declaration, d Declaration) bool {
	for _, e := range decls {
		if e.Kind != d.Kind {
			continue
		}
		if sameSource(e.Source, d.Source) {
			return true
		}
		if d.Kind == KindMarketplace && strings.EqualFold(e.Name, d.Name) {
			return true
		}
	}
	return false
}

// inEffect keeps the entries a sync may use.
func inEffect(decls []Declaration) []Declaration {
	out := make([]Declaration, 0, len(decls))
	for _, d := range decls {
		if d.State == StateReady {
			out = append(out, d)
		}
	}
	return out
}

// held keeps the project entries the trust gate holds back.
func held(decls []Declaration) []Declaration {
	var out []Declaration
	for _, d := range decls {
		if d.State != StateReady {
			out = append(out, d)
		}
	}
	return out
}

// FindDeclaration returns the entry key names for cwd (a marketplace by name,
// either kind by source), whatever its state; ok is false when none does.
func FindDeclaration(cfg *config.Config, cwd, key string) (Declaration, bool) {
	decls, _ := Declarations(cfg, cwd)
	for _, d := range decls {
		if keyNames(key, d.Kind, d.Name, d.Source) {
			return d, true
		}
	}
	return Declaration{}, false
}

// ErrDeclarationChanged is returned by ApproveShown when the project entry
// on disk is no longer the one the operator was shown.
var ErrDeclarationChanged = errors.New("the declaration changed since it was shown")

// ApproveShown records the operator's approval of a project entry for the
// workspace of cwd. fingerprint is the digest of the entry the operator was
// shown; when it no longer matches the file, nothing is recorded and the
// error wraps ErrDeclarationChanged. An empty fingerprint approves the entry
// as it is now, for a client that shows it and approves it in one step.
//
// key may name entries of several files (a project source at the address of
// one of the operator's catalogs is listed on its own): the project entry is
// the one approved, the one with that fingerprint when one is given.
func ApproveShown(cfg *config.Config, cwd, key, fingerprint string) (Declaration, error) {
	decls, _ := Declarations(cfg, cwd)
	fp := strings.TrimSpace(fingerprint)
	var first, project *Declaration
	for i := range decls {
		d := &decls[i]
		if !keyNames(key, d.Kind, d.Name, d.Source) {
			continue
		}
		if first == nil {
			first = d
		}
		if d.Origin != OriginProject {
			continue
		}
		if fp != "" && d.Fingerprint == fp {
			project = d
			break
		}
		if project == nil {
			project = d
		}
	}
	if first == nil {
		return Declaration{}, fmt.Errorf("no source or marketplace %q is declared for %s", key, cwd)
	}
	if project == nil {
		return *first, fmt.Errorf("%q comes from %s, which needs no approval", key, originWord(*first))
	}
	d := *project
	if cfg.Skills.ResolvedProjectTrust() == config.ProjectTrustDeny {
		return d, fmt.Errorf("project marketplaces are switched off by skills.project_trust: deny")
	}
	if fp != "" && fp != d.Fingerprint {
		return d, fmt.Errorf("%s: %w; review it and approve again", key, ErrDeclarationChanged)
	}
	if err := NewTrustStore(cfg.Paths.Home).Approve(mcp.CanonicalWorkspace(cwd), d); err != nil {
		return d, err
	}
	d.Trusted, d.State = true, StateReady
	return d, nil
}

// Revoke withdraws the approval of the project entry key names for the
// workspace of cwd and reports whether one existed.
func Revoke(cfg *config.Config, cwd, key string) (bool, error) {
	return NewTrustStore(cfg.Paths.Home).Revoke(mcp.CanonicalWorkspace(cwd), key)
}

func originWord(d Declaration) string {
	switch d.Origin {
	case OriginSystem:
		return "Coddy itself"
	case OriginHome:
		return d.Path
	default:
		return d.Path
	}
}

// declarationPath is the file a scope writes.
func declarationPath(cfg *config.Config, cwd, scope string) (string, error) {
	switch strings.TrimSpace(scope) {
	case "", ScopeGlobal:
		return config.GlobalMarketplacesPath(cfg.Paths.Home), nil
	case ScopeLocal:
		if strings.TrimSpace(cwd) == "" {
			return "", fmt.Errorf("a project marketplace needs a workspace")
		}
		return config.ProjectMarketplacesPath(cwd), nil
	default:
		return "", fmt.Errorf("unknown scope %q (use %q or %q)", scope, ScopeGlobal, ScopeLocal)
	}
}

// updateDeclarations runs one read-modify-write cycle of a declaration file;
// change reports whether it changed anything, and only then is it written.
func updateDeclarations(path string, change func(*config.MarketplacesFile) (bool, error)) (bool, error) {
	declarationsMu.Lock()
	defer declarationsMu.Unlock()
	file, err := config.ReadMarketplacesFile(path)
	if err != nil {
		return false, err
	}
	changed, err := change(&file)
	if err != nil || !changed {
		return false, err
	}
	return true, config.WriteMarketplacesFile(path, file)
}

// declareSource adds source to the sources of the file at path, unless one
// of its sources is the same source already.
func declareSource(path, source string) (bool, error) {
	return updateDeclarations(path, func(f *config.MarketplacesFile) (bool, error) {
		for _, s := range f.Sources {
			if sameSource(s, source) {
				return false, nil
			}
		}
		f.Sources = append(f.Sources, source)
		return true, nil
	})
}

// declareMarketplace adds a marketplace to the file at path, or updates the
// source of the one it lists under that name. A name it declares from
// another source is an error.
func declareMarketplace(path, name, source string) (bool, error) {
	return updateDeclarations(path, func(f *config.MarketplacesFile) (bool, error) {
		for i, m := range f.Marketplaces {
			switch {
			case strings.EqualFold(m.Name, name) && sameSource(m.Source, source):
				return false, nil
			case strings.EqualFold(m.Name, name):
				return false, fmt.Errorf("a marketplace named %q is added already, from %s (%s); remove it with `plugin marketplace remove %s` to add this one", name, m.Source, path, name)
			case sameSource(m.Source, source):
				// The marketplace changed its name since it was added.
				f.Marketplaces[i].Name = name
				return true, nil
			}
		}
		f.Marketplaces = append(f.Marketplaces, config.DeclaredMarketplace{Name: name, Source: source})
		return true, nil
	})
}

// undeclare removes from the file at path every source and marketplace key
// names, returning what it removed. A marketplace removed by its name takes a
// source of the same address with it, or the next sync would install every
// plugin of it again.
func undeclare(path, key string) ([]config.DeclaredMarketplace, []string, error) {
	var gone []config.DeclaredMarketplace
	var goneSources []string
	_, err := updateDeclarations(path, func(f *config.MarketplacesFile) (bool, error) {
		keptMarkets := f.Marketplaces[:0:0]
		for _, m := range f.Marketplaces {
			if keyNames(key, KindMarketplace, m.Name, m.Source) {
				gone = append(gone, m)
				continue
			}
			keptMarkets = append(keptMarkets, m)
		}
		keptSources := f.Sources[:0:0]
		for _, s := range f.Sources {
			named := keyNames(key, KindSource, "", s)
			for _, m := range gone {
				named = named || sameSource(m.Source, s)
			}
			if named {
				goneSources = append(goneSources, s)
				continue
			}
			keptSources = append(keptSources, s)
		}
		if len(gone) == 0 && len(goneSources) == 0 {
			return false, nil
		}
		f.Sources, f.Marketplaces = keptSources, keptMarkets
		return true, nil
	})
	return gone, goneSources, err
}
