package linediff

import (
	"fmt"
	// Seeded generators make reproducible test inputs; nothing here is secret.
	"math/rand" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"strings"
	"testing"
	"time"
)

// applyOps replays an edit script, returning what it makes of each side. A
// script that does not rebuild both files is wrong however few edits it uses.
func applyOps(ops []op) (fromA, toB []string) {
	for _, o := range ops {
		switch o.kind {
		case opEqual:
			fromA = append(fromA, o.text)
			toB = append(toB, o.text)
		case opDelete:
			fromA = append(fromA, o.text)
		case opInsert:
			toB = append(toB, o.text)
		}
	}
	return fromA, toB
}

func countEdits(ops []op) int {
	n := 0
	for _, o := range ops {
		if o.kind != opEqual {
			n++
		}
	}
	return n
}

// referenceEditDistance is the textbook LCS recurrence, quadratic and obviously
// correct, used only to say what "minimal" means for a given pair.
func referenceEditDistance(a, b []string) int {
	cols := len(b) + 1
	table := make([]int, (len(a)+1)*cols)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i*cols+j] = table[(i+1)*cols+j+1] + 1
			} else if table[(i+1)*cols+j] >= table[i*cols+j+1] {
				table[i*cols+j] = table[(i+1)*cols+j]
			} else {
				table[i*cols+j] = table[i*cols+j+1]
			}
		}
	}
	lcs := table[0]
	return (len(a) - lcs) + (len(b) - lcs)
}

func randomLines(rng *rand.Rand, n int) []string {
	// A tiny alphabet so the two sides genuinely share lines; random unique
	// strings would make every pair a full rewrite and test nothing.
	const alphabet = "abcde"
	out := make([]string, n)
	for i := range out {
		out[i] = string(alphabet[rng.Intn(len(alphabet))])
	}
	return out
}

func TestDiffOpsRebuildBothSides(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 400; i++ {
		a := randomLines(rng, rng.Intn(24))
		b := randomLines(rng, rng.Intn(24))
		gotA, gotB := applyOps(diffOps(a, b))
		if strings.Join(gotA, "\n") != strings.Join(a, "\n") {
			t.Fatalf("case %d: script does not rebuild a\n a=%v\n got=%v", i, a, gotA)
		}
		if strings.Join(gotB, "\n") != strings.Join(b, "\n") {
			t.Fatalf("case %d: script does not rebuild b\n b=%v\n got=%v", i, b, gotB)
		}
	}
}

func TestDiffOpsAreMinimal(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 400; i++ {
		a := randomLines(rng, rng.Intn(20))
		b := randomLines(rng, rng.Intn(20))
		got := countEdits(diffOps(a, b))
		want := referenceEditDistance(a, b)
		if got != want {
			t.Fatalf("case %d: %d edits, minimum is %d\n a=%v\n b=%v", i, got, want, a, b)
		}
	}
}

func TestDiffOpsHandleEmptySides(t *testing.T) {
	if ops := diffOps(nil, nil); len(ops) != 0 {
		t.Fatalf("two empty files have no edits, got %v", ops)
	}
	if got := countEdits(diffOps(nil, []string{"a", "b"})); got != 2 {
		t.Fatalf("want 2 insertions, got %d", got)
	}
	if got := countEdits(diffOps([]string{"a", "b"}, nil)); got != 2 {
		t.Fatalf("want 2 deletions, got %d", got)
	}
}

// The bug this replaced the LCS table for: a few edits scattered through a file
// too large for a quadratic table used to be reported as a whole-file rewrite.
func TestScatteredEditsInALargeFileStayScattered(t *testing.T) {
	const lines = 4000
	before := make([]string, lines)
	for i := range before {
		before[i] = fmt.Sprintf("line %d", i)
	}
	after := append([]string(nil), before...)
	after[10] = "line 10 edited"
	after[2000] = "line 2000 edited"
	after[3990] = "line 3990 edited"

	added, deleted := Stat(strings.Join(before, "\n")+"\n", strings.Join(after, "\n")+"\n")
	if added != 3 || deleted != 3 {
		t.Fatalf("want 3 added and 3 deleted, got %d/%d", added, deleted)
	}
}

// A large insertion far from either end is the other shape that used to defeat
// the table: the changed region is huge even though the edit is simple.
func TestLargeInsertionInTheMiddle(t *testing.T) {
	const lines = 3000
	before := make([]string, lines)
	for i := range before {
		before[i] = fmt.Sprintf("line %d", i)
	}
	inserted := make([]string, 500)
	for i := range inserted {
		inserted[i] = fmt.Sprintf("new %d", i)
	}
	after := make([]string, 0, lines+len(inserted))
	after = append(after, before[:1500]...)
	after = append(after, inserted...)
	after = append(after, before[1500:]...)

	added, deleted := Stat(strings.Join(before, "\n")+"\n", strings.Join(after, "\n")+"\n")
	if added != 500 || deleted != 0 {
		t.Fatalf("want 500 added and 0 deleted, got %d/%d", added, deleted)
	}
}

// Two unrelated files of real size: the point is that this finishes rather than
// exhausting memory, which is what the old table did before its cap kicked in.
func TestUnrelatedLargeFilesComplete(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	a := randomLines(rng, 4000)
	b := randomLines(rng, 4000)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = diffOps(a, b)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("diff of two 4000-line files did not finish in 30s")
	}
}

// The search is bounded, so a pair that is both enormous and almost entirely
// different is reported as a wholesale replacement rather than being chased to
// a minimal answer nobody would read. It must still be a correct script, and it
// must be quick.
func TestPathologicalPairFallsBackQuickly(t *testing.T) {
	a := make([]string, 40000)
	b := make([]string, 40000)
	for i := range a {
		a[i] = fmt.Sprintf("old %d", i)
		b[i] = fmt.Sprintf("new %d", i)
	}

	start := time.Now()
	ops := diffOps(a, b)
	elapsed := time.Since(start)

	gotA, gotB := applyOps(ops)
	if len(gotA) != len(a) || len(gotB) != len(b) {
		t.Fatalf("script does not rebuild both sides: %d/%d", len(gotA), len(gotB))
	}
	if gotA[0] != a[0] || gotB[0] != b[0] {
		t.Fatalf("script rebuilt the wrong content")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("bounded search still took %s", elapsed)
	}
}

// The budget must not cost ordinary files their exact diff: a file of realistic
// size with a realistic number of edits stays minimal.
func TestRealisticFileStaysExact(t *testing.T) {
	const lines = 6000
	before := make([]string, lines)
	for i := range before {
		before[i] = fmt.Sprintf("line %d", i)
	}
	after := append([]string(nil), before...)
	for i := 0; i < 300; i++ {
		after[i*17] = fmt.Sprintf("line %d edited", i*17)
	}

	added, deleted := Stat(strings.Join(before, "\n")+"\n", strings.Join(after, "\n")+"\n")
	if added != 300 || deleted != 300 {
		t.Fatalf("want 300 added and 300 deleted, got %d/%d", added, deleted)
	}
}
