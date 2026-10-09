// Package linediff computes line-level differences between two versions of a
// text file and renders them as a standard unified diff.
//
// It exists because the Edits views of a chat - the count on the plate over the
// composer and the edits window - need one answer to "what changed" on every
// surface that renders one. Deriving
// that separately per surface is how the same edit ends up reported as
// "+8 -0" in one place and "+9 -1" in another.
package linediff

import (
	"fmt"
	"strings"
)

const (
	// DefaultContext is the number of unchanged lines kept around each change,
	// matching "diff -u" and what git emits.
	DefaultContext = 3

	// maxPatchBytes caps a rendered patch. Past it the diff stops being
	// something a human reads, and the payload starts to matter.
	maxPatchBytes = 256 * 1024

	// snakeBudget bounds the middle-snake search, whose cost is the region size
	// times the edit distance. Files of ordinary size stay well inside it and
	// get an exact answer; only a pair that is both very large and almost
	// entirely different runs out, and that pair is a rewrite however it is
	// described, so reporting it as one loses nothing a reader would notice.
	snakeBudget = 16_000_000
)

type opKind uint8

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string
}

// splitLines splits content into lines on "\n" only. A carriage return stays
// part of the line it terminates: this repository is checked out CRLF, and
// treating it as a separator would mark every line of every CRLF file as
// changed the moment one line is edited.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	// A trailing newline yields one empty trailing element that is not a line.
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffOps aligns two line slices into an edit script.
func diffOps(a, b []string) []op {
	// Common prefix and suffix carry no information and shrink the quadratic
	// part to the region that actually changed, which for a normal edit in a
	// large file is a handful of lines.
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	midA := a[prefix : len(a)-suffix]
	midB := b[prefix : len(b)-suffix]

	ops := make([]op, 0, len(a)+len(b))
	for _, line := range a[:prefix] {
		ops = append(ops, op{kind: opEqual, text: line})
	}
	ops = append(ops, myersOps(midA, midB)...)
	for _, line := range a[len(a)-suffix:] {
		ops = append(ops, op{kind: opEqual, text: line})
	}
	return ops
}

// Stat counts added and deleted lines between two file contents.
func Stat(before, after string) (added, deleted int) {
	if before == after {
		return 0, 0
	}
	for _, o := range diffOps(splitLines(before), splitLines(after)) {
		switch o.kind {
		case opInsert:
			added++
		case opDelete:
			deleted++
		case opEqual:
		}
	}
	return added, deleted
}

