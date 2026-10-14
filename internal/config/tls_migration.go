package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const tlsMigrationFix = "Move incoming TLS/mTLS to an external reverse proxy and restrict access to the HTTP backend; install private CAs in the system trust store and use an external transport for outgoing client certificates. Then explicitly remove this key. Keep HTTPS URLs and proxy settings unchanged; do not disable certificate verification."

type tlsMigrationError struct{ findings []Finding }

func (e *tlsMigrationError) Error() string {
	var out []string
	for _, f := range e.findings {
		out = append(out, fmt.Sprintf("%s:%d:%d: removed TLS setting: %s", f.Path, f.Line, f.Column, tlsMigrationFix))
	}
	return strings.Join(out, "\n")
}
func isTLSMigrationError(err error) bool {
	var migration *tlsMigrationError
	return errors.As(err, &migration)
}

// ValidateRemovedTLSKeys checks YAML presence, not decoded values, before a DTO
// can discard obsolete fields. YAML keys remain case-sensitive.
func ValidateRemovedTLSKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(normalizeConfigSource(data), &doc); err != nil {
		return nil
	} // The caller reports syntax errors.
	findings := removedTLSFindings(&doc)
	if len(findings) > 0 {
		return &tlsMigrationError{findings: findings}
	}
	return nil
}

// ValidateRemovedTLSJSONKeys checks JSON presence using encoding/json's
// case-insensitive field matching. Token parsing preserves duplicate keys and
// accepts the same Unicode escapes as the settings DTO decoder.
func ValidateRemovedTLSJSONKeys(data []byte) error {
	if !json.Valid(data) {
		return nil // The caller reports JSON syntax errors.
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	doc, err := jsonMigrationNode(decoder, data)
	if err != nil {
		return fmt.Errorf("check removed TLS JSON keys: %w", err)
	}
	findings := removedTLSFindingsWithKey(doc, canonicalJSONTLSKey)
	if len(findings) > 0 {
		return &tlsMigrationError{findings: findings}
	}
	return nil
}

func jsonMigrationNode(decoder *json.Decoder, data []byte) (*yaml.Node, error) {
	offset := int(decoder.InputOffset())
	for offset < len(data) && strings.ContainsRune(" \t\r\n:,", rune(data[offset])) {
		offset++
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	line := bytes.Count(data[:offset], []byte{'\n'}) + 1
	column := offset - bytes.LastIndexByte(data[:offset], '\n')
	node := &yaml.Node{Kind: yaml.ScalarNode, Line: line, Column: column}
	if key, ok := token.(string); ok {
		node.Value = key
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			node.Kind = yaml.MappingNode
		case '[':
			node.Kind = yaml.SequenceNode
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
		for decoder.More() {
			child, err := jsonMigrationNode(decoder, data)
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
	}
	return node, nil
}

func removedTLSPath(path string) bool {
	if path == "tls" || path == "httpserver.tls" || path == "swarm.tls" || path == "swarm.node_tls" {
		return true
	}
	prefix, key, ok := strings.Cut(path, "].")
	if !ok {
		return false
	}
	if strings.HasPrefix(prefix, "providers[") {
		return key == "ca_file" || key == "client_cert_file" || key == "client_key_file" || key == "tls_auto"
	}
	if strings.HasPrefix(prefix, "swarm.join[") || strings.HasPrefix(prefix, "swarm.upstreams[") {
		return key == "dial.ca_file" || key == "dial.cert_file" || key == "dial.key_file" || key == "dial.auto" || key == "dial.insecure_skip_verify"
	}
	return false
}

func removedTLSFindings(doc *yaml.Node) []Finding {
	return removedTLSFindingsWithKey(doc, func(key string) string { return key })
}

func removedTLSFindingsWithKey(doc *yaml.Node, keyName func(string) string) []Finding {
	var findings []Finding
	active := map[*yaml.Node]bool{}
	var visit func(*yaml.Node, string)
	visit = func(n *yaml.Node, path string) {
		if n == nil || active[n] {
			return
		}
		active[n] = true
		defer delete(active, n)
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				visit(c, path)
			}
		case yaml.AliasNode:
			visit(n.Alias, path)
		case yaml.SequenceNode:
			for i, c := range n.Content {
				visit(c, fmt.Sprintf("%s[%d]", path, i))
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Tag == "!!merge" {
					if v.Kind == yaml.SequenceNode {
						for _, c := range v.Content {
							visit(c, path)
						}
					} else {
						visit(v, path)
					}
					continue
				}
				next := keyName(k.Value)
				if path != "" {
					next = path + "." + next
				}
				if removedTLSPath(next) {
					findings = append(findings, Finding{Severity: SeverityError, Path: next, Line: k.Line, Column: k.Column, Message: "TLS setting has been removed", Fix: tlsMigrationFix})
					continue
				}
				visit(v, next)
			}
		}
	}
	visit(doc, "")
	return findings
}

func canonicalJSONTLSKey(key string) string {
	for _, known := range []string{
		"tls", "httpserver", "swarm", "node_tls", "providers", "join", "upstreams", "dial",
		"ca_file", "client_cert_file", "client_key_file", "tls_auto",
		"cert_file", "key_file", "auto", "insecure_skip_verify",
	} {
		if strings.EqualFold(key, known) {
			return known
		}
	}
	return key
}
