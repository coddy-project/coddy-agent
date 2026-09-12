package prompts

import (
	"embed"
	"strings"
)

// Prompt section fragments (data-driven variant assembly).
//
// The built-in prompt for each mode is assembled from small Markdown section
// fragments stored under sections/. Once the caller supplies variant keys, the
// assembler resolves their manifests and fragments without enumerating them.
//
// Layout and naming conventions (path prefixes are relative to sections/):
//
//	<mode>/manifest            Base structure: section IDs, one per line.
//	<mode>/manifest.<variant>  Per-variant structure override (optional).
//	<mode>/<id>.md             Shared fragment for section <id>.
//	<mode>/<id>_<variant>.md   Variant-specific fragment override (optional).
//
// <mode> is one of the session modes: agent, plan, ask.
// <variant> is any model family or per-model slug resolved by the caller
// (for example "anthropic", "gemma", "gpt-oss", or a filename-safe model
// slug like "gpt-oss-20b"). No variant name is enumerated here.
//
// Resolution for a (mode, variants) render, where variants is most-specific
// first (e.g. [model-reference-slug, API-model-slug, family]):
//
//  1. Structure: the first variant that has a <mode>/manifest.<variant> file
//     supplies the section ID list; otherwise <mode>/manifest (the base) is used.
//  2. Content: for each section ID, the first existing fragment is used, trying
//     <mode>/<id>_<variant>.md for each variant in order, then <mode>/<id>.md.
//     A section ID that resolves to no file is silently skipped, so the base
//     manifest can list an optional "notes" slot that is only filled when a
//     variant provides notes_<variant>.md.
//
// Fragments are trimmed and joined with a blank line. A fragment that is all
// conditional (the subagent role, the footer blocks) opens its actions with
// {{- so an empty block leaves no run of blank lines behind.
//
// Custom on-disk prompts (config.Prompts dir) keep the one-file-per-mode shape
// and bypass this assembly entirely (see loadSource in loader.go).

// sectionFS holds every section fragment and manifest. Embedding the directory
// pulls in the whole tree recursively, so adding a subfolder needs no change.
//
//go:embed sections
var sectionFS embed.FS

// sectionSource reads a section fragment by its path within sections/ (for
// example "agent/header") and returns it trimmed of surrounding whitespace.
// Missing fragments return ("", false).
func sectionSource(name string) (string, bool) {
	b, err := sectionFS.ReadFile("sections/" + name + ".md")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// sectionExists reports whether a section fragment is present in the embed.
func sectionExists(name string) bool {
	_, ok := sectionSource(name)
	return ok
}

// assembleEmbeddedSource concatenates the section fragments resolved for
// (mode, variants) into a single template source string.
func assembleEmbeddedSource(mode string, variants []string) string {
	fragments := resolveFragments(mode, variants)
	parts := make([]string, 0, len(fragments))
	for _, f := range fragments {
		s, _ := sectionSource(f)
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

// resolveFragments returns the ordered fragment paths (within sections/) for
// (mode, variants): structure from a manifest, content resolved per section.
// Fragment lookup uses the effective mode (the one whose manifest was selected),
// so an unknown mode that falls back to the agent manifest also resolves agent
// fragments.
func resolveFragments(mode string, variants []string) []string {
	effectiveMode, ids := manifestFor(mode, variants)
	fragments := make([]string, 0, len(ids))
	for _, id := range ids {
		if f := resolveFragment(effectiveMode, id, variants); f != "" {
			fragments = append(fragments, f)
		}
	}
	return fragments
}

// manifestFor resolves the (effective mode, section IDs) for (mode, variants).
// The first variant that ships a per-variant manifest wins, otherwise the base
// manifest. Unknown modes fall back to the agent structure. The returned mode is
// the one whose manifest and fragment directory were selected.
func manifestFor(mode string, variants []string) (string, []string) {
	for _, v := range variants {
		if ids := readManifest(mode, v); ids != nil {
			return mode, ids
		}
	}
	if ids := readManifest(mode, ""); ids != nil {
		return mode, ids
	}
	if mode != "agent" {
		return manifestFor("agent", variants)
	}
	return mode, nil
}

// readManifest reads sections/<mode>/manifest[.<variant>]. A nil slice means
// "no such manifest file". Blank lines and '#' comment lines are ignored; every
// other non-empty line is a section ID (order is significant).
func readManifest(mode, variant string) []string {
	path := "sections/" + mode + "/manifest"
	if variant != "" {
		path += "." + variant
	}
	b, err := sectionFS.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseManifest(string(b))
}

func parseManifest(src string) []string {
	var ids []string
	for _, line := range strings.Split(src, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		ids = append(ids, l)
	}
	return ids
}

// resolveFragment picks the fragment path for section id within (mode, variants):
// the first existing <mode>/<id>_<variant>.md across variants (most-specific
// first), then the shared <mode>/<id>.md. It returns "" when neither exists, so
// the manifest can list optional slots (for example "notes") that are only
// rendered when a variant supplies them.
func resolveFragment(mode, id string, variants []string) string {
	for _, v := range variants {
		f := mode + "/" + id + "_" + v
		if sectionExists(f) {
			return f
		}
	}
	f := mode + "/" + id
	if sectionExists(f) {
		return f
	}
	return ""
}
