package mention

import (
	"bytes"
	"encoding/xml"
	"html"
	"path"
	"regexp"
	"strings"
)

// Attachment kinds: what a resolved mention put into the message.
const (
	KindFile      = "file"
	KindDirectory = "directory"
	KindSession   = "session"
	KindRule      = "rule"
	KindAgent     = "agent"
	KindPlan      = "plan"
	// KindSkill carries the body of a skill the message invoked as /name.
	KindSkill = "skill"
)

// Tag is the element a resolved mention is written as inside the user message.
const (
	attachmentOpenTag  = "<coddy_attachment"
	attachmentCloseTag = "</coddy_attachment>"
	cdataOpen          = "<![CDATA["
	cdataClose         = "]]>"
)

// Attachment is one resolved mention as the model reads it.
type Attachment struct {
	// Kind is one of the Kind* constants; empty means a file.
	Kind string
	// Path is the reference the model acts on: a workspace-relative or
	// absolute path, or "session:<id>" / "agent:<name>" for a meta mention.
	Path string
	// Name is the short label: a file's base name, a session title.
	Name string
	// Typed is the mention as the user wrote it, without the "@", when it
	// differs from Path ("~/notes.md" for /home/u/notes.md). A transcript
	// collapses the block back to it.
	Typed string
	// Lines is the range a ranged file mention was narrowed to.
	Lines Range
	// Body is the text the model reads.
	Body string
}

// XML renders the attachment as a <coddy_attachment> element with the body
// in CDATA, split where the body itself holds "]]>".
func (a Attachment) XML() string {
	name := a.Name
	if name == "" {
		name = path.Base(strings.TrimSuffix(a.Path, "/"))
		if name == "." || name == "/" || name == "" {
			name = a.Path
		}
	}
	var b strings.Builder
	b.WriteString(attachmentOpenTag)
	writeAttr(&b, "path", a.Path)
	writeAttr(&b, "name", name)
	if !a.Lines.IsZero() {
		writeAttr(&b, "lines", rangeKey(a.Lines))
	}
	if a.Kind != "" && a.Kind != KindFile {
		writeAttr(&b, "kind", a.Kind)
	}
	if a.Typed != "" && a.Typed != a.Path {
		writeAttr(&b, "mention", a.Typed)
	}
	b.WriteString(">\n")
	b.WriteString(cdataOpen)
	b.WriteString(strings.ReplaceAll(a.Body, cdataClose, "]]]]><![CDATA[>"))
	b.WriteString(cdataClose)
	b.WriteString("\n")
	b.WriteString(attachmentCloseTag)
	return b.String()
}

func writeAttr(b *strings.Builder, key, value string) {
	b.WriteByte(' ')
	b.WriteString(key)
	b.WriteString(`="`)
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(value))
	b.Write(buf.Bytes())
	b.WriteByte('"')
}

var (
	attrPathRE    = regexp.MustCompile(`\bpath="([^"]*)"`)
	attrLinesRE   = regexp.MustCompile(`\blines="([0-9]{1,9})-([0-9]{1,9})"`)
	attrMentionRE = regexp.MustCompile(`\bmention="([^"]*)"`)
	attrKindRE    = regexp.MustCompile(`\bkind="([^"]*)"`)
)

// Block is one <coddy_attachment> element found in a message.
type Block struct {
	Start, End int
	Path       string
	Typed      string
	// Kind is the kind attribute, KindFile when the element has none.
	Kind  string
	Lines Range
}

