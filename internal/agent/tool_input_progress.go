package agent

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// toolInputProgress observes arguments; it never validates or executes them.
// Only the three file-writing tools expose a bounded, decoded draft preview.
// Scanning each byte once avoids re-parsing an ever-growing JSON document.
type toolInputProgress struct {
	field                                      string
	depth                                      int
	inString, keyString, escaped               bool
	key, token, escape, path, tail             string
	high                                       rune
	bytes, lines, argumentBytes, argumentRunes int
	last                                       time.Time
}

func newToolInputProgress(name string) *toolInputProgress {
	p := &toolInputProgress{}
	switch name {
	case "write":
		p.field = "content"
	case "edit":
		p.field = "newString"
	case "apply_patch":
		p.field = "patch"
	}
	return p
}

func (p *toolInputProgress) add(delta string) {
	p.argumentBytes += len(delta)
	p.argumentRunes += utf8.RuneCountInString(delta)
	if p.field == "" {
		return
	}
	for i := 0; i < len(delta); i++ {
		c := delta[i]
		if !p.inString {
			switch c {
			case '{', '[':
				p.depth++
			case '}', ']':
				p.depth--
			case ',':
				if p.depth == 1 {
					p.key = ""
				}
			case '"':
				p.inString = true
				p.keyString = p.depth == 1 && p.key == ""
				p.token = ""
			}
			continue
		}
		if p.escaped {
			p.escape += string(c)
			if len(p.escape) == 1 && c == 'u' {
				continue
			}
			if p.escape[0] == 'u' && len(p.escape) < 5 {
				continue
			}
			if p.escape[0] == 'u' {
				if n, err := strconv.ParseUint(p.escape[1:], 16, 16); err == nil {
					p.unicode(rune(n))
				}
			} else if p.escape == "/" {
				p.decoded("/")
			} else {
				if s, err := strconv.Unquote(`"\` + p.escape + `"`); err == nil {
					p.decoded(s)
				}
			}
			p.escaped = false
			p.escape = ""
			continue
		}
		switch c {
		case '\\':
			p.escaped = true
		case '"':
			p.flushHigh()
			p.inString = false
			if p.keyString {
				p.key = p.token
			} else if p.depth == 1 && p.key == "path" {
				p.path = p.token
			}
		default:
			end := strings.IndexAny(delta[i:], "\"\\")
			if end < 0 {
				end = len(delta) - i
			}
			p.decoded(delta[i : i+end])
			i += end - 1
		}
	}
}

func (p *toolInputProgress) unicode(r rune) {
	if p.high != 0 && r >= 0xdc00 && r <= 0xdfff {
		h := p.high
		p.high = 0
		p.decoded(string(utf16.DecodeRune(h, r)))
		return
	}
	p.flushHigh()
	if r >= 0xd800 && r <= 0xdbff {
		p.high = r
		return
	}
	if r >= 0xdc00 && r <= 0xdfff {
		r = utf8.RuneError
	}
	p.decoded(string(r))
}

func (p *toolInputProgress) flushHigh() {
	if p.high != 0 {
		p.high = 0
		p.decoded(string(utf8.RuneError))
	}
}

func (p *toolInputProgress) decoded(s string) {
	p.flushHigh()
	if p.depth != 1 {
		return
	}
	if p.keyString || p.key == "path" {
		if len(p.token) < 512 {
			take := min(len(s), 512-len(p.token))
			p.token += strings.Clone(s[:take])
		}
		return
	}
	if p.key != p.field {
		return
	}
	if p.bytes == 0 && s != "" {
		p.lines = 1
	}
	p.bytes += len(s)
	p.lines += strings.Count(s, "\n")
	if len(s) >= 1024 {
		p.tail = strings.Clone(s[len(s)-1024:])
	} else {
		if len(p.tail)+len(s) > 1024 {
			p.tail = p.tail[len(p.tail)+len(s)-1024:]
		}
		p.tail += s
	}
}

func (p *toolInputProgress) update(id string, now time.Time, force bool) *acp.ToolCallStatusUpdate {
	if p.field == "" || p.argumentBytes == 0 || (!force && !p.last.IsZero() && now.Sub(p.last) < 200*time.Millisecond) {
		return nil
	}
	p.last = now
	tail := strings.ToValidUTF8(p.tail, "")
	lines := strings.Split(tail, "\n")
	if len(lines) > 6 {
		tail = strings.Join(lines[len(lines)-6:], "\n")
	}
	return &acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "pending",
		Meta: map[string]interface{}{"coddy": map[string]interface{}{"toolInputProgress": map[string]interface{}{
			"path": strings.ToValidUTF8(p.path, ""), "bytes": p.bytes, "lines": p.lines, "argumentBytes": p.argumentBytes, "preview": tail,
		}}},
	}
}
