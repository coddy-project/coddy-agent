package docsgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Release is what GitHub returns for one release, reduced to what the
// changelog prints.
type Release struct {
	Tag         string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	URL         string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

// FetchReleases reads every release of repo through the gh CLI.
func FetchReleases(repo string) ([]Release, error) {
	cmd := exec.Command("gh", "api", "--paginate", "repos/"+repo+"/releases", "--jq", ".[]")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api releases: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var releases []Release
	for {
		var r Release
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if !r.Draft {
			releases = append(releases, r)
		}
	}
	return releases, nil
}

var headingRE = regexp.MustCompile(`(?m)^(#{1,5}) `)

// Changelog renders docs/getting-started/changelog.md from the releases,
// newest first. Headings inside a release body are demoted so the page keeps
// one H1 and one H2 per version.
func Changelog(repo string, releases []Release) string {
	var b strings.Builder
	b.WriteString("# Changelog\n\n")
	fmt.Fprintf(&b, "Release notes of every published version, generated from the [GitHub Releases](https://github.com/%s/releases) of the repository by `make docs-changelog`. `coddy update` prints the notes of the versions it skips over, so the answer to \"what changed\" is also on the screen after an upgrade.\n", repo)
	for _, r := range releases {
		date := r.PublishedAt
		if t, err := time.Parse(time.RFC3339, r.PublishedAt); err == nil {
			date = t.Format("2006-01-02")
		}
		title := r.Tag
		if r.Name != "" && r.Name != r.Tag {
			title = r.Tag + " " + r.Name
		}
		if r.Prerelease {
			title += " (pre-release)"
		}
		fmt.Fprintf(&b, "\n## %s - %s\n\n", title, date)
		body := strings.TrimSpace(strings.ReplaceAll(r.Body, "\r\n", "\n"))
		if body == "" {
			fmt.Fprintf(&b, "No notes. [Release page](%s).\n", r.URL)
			continue
		}
		body = headingRE.ReplaceAllString(body, "#$1 ")
		fmt.Fprintf(&b, "%s\n\n[Release page](%s).\n", body, r.URL)
	}
	return b.String()
}
