package textenc

import "testing"

// The system ANSI code page is a no-op outside Windows, so the end-to-end path
// through DecodeToUTF8 cannot exercise this choice on Linux CI. These tests feed
// the decision the two readings directly, which keeps the heuristic covered
// everywhere: a regression in it would otherwise only surface on Windows.
func TestPreferSystemANSI(t *testing.T) {
	tests := []struct {
		name     string
		detected string
		ansi     string
		want     bool
	}{
		{
			name:     "one short cyrillic comment in ascii source",
			detected: "package main\n\nfunc main() {\n\t// Ãîòîâî\n}\n",
			ansi:     "package main\n\nfunc main() {\n\t// Готово\n}\n",
			want:     true,
		},
		{
			name:     "cyrillic value in a json field",
			detected: "{\n  \"id\": 1,\n  \"title\": \"Îò÷¸ò\"\n}\n",
			ansi:     "{\n  \"id\": 1,\n  \"title\": \"Отчёт\"\n}\n",
			want:     true,
		},
		{
			name:     "detection already found a non-latin script",
			detected: "# Итоги\nready\n",
			ansi:     "# Ниогй\nready\n",
			want:     false,
		},
		{
			name:     "ansi reading is latin too",
			detected: "Café au lait\n",
			ansi:     "Café au lait\n",
			want:     false,
		},
		{
			name:     "french accents are isolated marks inside ascii words",
			detected: "naïve résumé\nfrom the café\n",
			ansi:     "naпve rйsumй\nfrom the cafй\n",
			want:     false,
		},
		{
			name:     "german umlaut inside an ascii word",
			detected: "Müller GmbH\naddress: Berlin\n",
			ansi:     "Mьller GmbH\naddress: Berlin\n",
			want:     false,
		},
		{
			name:     "accented word standing alone stays latin",
			detected: "il va à Paris\ndemain matin\n",
			ansi:     "il va а Paris\ndemain matin\n",
			want:     false,
		},
		{
			name:     "run too short to read as mis-decoded text",
			detected: "answer: Äà\nnext: no\n",
			ansi:     "answer: Да\nnext: no\n",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preferSystemANSI(tt.detected, tt.ansi); got != tt.want {
				t.Fatalf("preferSystemANSI(%q, %q) = %v, want %v", tt.detected, tt.ansi, got, tt.want)
			}
		})
	}
}

func TestMaxNonASCIIRun(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "pure ascii", text: "hello world\n", want: 0},
		{name: "isolated accents", text: "naïve résumé", want: 1},
		{name: "mis-decoded cyrillic word", text: "// Ãîòîâî", want: 6},
		{name: "run broken by a space", text: "Ãî òîâî", want: 4},
		{name: "symbols count toward the run", text: "Îò÷¸ò", want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxNonASCIIRun(tt.text); got != tt.want {
				t.Fatalf("maxNonASCIIRun(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

func TestHasMixedScriptWord(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "pure ascii", text: "hello world", want: false},
		{name: "pure cyrillic word among ascii", text: "answer: Готово", want: false},
		{name: "latin accents", text: "naïve résumé", want: false},
		{name: "ascii word carrying a cyrillic letter", text: "rйsumй", want: true},
		{name: "mixed word at the very end", text: "value: Mьller", want: true},
		{name: "digits do not join letters into one word", text: "Отчёт2024", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasMixedScriptWord(tt.text); got != tt.want {
				t.Fatalf("hasMixedScriptWord(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
