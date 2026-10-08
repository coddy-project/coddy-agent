package main

// The --lang flag of `coddy docs`: every verb takes it anywhere among its
// arguments, in both spellings, and an empty one is a usage error.

import (
	"io"
	"strings"
	"testing"
)

// docsEnglishLocale pins the terminal's language to English, whatever the
// machine running the test speaks, so only the flag picks Russian.
func docsEnglishLocale(t *testing.T) {
	t.Helper()
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("CODDY_LANG", "")
}

func TestDocsLangFlagStandsAnywhereAmongTheArguments(t *testing.T) {
	docsEnglishLocale(t)
	for _, args := range [][]string{
		{"search", "--lang", "ru", "упоминание файла"},
		{"search", "упоминание", "--lang=ru", "файла"},
		{"--lang", "ru", "search", "упоминание файла"},
	} {
		var out strings.Builder
		if err := runDocs(args, &out); err != nil {
			t.Fatalf("coddy docs %v: %v", args, err)
		}
		if !strings.Contains(out.String(), "features/mentions") {
			t.Fatalf("coddy docs %v did not find the Russian page: %s", args, out.String())
		}
	}

	var out strings.Builder
	if err := runDocs([]string{"show", "features/mentions#what-the-model-receives", "--lang", "ru"}, &out); err != nil {
		t.Fatalf("coddy docs show: %v", err)
	}
	if !strings.HasPrefix(out.String(), "## Что получает модель") {
		t.Fatalf("coddy docs show ... --lang ru: %.200s", out.String())
	}
}

func TestDocsWithoutAVerbTakesTheLangFlagToo(t *testing.T) {
	docsEnglishLocale(t)
	var out strings.Builder
	if err := runDocs([]string{"--lang", "ru"}, &out); err != nil {
		t.Fatalf("coddy docs --lang ru: %v", err)
	}
	if !strings.Contains(out.String(), "Возможности") {
		t.Fatalf("the contents did not come out in Russian: %.200s", out.String())
	}
}

func TestDocsLangFlagWithoutAKnownLanguageIsAUsageError(t *testing.T) {
	docsEnglishLocale(t)
	for _, args := range [][]string{
		{"--lang"},
		{"show", "features/mentions", "--lang"},
		{"search", "homebrew", "--lang="},
		{"show", "--lang", "fr", "features/mentions"},
		{"search", "--lang", "--limit", "2", "homebrew"},
	} {
		if err := runDocs(args, io.Discard); err == nil || err.Error() != docsUsage() {
			t.Errorf("coddy docs %v: %v", args, err)
		}
	}
}
