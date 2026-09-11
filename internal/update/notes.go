package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// After an update the user is looking at a version number and nothing else,
// and the question "what changed?" sends them to the releases page (issue
// #195). The report below answers it in place: every release between the
// version that was running and the one just installed, oldest first, with its
// notes, capped so a long gap stays readable, and the GitHub comparison for
// the whole range at the end. It is printed only once an update happened, and
// it never fails one: a list that cannot be fetched leaves the comparison link
// alone, and a build with no release to count from gets the notes of the
// release it installed.

// NotesEnvVar turns the report off for scripted runs when set to 0, false, no
// or off - the same effect as --no-notes.
const NotesEnvVar = "CODDY_UPDATE_NOTES"

const (
	// notesMaxLines caps the lines printed from the release notes, so a
	// dozen releases at once do not scroll the install lines off the screen.
	notesMaxLines = 20
	// notesTimeout bounds the extra request: the update is done by then, and
	// a slow API must not hold the prompt hostage.
	notesTimeout = 15 * time.Second
	// notesMaxPages bounds the walk down the release list, notesPageSize
	// releases a page, which is as far as GitHub pages it.
	notesMaxPages = 5
	notesPageSize = 100
)

// NotesDisabledByEnv reports whether the environment asks for a quiet update.
func NotesDisabledByEnv() bool {
	return notesDisabled(os.Getenv(NotesEnvVar))
}

func notesDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// reportChanges prints what changed between the version that was running and
// the release just installed. Nothing in here can fail the update that is
// already in: an error fetching the list degrades the report to the link.
func reportChanges(ctx context.Context, client *http.Client, opts Options, installed *ghRelease, out io.Writer) {
	if opts.NoNotes {
		return
	}
	to := strings.TrimPrefix(strings.TrimSpace(installed.TagName), "v")
	from := NormalizeSemver(opts.CurrentVersion)
	if from == "" || CompareSemver(from, to) >= 0 {
		// No range to walk: a dev build has no release to count from, and a
		// downgrade walks backwards. The installed release speaks for itself.
		_, _ = fmt.Fprintf(out, "\nRelease notes for %s:\n", to)
		printNotes(out, notesLines(installed.Body))
		_, _ = fmt.Fprintln(out, releaseURL(opts.Repo, installed))
		return
	}

	compare := fmt.Sprintf("https://github.com/%s/compare/%s...%s", opts.Repo, from, to)
	ctx, cancel := context.WithTimeout(ctx, notesTimeout)
	defer cancel()
	releases, err := fetchReleasesBetween(ctx, client, opts.APIBase, opts.Repo, from, to)
	if err != nil || len(releases) == 0 {
		_, _ = fmt.Fprintf(out, "\nFull changelog: %s\n", compare)
		return
	}

	var lines []string
	for i := range releases {
		lines = append(lines, releaseHeading(&releases[i]))
		for _, l := range notesLines(releases[i].Body) {
			lines = append(lines, "  "+l)
		}
	}
	_, _ = fmt.Fprintf(out, "\nChanges since %s:\n", from)
	printNotes(out, lines)
	_, _ = fmt.Fprintf(out, "Full changelog: %s\n", compare)
}

// printNotes writes the lines up to the cap and says how many did not fit.
func printNotes(out io.Writer, lines []string) {
	kept, more := capLines(lines, notesMaxLines)
	for _, l := range kept {
		_, _ = fmt.Fprintln(out, l)
	}
	if more > 0 {
		_, _ = fmt.Fprintf(out, "... %d more lines\n", more)
	}
}

// capLines keeps the first max lines and counts the rest.
func capLines(lines []string, max int) ([]string, int) {
	if len(lines) <= max {
		return lines, 0
	}
	return lines[:max], len(lines) - max
}

// releaseHeading names a release the way the report lists it: the tag, the
// title when the release has one of its own, and the day it was published.
func releaseHeading(rel *ghRelease) string {
	tag := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	heading := tag
	if name := strings.TrimSpace(rel.Name); name != "" && name != tag && name != rel.TagName {
		heading += " - " + name
	}
	if day := publishedDay(rel.PublishedAt); day != "" {
		heading += " (" + day + ")"
	}
	return heading
}

// publishedDay trims a GitHub timestamp down to its date.
func publishedDay(published string) string {
	published = strings.TrimSpace(published)
	if len(published) < len("2006-01-02") {
		return ""
	}
	if _, err := time.Parse("2006-01-02", published[:10]); err != nil {
		return ""
	}
	return published[:10]
}

// releaseURL is the page of one release, from the API when it gave one.
func releaseURL(repo string, rel *ghRelease) string {
	if url := strings.TrimSpace(rel.HTMLURL); url != "" {
		return url
	}
	return fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, strings.TrimSpace(rel.TagName))
}

// fetchReleasesBetween lists the releases whose version is after from and up
// to to, oldest first. GitHub pages the list newest first, so the walk stops
// at the page that reached the running release. Drafts and prereleases are
// not something an update installs, so they are not something it reports.
func fetchReleasesBetween(ctx context.Context, client *http.Client, apiBase, repo, from, to string) ([]ghRelease, error) {
	apiBase = strings.TrimRight(apiBase, "/")
	var picked []ghRelease
	for page := 1; page <= notesMaxPages; page++ {
		url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", apiBase, repo, notesPageSize, page)
		list, err := fetchReleasePage(ctx, client, url)
		if err != nil {
			return nil, err
		}
		reachedFrom := false
		for _, rel := range list {
			if rel.Draft || rel.Prerelease {
				continue
			}
			tag := NormalizeSemver(rel.TagName)
			if tag == "" {
				continue
			}
			if CompareSemver(tag, from) <= 0 {
				reachedFrom = true
				continue
			}
			if CompareSemver(tag, to) > 0 {
				continue
			}
			picked = append(picked, rel)
		}
		if reachedFrom || len(list) < notesPageSize {
			break
		}
	}
	sort.SliceStable(picked, func(i, j int) bool {
		return CompareSemver(picked[i].TagName, picked[j].TagName) < 0
	})
	return picked, nil
}

func fetchReleasePage(ctx context.Context, client *http.Client, url string) ([]ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "coddy-agent-update")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github api %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

var (
	// notesHeading is a markdown heading: the hashes, a space, the title.
	notesHeading = regexp.MustCompile(`^#{1,6}\s+(\S.*)$`)
	// notesPullTrailer is what GitHub appends to every generated line:
	// "by @author in https://github.com/owner/repo/pull/N".
	notesPullTrailer = regexp.MustCompile(`\s+by @[\w-]+ in https://github\.com/[\w.-]+/[\w.-]+/pull/(\d+)\s*$`)
)

// notesLines renders a release body for the terminal, one line per change.
// GitHub's generated notes wrap every change in boilerplate - a "What's
// Changed" heading, the author and pull request link on each line, a "Full
// Changelog" footer - and all of it is dropped: the report has its own
// comparison link, and the pull request number is kept as "(#N)". Blank
// lines go, headings become a plain title with a colon, bullets read as "-".
func notesLines(body string) []string {
	var lines []string
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := notesHeading.FindStringSubmatch(line); m != nil {
			title := strings.TrimSpace(m[1])
			if strings.EqualFold(title, "what's changed") || strings.EqualFold(title, "what’s changed") {
				continue
			}
			lines = append(lines, title+":")
			continue
		}
		if strings.HasPrefix(line, "**Full Changelog**") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "* "); ok {
			line = "- " + rest
		}
		line = notesPullTrailer.ReplaceAllString(line, " (#$1)")
		lines = append(lines, line)
	}
	return lines
}
