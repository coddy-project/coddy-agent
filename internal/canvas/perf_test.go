package canvas

import (
	"os"
	"path/filepath"
	"testing"
)

// benchBoard is a workspace with one page on one board, and the home the
// receipts live under.
func benchBoard(tb testing.TB) (Ref, string) {
	ws, home, bundle := tb.TempDir(), tb.TempDir(), tb.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "page.html"), []byte("<html></html>"), 0o600); err != nil {
		tb.Fatal(err)
	}
	doc := `{"nodes":[{"id":"n1","type":"file","file":"page.html","x":0,"y":0,"width":800,"height":600}],"edges":[]}`
	if err := os.WriteFile(filepath.Join(ws, "board.canvas"), []byte(doc), 0o600); err != nil {
		tb.Fatal(err)
	}
	return Ref{Bundle: bundle, Workspace: ws, File: "board.canvas"}, home
}

// FrameOf decides whether a page runs on every file request of a frame, and a
// page loads as many of those as it has assets. Under allow the decision costs
// nothing; under ask it reads the receipts, which is what the parallel pair
// below holds to a shared read rather than a shared lock.
func BenchmarkFrameOfAllow(b *testing.B) {
	ref, home := benchBoard(b)
	ref.Trust = NewTrust(home, "allow")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FrameOf(ref, "n1"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFrameOfAsk(b *testing.B) {
	ref, home := benchBoard(b)
	ref.Trust = NewTrust(home, "ask")
	if err := ref.Trust.Store.Approve(ref.Workspace, "board.canvas", []string{"page.html"}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FrameOf(ref, "n1"); err != nil {
			b.Fatal(err)
		}
	}
}

// The assets of a page arrive together, and every session's frames share one
// receipts file: the read must not serialise them.
func BenchmarkFrameOfAskParallel(b *testing.B) {
	ref, home := benchBoard(b)
	ref.Trust = NewTrust(home, "ask")
	if err := ref.Trust.Store.Approve(ref.Workspace, "board.canvas", []string{"page.html"}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := FrameOf(ref, "n1"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
