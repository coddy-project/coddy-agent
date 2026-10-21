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
// with, the one nearest the workspace first, so where an address names a rule
// in two places its owner nearer the workspace wins - the workspace's own rule
// folder over a subproject, db's ops/deploy over the subproject db/ops's
// deploy. The completion lists rows in the same order. A name with no "/" that
// no rule has whole finds the rule whose last segment it is, when exactly one
// has it.
func ruleByAddress(cfg *config.Config, cwd string, catalog []*rules.Rule, address string) *rules.Rule {
	if !ruleAddressSpelling(address) {
		return nil
	}
	if r := ruleNamed(catalog, address, nil); r != nil {
		return r
	}
	segs := strings.Split(address, "/")
	for k := 1; k < len(segs); k++ {
		if r := ruleNamed(SubfolderRules(cfg, cwd, strings.Join(segs[:k], "/")), strings.Join(segs[k:], "/"), nil); r != nil {
			return r
		}
	}
	return ruleByLastSegment(catalog, address, nil)
}

// ruleNamed is the first rule of rs named name (the case of an ASCII letter
// does not matter) that keep admits.
func ruleNamed(rs []*rules.Rule, name string, keep func(*rules.Rule) bool) *rules.Rule {
	for _, r := range rs {
		if r != nil && (keep == nil || keep(r)) && asciiEqualFold(r.CanonicalName(), name) {
			return r
		}
	}
	return nil
}

// asciiEqualFold reports whether a and b are equal once their ASCII letters
// are lowercased. An address is ASCII, so only such a name can match one:
// strings.EqualFold would also fold the Kelvin sign into "k" and let a rule
// no reference can spell take an address another rule answers to.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
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
		if r == nil || (keep != nil && !keep(r)) || !asciiEqualFold(r.BaseName(), name) {
			continue
		}
		if found != nil && !asciiEqualFold(found.CanonicalName(), r.CanonicalName()) {
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