// Blocks returns the attachment elements of s in order. The body is one or
// more CDATA sections, walked before the closing tag is looked for, so a file
// that itself contains "</coddy_attachment>" cannot end its block early. An
// opening tag without a well-formed block is ordinary text.
func Blocks(s string) []Block {
	var out []Block
	for from := 0; from < len(s); {
		start := strings.Index(s[from:], attachmentOpenTag)
		if start < 0 {
			break
		}
		start += from
		after := s[start+len(attachmentOpenTag):]
		tagEnd := strings.IndexByte(after, '>')
		if tagEnd < 0 || (after[0] != '>' && after[0] != ' ' && after[0] != '\t' && after[0] != '\n') {
			from = start + len(attachmentOpenTag)
			continue
		}
		attrs := after[:tagEnd]
		body := after[tagEnd+1:]
		end := bodyEnd(body)
		if end < 0 {
			from = start + len(attachmentOpenTag)
			continue
		}
		blk := Block{Start: start, End: start + len(attachmentOpenTag) + tagEnd + 1 + end, Kind: KindFile}
		if m := attrKindRE.FindStringSubmatch(attrs); m != nil && m[1] != "" {
			blk.Kind = html.UnescapeString(m[1])
		}
		if m := attrPathRE.FindStringSubmatch(attrs); m != nil {
			blk.Path = html.UnescapeString(m[1])
		}
		if m := attrMentionRE.FindStringSubmatch(attrs); m != nil {
			blk.Typed = html.UnescapeString(m[1])
		}
		if m := attrLinesRE.FindStringSubmatch(attrs); m != nil {
			s0, e0 := atoiSmall(m[1]), atoiSmall(m[2])
			if s0 >= 1 && e0 >= s0 {
				blk.Lines = Range{Start: s0, End: e0}
			}
		}
		out = append(out, blk)
		from = blk.End
	}
	return out
}

// bodyEnd returns the offset just past the closing tag of one body, skipping
// CDATA sections, or -1 when the block is unterminated.
func bodyEnd(body string) int {
	i := 0
	for {
		j := i
		for j < len(body) && (body[j] == ' ' || body[j] == '\t' || body[j] == '\n' || body[j] == '\r') {
			j++
		}
		if strings.HasPrefix(body[j:], cdataOpen) {
			k := strings.Index(body[j+len(cdataOpen):], cdataClose)
			if k < 0 {
				return -1
			}
			i = j + len(cdataOpen) + k + len(cdataClose)
			continue
		}
		if strings.HasPrefix(body[j:], attachmentCloseTag) {
			return j + len(attachmentCloseTag)
		}
		k := strings.Index(body[i:], attachmentCloseTag)
		if k < 0 {
			return -1
		}
		return i + k + len(attachmentCloseTag)
	}
}

func atoiSmall(s string) int {
	v := 0
	for _, c := range s {
		v = v*10 + int(c-'0')
	}
	return v
}

// ForDisplay is what a surface shows for a persisted user message: every
// attachment element collapsed to its mention ("@src/app.go:21-31"), or
// dropped when the text before it already carries that mention, as it does
// for everything the user typed. The web UI does the same in
// stripCoddyAttachments.ts.
func ForDisplay(s string) string {
	blocks := Blocks(s)
	if len(blocks) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, blk := range blocks {
		b.WriteString(s[last:blk.Start])
		last = blk.End
		label := blk.Typed
		if (label == "" && blk.Kind == KindRule) || blk.Kind == KindSkill {
			// A rule a mentioned path pulled in, or the body of a /skill the
			// text already names: nothing the user typed is missing.
			continue
		}
		if label == "" {
			label = blk.Path
		}
		if label == "" {
			continue
		}
		token := "@" + label
		if !blk.Lines.IsZero() {
			token += ":" + rangeKey(blk.Lines)
		}
		if mentionedBefore(s[:blocks[0].Start], label, blk.Lines) {
			continue
		}
		b.WriteString(token)
	}
	b.WriteString(s[last:])
	return strings.TrimRight(collapseBlankRuns(b.String()), " \t\n")
}

// mentionedBefore reports whether text already holds a mention of label with
// the same range, in any of the spellings Parse reads.
func mentionedBefore(text, label string, lines Range) bool {
	want := strings.TrimSuffix(strings.ReplaceAll(label, `\`, "/"), "/")
	for _, tok := range Parse(text) {
		switch {
		case tok.URL != "":
			if tok.URL == label {
				return true
			}
		case !tok.IsPath():
			if string(tok.Scheme)+":"+tok.Ref == label {
				return true
			}
		default:
			for _, r := range tok.Readings {
				got := strings.TrimSuffix(strings.ReplaceAll(r.Path, `\`, "/"), "/")
				if got == want && r.Range == lines {
					return true
				}
			}
		}
	}
	return false
}

func collapseBlankRuns(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
