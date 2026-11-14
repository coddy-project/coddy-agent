//go:build memory

package memstorage

// The performance group of the note search: benchmarks only, run by
// `make test-perf` (with the memory tag) and never by `make test`. A search
// runs at the recall of every user turn and before every save, and it reads
// and ranks every note of both roots each time, so its cost is what a store
// of a few hundred notes adds to the memory child's tool call.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchWords is the vocabulary of the generated notes: enough distinct words
// that a query term is rare in some notes and common in others.
var benchWords = strings.Fields(`api build cache config container database deploy docker endpoint error
	feature gateway handler image index job key log merge metric network node
	token upgrade version worker queue restart release rollback schema session
	service socket stack table task test timeout tunnel unit update user vault
	watcher window yaml zone alert backup branch client cluster commit context
	debug domain driver event filter folder format group header hook issue
	label limit lock mirror module offset option parser patch plugin policy
	probe proxy quota reader record region route runner scope script secret
	server signal source stream target thread tracker trigger volume webhook`)

// benchStore writes n generated notes, four fifths under the global root and
// the rest under the project root (which is reached through os.Root and
// costs more per file), plus the three notes a deployment question is about.
func benchStore(b *testing.B, n int) *Store {
	b.Helper()
	tmp := b.TempDir()
	g := filepath.Join(tmp, "memory")
	p := filepath.Join(tmp, "work", ".coddy", "memory")
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(p)), 0o755); err != nil {
		b.Fatal(err)
	}
	st := NewWithRoots(g, p)
	put := func(scope, rel, body string) {
		if _, err := st.WriteFlexible(scope, "n", rel, body); err != nil {
			b.Fatal(err)
		}
	}
	put("global", "ops/relay-deployment.md", "Relay deployment: build the image and restart the relay service.")
	put("global", "notes/how-to-write-a-note.md", "How to write a note. Open the file, add a heading and save it to the folder.")
	put("project", "relay-deploy-steps.md", "relay deploy steps")
	seed := uint32(1)
	next := func(mod int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % mod
	}
	for i := 0; i < n; i++ {
		var body strings.Builder
		for w, words := 0, 60+next(90); w < words; w++ {
			body.WriteString(benchWords[next(len(benchWords))])
			if w%12 == 11 {
				body.WriteString(".\n")
			} else {
				body.WriteByte(' ')
			}
		}
		scope := "global"
		if i%5 == 4 {
			scope = "project"
		}
		put(scope, fmt.Sprintf("topic-%02d/%s-%s-%03d.md", i%12, benchWords[next(len(benchWords))], benchWords[next(len(benchWords))], i), body.String())
	}
	if _, err := os.Stat(p); err != nil {
		b.Fatal(err)
	}
	return st
}

func BenchmarkStoreSearch(b *testing.B) {
	st := benchStore(b, 200)
	for _, tc := range []struct{ name, query string }{
		{"question", "how to deploy the relay"},
		{"plural", "configs"},
		{"three-words", "restart worker queue"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				hits, err := st.Search(tc.query, "both", 8)
				if err != nil {
					b.Fatal(err)
				}
				_ = hits
			}
		})
	}
}
