package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// A rule is named in a prompt by its address: its name - the path of its file
// inside the rule folder it was read from, without the extension
// ("frontend/style") - and, for a rule of a subproject's own .coddy/rules, the
// subproject's folder in front of it ("db/ops/deploy"). The workspace's rule
// folder and the rule folders of its subprojects make one tree of addresses,
// which "@rule:" names a rule of and its completion walks.

// ruleByAddress finds the rule "@rule:<address>" names: a rule of the catalog
// with that name first, then a rule of the subprojects the address can start
// with, the deepest first, so where an address names a rule in two places the
// workspace's own rule folder wins. A name with no "/" that no rule has whole
// finds the rule whose last segment it is, when exactly one has it.
func ruleByAddress(cfg *config.Config, cwd string, catalog []*rules.Rule, address string) *rules.Rule {
	if !ruleAddressSpelling(address) {
		return nil
	}
	if r := ruleNamed(catalog, address, nil); r != nil {
		return r
	}
	segs := strings.Split(address, "/")
	for k := len(segs) - 1; k >= 1; k-- {
		if r := ruleNamed(SubfolderRules(cfg, cwd, strings.Join(segs[:k], "/")), strings.Join(segs[k:], "/"), nil); r != nil {
			return r
		}
	}
	return ruleByLastSegment(catalog, address, nil)
}

// ruleNamed is the first rule of rs named name (case does not matter) that
// keep admits.
func ruleNamed(rs []*rules.Rule, name string, keep func(*rules.Rule) bool) *rules.Rule {
	for _, r := range rs {
		if r != nil && (keep == nil || keep(r)) && strings.EqualFold(r.CanonicalName(), name) {
			return r
		}
	}
	return nil
}

// ruleByLastSegment is the one rule of rs, keep admitting, whose last name
// segment is name, for a name with no "/": nil when none has it or several
// do, so a bare name never picks one of two rules for the user.
func ruleByLastSegment(rs []*rules.Rule, name string, keep func(*rules.Rule) bool) *rules.Rule {
	if strings.Contains(name, "/") {
		return nil
	}
	var found *rules.Rule
	for _, r := range rs {
		if r == nil || (keep != nil && !keep(r)) || !strings.EqualFold(r.BaseName(), name) {
			continue
		}
		if found != nil && !strings.EqualFold(found.CanonicalName(), r.CanonicalName()) {
			return nil
		}
		if found == nil {
			found = r
		}
	}
	return found
}

// ruleAddressSpelling reports whether address is written the one way a
// reference names a rule or a folder: segments a reference can spell,
// separated by single slashes, none of them "." or ".." and none hidden, so
// "/etc", "./db" and "db//sub" name nothing rather than a folder they clean
// to.
func ruleAddressSpelling(address string) bool {
	for _, seg := range strings.Split(address, "/") {
		if !mention.RefName(seg) || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}
