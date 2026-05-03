package logger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Record is a parsed log line with the standard slog fields plus arbitrary
// attributes. Time is zero if the line did not carry a "time=..." attribute
// the parser could read.
type Record struct {
	Time    time.Time
	Level   string
	Message string
	Attrs   map[string]string
}

// Filter selects matching records for ParseFile. All fields are optional.
type Filter struct {
	// Attrs require these key=value pairs to be present (exact match).
	Attrs map[string]string
	// Since drops records strictly older than this timestamp. Zero disables.
	Since time.Time
	// Limit caps the number of records returned. Zero or negative is unlimited.
	Limit int
}

// ParseFile reads path line by line, parses each line as a slog Text or JSON
// record, applies filter and returns the matching records in file order.
//
// It tolerates malformed lines (skipped silently) so mixing log formats does
// not break the consumer.
func ParseFile(path string, f Filter) ([]Record, error) {
	fh, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("logger: open %s: %w", path, err)
	}
	defer fh.Close()

	return ParseReader(fh, f)
}

// ParseReader parses log records from r. See ParseFile.
func ParseReader(r io.Reader, f Filter) ([]Record, error) {
	out := []Record{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		rec, ok := parseLine(line)
		if !ok {
			continue
		}
		if !matches(rec, f) {
			continue
		}
		out = append(out, rec)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("logger: scan: %w", err)
	}
	return out, nil
}

func matches(r Record, f Filter) bool {
	if !f.Since.IsZero() && r.Time.Before(f.Since) {
		return false
	}
	for k, v := range f.Attrs {
		got, ok := r.Attrs[k]
		if !ok || got != v {
			return false
		}
	}
	return true
}

// parseLine returns the parsed record and ok=true on success.
func parseLine(line string) (Record, bool) {
	if strings.HasPrefix(line, "{") {
		return parseJSONLine(line)
	}
	return parseTextLine(line)
}

func parseJSONLine(line string) (Record, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Record{}, false
	}
	rec := Record{Attrs: map[string]string{}}
	for k, v := range raw {
		switch k {
		case "time":
			if s, ok := v.(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					rec.Time = t
				}
			}
		case "level":
			if s, ok := v.(string); ok {
				rec.Level = strings.ToLower(s)
			}
		case "msg":
			if s, ok := v.(string); ok {
				rec.Message = s
			}
		default:
			rec.Attrs[k] = fmt.Sprint(v)
		}
	}
	return rec, true
}

// parseTextLine implements a small parser for slog's TextHandler output.
//
// The TextHandler writes "key=value" pairs separated by spaces. Quoted
// strings can contain spaces and use backslash escaping.
func parseTextLine(line string) (Record, bool) {
	rec := Record{Attrs: map[string]string{}}
	for _, kv := range tokenize(line) {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := kv[:eq]
		val := unquote(kv[eq+1:])
		switch key {
		case "time":
			if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
				rec.Time = t
			} else if t, err := time.Parse(time.RFC3339, val); err == nil {
				rec.Time = t
			}
		case "level":
			rec.Level = strings.ToLower(val)
		case "msg":
			rec.Message = val
		default:
			rec.Attrs[key] = val
		}
	}
	if rec.Level == "" && rec.Message == "" {
		// Looked nothing like a slog record.
		return rec, false
	}
	return rec, true
}

// tokenize splits a slog text line into tokens, respecting quoted strings.
func tokenize(line string) []string {
	out := []string{}
	var b strings.Builder
	inQuote := false
	escape := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case escape:
			b.WriteByte(c)
			escape = false
		case c == '\\' && inQuote:
			b.WriteByte(c)
			escape = true
		case c == '"':
			b.WriteByte(c)
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// unquote strips one layer of double quotes if present and unescapes
// backslashed characters; otherwise returns the string as-is.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var b strings.Builder
		escape := false
		for i := 1; i < len(s)-1; i++ {
			c := s[i]
			if escape {
				switch c {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				default:
					b.WriteByte(c)
				}
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			b.WriteByte(c)
		}
		return b.String()
	}
	return s
}
