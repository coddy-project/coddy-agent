package subagents

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// CatalogEntry is one definition as the catalog shows it, with the trust
// decision for a given workspace attached.
type CatalogEntry struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Scope       Scope      `json:"scope"`
	Path        string     `json:"path,omitempty"`
	Digest      string     `json:"digest,omitempty"`
	Model       string     `json:"model,omitempty"`
	Mode        string     `json:"mode,omitempty"`
	Builtin     bool       `json:"builtin"`
	Hidden      bool       `json:"hidden"`
	Trust       TrustState `json:"trust"`
	// Trusted and NeedsApproval mirror Trust as booleans for clients that
	// switch on them (the SPA, scripts).
	Trusted       bool `json:"trusted"`
	NeedsApproval bool `json:"needs_approval"`
}

// BuildCatalog decides trust for every definition and returns the entries
// sorted by name. Hidden definitions are included with their flag set: the
// operator's listing shows everything, only the model-facing block omits them.
func BuildCatalog(defs []*Definition, policy, workspace string, store *TrustStore) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		trust := Decide(d, policy, workspace, store)
		out = append(out, CatalogEntry{
			Name:          d.Name,
			Description:   d.Description,
			Scope:         d.Scope,
			Path:          d.Path,
			Digest:        d.Digest,
			Model:         d.Model,
			Mode:          d.Mode,
			Builtin:       d.Builtin,
			Hidden:        d.Hidden,
			Trust:         trust,
			Trusted:       trust == TrustTrusted,
			NeedsApproval: trust == TrustNeedsApproval,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PromptBlock renders the catalog the parent model reads, plus guidance on
// when delegation pays off. Hidden definitions are omitted. An unapproved
// project definition is listed by name with a static approval notice and
// nothing the file authored: until the operator approves it, its text must not
// reach the parent's prompt at all, or an untrusted checkout could steer the
// parent before any approval.
func PromptBlock(entries []CatalogEntry) string {
	visible := make([]CatalogEntry, 0, len(entries))
	for _, e := range entries {
		if !e.Hidden {
			visible = append(visible, e)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Subagents\n\n")
	b.WriteString("You can delegate a bounded, self-contained piece of work to a subagent with the **`spawn_agent`** tool: ")
	b.WriteString("pass the agent name and a prompt that carries everything the child needs, because it starts with an empty context and sees none of this conversation. ")
	b.WriteString("Only its final report comes back to you; the user does not see it, so repeat what matters in your own reply. ")
	b.WriteString("Delegate when the work would flood this context (a long investigation, reviewing several modules, researching several options) or when independent pieces can run in parallel with **`background: true`**; ")
	b.WriteString("collect detached runs with **`background_wait`** or **`background_output`** and stop them with **`background_stop`**. Do not delegate a one-step task you can do directly.\n\n")
	b.WriteString("Available subagents:\n\n")
	for _, e := range visible {
		if e.NeedsApproval {
			fmt.Fprintf(&b, "- `%s`: project definition awaiting approval; its description is withheld and spawning it is refused until the user approves it on the machine running coddy (`coddy agents trust %s` there, or POST /coddy/subagents/%s/trust with the session workspace as cwd)\n", e.Name, e.Name, e.Name)
			continue
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", e.Name, strings.Join(strings.Fields(e.Description), " "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// WriteListing prints the operator-facing table used by `coddy agents list`.
func WriteListing(w io.Writer, entries []CatalogEntry) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tSCOPE\tTRUST\tFLAGS\tDESCRIPTION\tPATH")
	for _, e := range entries {
		var flags []string
		if e.Hidden {
			flags = append(flags, "hidden")
		}
		if e.Model != "" {
			flags = append(flags, "model="+e.Model)
		}
		if e.Mode != "" {
			flags = append(flags, "mode="+e.Mode)
		}
		path := e.Path
		if e.Builtin {
			path = "(embedded)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, e.Scope, e.Trust, strings.Join(flags, ","), e.Description, path)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "(total %d)\n", len(entries))
}
