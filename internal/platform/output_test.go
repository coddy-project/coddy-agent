package platform

import "testing"

// DecodeOutput must be a no-op for anything already valid UTF-8, on every
// platform: that is the path all wrapped PowerShell output and all POSIX output
// takes.
func TestDecodeOutputPassesThroughUTF8(t *testing.T) {
	for _, want := range []string{
		"",
		"plain ascii output\n",
		"Русский текст — em dash ✅ 中文\r\n",
		"Expand `agent.qwen.md` to **7 blocks** (21 items)",
	} {
		if got := DecodeOutput([]byte(want)); got != want {
			t.Fatalf("DecodeOutput(%q) = %q, want it unchanged", want, got)
		}
	}
}
