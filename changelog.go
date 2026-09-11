package changelog

import (
	"embed"
	"regexp"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
)

// versionFiles holds the batched per-version notes plus the header template.
//
//go:embed .changes/*.md
var versionFiles embed.FS

// headerFile is the changelog preamble shown above the versions.
//
//go:embed .changes/header.tpl.md
var headerFile string

// headerName is the embedded file that is the preamble, not a version.
const headerName = ".changes/header.tpl.md"

// headingRe pulls the URL and date out of a version heading, e.g.
// "## [0.7.7](https://…/tag/v0.7.7) — 2026-07-05".
var headingRe = regexp.MustCompile(`^##\s+\[[^\]]*\]\(([^)]+)\)\s*[—-]\s*(.+?)\s*$`)

// Release is one released version's notes, parsed into structured sections.
type Release struct {
	// Version is the semantic version without the leading v, e.g. "0.7.7".
	Version string `json:"version"`
	// Tag is the git tag form, e.g. "v0.7.7".
	Tag string `json:"tag"`
	// URL links to the release, taken from the version heading.
	URL string `json:"url,omitempty"`
	// Date is the release date as written in the heading, e.g. "2026-07-05".
	Date string `json:"date,omitempty"`
	// Sections are the change groups (Added, Fixed, …) in file order.
	Sections []Section `json:"sections"`
	// Markdown is the version's notes exactly as batched. It drives the human
	// renderer and is deliberately excluded from JSON.
	Markdown string `json:"-"`
}

// Section is one change group within a release, e.g. "Fixed".
type Section struct {
	Kind    string   `json:"kind"`
	Changes []string `json:"changes"`
}

// Header returns the changelog preamble.
func Header() string {
	return strings.TrimSpace(headerFile)
}

// releases parses the embedded version files once. The embedded content is
// fixed at build time, so the result is memoized and shared across calls.
var releases = sync.OnceValue(parseReleases)

// Releases returns every released version, newest first. The returned slice is
// shared across calls and must be treated as read-only.
func Releases() []Release {
	return releases()
}

func parseReleases() []Release {
	entries, err := versionFiles.ReadDir(".changes")
	if err != nil {
		return nil
	}

	out := make([]Release, 0, len(entries))
	for _, entry := range entries {
		name := ".changes/" + entry.Name()
		if entry.IsDir() || name == headerName || !strings.HasSuffix(name, ".md") {
			continue
		}
		tag := "v" + strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "v"), ".md")
		if !semver.IsValid(tag) {
			continue
		}
		body, err := versionFiles.ReadFile(name)
		if err != nil {
			continue
		}
		out = append(out, parseRelease(tag, strings.TrimSpace(string(body))))
	}

	sort.SliceStable(out, func(i, j int) bool {
		return semver.Compare(out[i].Tag, out[j].Tag) > 0
	})
	return out
}

// Find returns the notes for a single version. The query may carry a leading v
// or not ("0.7.7" and "v0.7.7" both match).
func Find(query string) (Release, bool) {
	want := "v" + strings.TrimPrefix(strings.TrimSpace(query), "v")
	for _, r := range Releases() {
		if r.Tag == want {
			return r, true
		}
	}
	return Release{}, false
}

// Full assembles the complete changelog — the header followed by every version,
// newest first — mirroring the merged CHANGELOG.md.
func Full() string {
	blocks := []string{Header()}
	for _, r := range Releases() {
		blocks = append(blocks, r.Markdown)
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

// parseRelease turns one version file into a structured Release. The version and
// tag come from the filename (authoritative); the URL, date, and sections are
// parsed from the batched Markdown.
func parseRelease(tag, markdown string) Release {
	url, date := parseHeading(markdown)
	return Release{
		Version:  strings.TrimPrefix(tag, "v"),
		Tag:      tag,
		URL:      url,
		Date:     date,
		Sections: parseSections(markdown),
		Markdown: markdown,
	}
}

// parseHeading extracts the release URL and date from the version heading.
func parseHeading(markdown string) (url, date string) {
	for line := range strings.SplitSeq(markdown, "\n") {
		if strings.HasPrefix(line, "## ") {
			if m := headingRe.FindStringSubmatch(line); len(m) == 3 {
				return m[1], strings.TrimSpace(m[2])
			}
			return "", ""
		}
	}
	return "", ""
}

// parseSections walks the batched Markdown, turning each "### Kind" heading into
// a Section and each "- " bullet into one of its changes. A wrapped bullet
// (a continuation line) is folded back onto the change it belongs to.
func parseSections(markdown string) []Section {
	var sections []Section
	for raw := range strings.SplitSeq(markdown, "\n") {
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(line, "### "):
			sections = append(sections, Section{Kind: strings.TrimSpace(line[4:])})
		case strings.HasPrefix(line, "- "):
			if len(sections) == 0 {
				continue
			}
			s := &sections[len(sections)-1]
			s.Changes = append(s.Changes, strings.TrimSpace(line[2:]))
		default:
			if strings.TrimSpace(line) == "" || len(sections) == 0 {
				continue
			}
			s := &sections[len(sections)-1]
			if len(s.Changes) == 0 {
				continue
			}
			s.Changes[len(s.Changes)-1] += " " + strings.TrimSpace(line)
		}
	}
	return sections
}
