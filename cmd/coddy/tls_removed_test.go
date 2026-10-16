package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRemovedTLSCommandIsNotAdvertised(t *testing.T) {
	var usage bytes.Buffer
	printUsage(&usage)
	for name, body := range map[string]string{
		"usage":       usage.String(),
		"bash":        readRepoFile(t, "../../packaging/completions/coddy.bash"),
		"zsh":         readRepoFile(t, "../../packaging/completions/coddy.zsh"),
		"postinstall": readRepoFile(t, "../../packaging/scripts/postinstall.sh"),
	} {
		for _, line := range strings.Split(body, "\n") {
			for _, word := range strings.Fields(line) {
				if strings.Trim(word, "'\"`:;()|") == "tls" {
					t.Errorf("%s still advertises the removed TLS command: %s", name, line)
				}
			}
		}
	}
}