// Unified renders a unified diff for one file. It returns an empty patch when
// the two sides are identical, and reports truncated when the patch was cut
// short at maxPatchBytes; stats from Stat still describe the whole file.
func Unified(path, before, after string, context int) (patch string, truncated bool) {
	if before == after {
		return "", false
	}
	if context < 0 {
		context = 0
	}
	ops := diffOps(splitLines(before), splitLines(after))

	// Line numbers each op occupies on either side, precomputed so hunk
	// headers do not need a second pass.
	oldNo := make([]int, len(ops))
	newNo := make([]int, len(ops))
	oldCursor, newCursor := 0, 0
	for i, o := range ops {
		oldNo[i], newNo[i] = oldCursor, newCursor
		switch o.kind {
		case opEqual:
			oldCursor++
			newCursor++
		case opDelete:
			oldCursor++
		case opInsert:
			newCursor++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)

	for _, h := range hunkRanges(ops, context) {
		oldCount, newCount := 0, 0
		for _, o := range ops[h.start:h.end] {
			switch o.kind {
			case opEqual:
				oldCount++
				newCount++
			case opDelete:
				oldCount++
			case opInsert:
				newCount++
			}
		}
		oldStart, newStart := oldNo[h.start], newNo[h.start]
		if oldCount > 0 {
			oldStart++
		}
		if newCount > 0 {
			newStart++
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)

		for _, o := range ops[h.start:h.end] {
			// The marker is written even for an empty line: a bare "" would be
			// dropped by every unified-diff reader, the SPA parser included.
			switch o.kind {
			case opEqual:
				b.WriteString(" ")
			case opDelete:
				b.WriteString("-")
			case opInsert:
				b.WriteString("+")
			}
			b.WriteString(strings.TrimSuffix(o.text, "\n"))
			b.WriteString("\n")
			if !strings.HasSuffix(o.text, "\n") {
				b.WriteString("\\ No newline at end of file\n")
			}
			if b.Len() > maxPatchBytes {
				return b.String(), true
			}
		}
	}
	return b.String(), false
}

type hunkRange struct{ start, end int }

// hunkRanges groups changed ops into hunks, padding each with context lines and
// merging neighbours whose gap is small enough that separate hunks would repeat
// the same lines.
func hunkRanges(ops []op, context int) []hunkRange {
	var changed []int
	for i, o := range ops {
		if o.kind != opEqual {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}

	var out []hunkRange
	groupStart, prev := changed[0], changed[0]
	for _, idx := range changed[1:] {
		if idx-prev > 2*context+1 {
			out = append(out, padHunk(groupStart, prev, len(ops), context))
			groupStart = idx
		}
		prev = idx
	}
	out = append(out, padHunk(groupStart, prev, len(ops), context))
	return out
}

func padHunk(first, last, total, context int) hunkRange {
	start := first - context
	if start < 0 {
		start = 0
	}
	end := last + 1 + context
	if end > total {
		end = total
	}
	return hunkRange{start: start, end: end}
}

// myersOps produces a minimal edit script with Myers' O(ND) algorithm, in
// linear space (the divide-and-conquer refinement from section 4b of the paper).
//
// It replaces a full longest-common-subsequence table. That table needed one
// cell per pair of lines, so it had to be abandoned above a size cap, and past
// the cap the whole changed region was reported as deleted-then-inserted: a
// handful of scattered edits in a 3000-line file read as a 3000-line rewrite.
// Myers needs one slot per diagonal instead, so there is no cap to hit and the
// answer stays minimal at any file size.
func myersOps(a, b []string) []op {
	out := make([]op, 0, len(a)+len(b))

	var rec func(a, b []string)
	rec = func(a, b []string) {
		// Every level exposes a fresh common prefix and suffix. Peeling them is
		// far cheaper than letting the snake search walk them, and it also
		// guarantees the two ends differ below, which is what keeps the
		// recursion strictly shrinking.
		p := 0
		for p < len(a) && p < len(b) && a[p] == b[p] {
			out = append(out, op{kind: opEqual, text: a[p]})
			p++
		}
		a, b = a[p:], b[p:]

		s := 0
		for s < len(a) && s < len(b) && a[len(a)-1-s] == b[len(b)-1-s] {
			s++
		}
		suffix := a[len(a)-s:]
		a, b = a[:len(a)-s], b[:len(b)-s]

		switch {
		case len(a) == 0:
			for _, line := range b {
				out = append(out, op{kind: opInsert, text: line})
			}
		case len(b) == 0:
			for _, line := range a {
				out = append(out, op{kind: opDelete, text: line})
			}
		default:
			// Both ends differ, so the edit distance here is at least two and
			// the middle snake splits this into two strictly smaller problems.
			x, y, u, v, ok := middleSnake(a, b)
			if !ok {
				for _, line := range a {
					out = append(out, op{kind: opDelete, text: line})
				}
				for _, line := range b {
					out = append(out, op{kind: opInsert, text: line})
				}
				break
			}
			rec(a[:x], b[:y])
			for i := x; i < u; i++ {
				out = append(out, op{kind: opEqual, text: a[i]})
			}
			rec(a[u:], b[v:])
		}

		for _, line := range suffix {
			out = append(out, op{kind: opEqual, text: line})
		}
	}

	rec(a, b)
	return out
}

// middleSnake finds a run of equal lines lying on some minimal edit path, and
// reports it as [x,u) in a against [y,v) in b.
//
// The forward and backward searches advance one edit distance at a time until
// they meet; where they meet is by construction the middle of a shortest path,
// so recursing on the two sides yields a minimal script overall.
func middleSnake(a, b []string) (x, y, u, v int, ok bool) {
	n, m := len(a), len(b)
	span := n + m
	// One slot per diagonal in [-span, span], plus the k+-1 lookups at the ends.
	off := span + 1
	forward := make([]int, 2*span+3)
	backward := make([]int, 2*span+3)
	delta := n - m
	oddDelta := delta%2 != 0

	forward[off+1] = 0
	backward[off+1] = 0

	maxD := (span + 1) / 2
	if budget := snakeBudget / (span + 1); budget < maxD {
		maxD = budget
	}

	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			var px int
			if k == -d || (k != d && forward[off+k-1] < forward[off+k+1]) {
				px = forward[off+k+1]
			} else {
				px = forward[off+k-1] + 1
			}
			py := px - k
			startX, startY := px, py
			for px < n && py < m && a[px] == b[py] {
				px++
				py++
			}
			forward[off+k] = px
			// The backward search has only reached d-1, so only diagonals it
			// has actually visited can be checked for an overlap.
			if reverseK := delta - k; oddDelta && reverseK >= -(d-1) && reverseK <= d-1 {
				if forward[off+k]+backward[off+reverseK] >= n {
					return startX, startY, px, py, true
				}
			}
		}

		for k := -d; k <= d; k += 2 {
			var px int
			if k == -d || (k != d && backward[off+k-1] < backward[off+k+1]) {
				px = backward[off+k+1]
			} else {
				px = backward[off+k-1] + 1
			}
			py := px - k
			startX, startY := px, py
			for px < n && py < m && a[n-1-px] == b[m-1-py] {
				px++
				py++
			}
			backward[off+k] = px
			if forwardK := delta - k; !oddDelta && forwardK >= -d && forwardK <= d {
				if backward[off+k]+forward[off+forwardK] >= n {
					// Measured from the end, so flip back into a's coordinates.
					return n - px, m - py, n - startX, m - startY, true
				}
			}
		}
	}

	// Out of budget: the caller reports the region as replaced wholesale.
	return 0, 0, 0, 0, false
}
