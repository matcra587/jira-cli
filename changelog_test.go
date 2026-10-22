package changelog

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedReleaseCatalog(t *testing.T) {
	got := Releases()
	if len(got) == 0 {
		t.Fatal("embedded release catalog is empty")
	}
	entries, err := fs.Glob(versionFiles, ".changes/v*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(entries) {
		t.Fatalf("catalog has %d releases; embedded artifacts have %d", len(got), len(entries))
	}
	seen := make(map[string]bool, len(got))
	for _, release := range got {
		if seen[release.Tag] {
			t.Fatalf("duplicate release %q", release.Tag)
		}
		seen[release.Tag] = true
		body, err := versionFiles.ReadFile(".changes/" + release.Tag + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if release.Markdown != strings.TrimSpace(string(body)) {
			t.Fatalf("release %s changed embedded Markdown", release.Tag)
		}
		if release.Version != strings.TrimPrefix(release.Tag, "v") || release.URL == "" || release.Date == "" || len(release.Sections) == 0 {
			t.Fatalf("release %s lost metadata or sections", release.Tag)
		}
	}
	for _, path := range entries {
		tag := strings.TrimSuffix(strings.TrimPrefix(path, ".changes/"), ".md")
		if !seen[tag] {
			t.Fatalf("embedded artifact %s missing from catalog", path)
		}
	}
	if got[0].Tag != "v0.16.0-beta.0" || got[len(got)-1].Tag != "v0.1.0" {
		t.Fatal("catalog endpoints do not match released history")
	}
	positions := make(map[string]int, len(got))
	for i, release := range got {
		positions[release.Tag] = i
	}
	for _, pair := range [][2]string{{"v0.10.15", "v0.10.9"}, {"v0.10.0", "v0.9.2"}, {"v0.15.2", "v0.15.1"}, {"v0.16.0-beta.0", "v0.15.2"}} {
		if positions[pair[0]] >= positions[pair[1]] {
			t.Fatalf("%s must precede %s", pair[0], pair[1])
		}
	}
}

func TestReleaseArtifactSelectionAndSemanticOrder(t *testing.T) {
	files := fstest.MapFS{
		".changes/header.tpl.md":      {Data: []byte("# Header")},
		".changes/0.5.0.md":           {Data: []byte("older release")},
		".changes/v1.9.0.md":          {Data: []byte("numeric ordering")},
		".changes/v1.10.0.md":         {Data: []byte(" \nfinal release\n\t ")},
		".changes/v1.10.0-rc2.md":     {Data: []byte("lexical prerelease")},
		".changes/v1.10.0-rc10.md":    {Data: []byte("lexical prerelease")},
		".changes/v1.10.0-dev.md":     {Data: []byte("dev remains a prerelease")},
		".changes/v1.10.0-2.md":       {Data: []byte("numeric prerelease")},
		".changes/v1.10.0-1.md":       {Data: []byte("numeric prerelease")},
		".changes/v01.2.3.md":         {Data: []byte("invalid leading zero")},
		".changes/v1.2.3.4.md":        {Data: []byte("invalid fourth segment")},
		".changes/v1.2.3-01.md":       {Data: []byte("invalid numeric prerelease")},
		".changes/vnotrelease.md":     {Data: []byte("invalid version")},
		".changes/v3.0.0.txt":         {Data: []byte("wrong extension")},
		".changes/v9.0.0.md/file.txt": {Data: []byte("directory is not a release")},
	}
	got := readReleases(files)
	want := []string{"v1.10.0", "v1.10.0-rc2", "v1.10.0-rc10", "v1.10.0-dev", "v1.10.0-2", "v1.10.0-1", "v1.9.0", "v0.5.0"}
	if len(got) != len(want) {
		t.Fatalf("catalog selected %d artifacts; want %d", len(got), len(want))
	}
	for i, tag := range want {
		if got[i].Tag != tag {
			t.Fatalf("release %d: %s; want %s", i, got[i].Tag, tag)
		}
	}
	if got[0].Markdown != "final release" || got[0].Sections != nil || got[0].URL != "" || got[0].Date != "" {
		t.Fatal("outer whitespace or absent metadata changed")
	}
}

func TestReleaseEquivalentVersionOrder(t *testing.T) {
	got := readReleases(fstest.MapFS{
		".changes/v1.0.0+aaa.md": {Data: []byte("first")},
		".changes/v1.0.0+bbb.md": {Data: []byte("second")},
		".changes/v1.0.0.md":     {Data: []byte("third")},
	})
	if len(got) != 3 || got[0].Tag != "v1.0.0+aaa" || got[1].Tag != "v1.0.0+bbb" || got[2].Tag != "v1.0.0" {
		t.Fatal("equal semantic versions lost stable artifact order")
	}
}

func TestReleaseShorthandArtifactCompatibility(t *testing.T) {
	got := readReleases(fstest.MapFS{
		".changes/v1.md":   {Data: []byte("major shorthand")},
		".changes/v1.2.md": {Data: []byte("minor shorthand")},
	})
	if len(got) != 2 || got[0].Tag != "v1.2" || got[0].Version != "1.2" || got[1].Tag != "v1" || got[1].Version != "1" {
		t.Fatal("baseline semantic-version shorthand artifacts were narrowed")
	}
}

func TestReleaseConcurrentOwnership(t *testing.T) {
	for range 16 {
		t.Run("caller", func(t *testing.T) {
			t.Parallel()
			for range 20 {
				got := Releases()
				want := got[0].Sections[0].Changes[0]
				got[0].Sections[0].Changes[0] = "caller-owned change"
				found, ok := Find(got[0].Tag)
				if !ok || found.Sections[0].Changes[0] != want {
					t.Fatal("parallel caller changed cached release")
				}
				found.Sections[0].Changes[0] = "caller-owned selection"
			}
		})
	}
}

func TestReleaseVersionSelection(t *testing.T) {
	for _, query := range []string{"0.15.2", "v0.15.2", " \tv0.15.2\n"} {
		t.Run(query, func(t *testing.T) {
			got, ok := Find(query)
			if !ok || got.Tag != "v0.15.2" || got.Date != "2026-07-28" || got.URL != "https://github.com/matcra587/jira-cli/releases/tag/v0.15.2" {
				t.Fatalf("query %q did not select expected release metadata", query)
			}
		})
	}
	for _, query := range []string{"", "latest", "V0.15.2", "vv0.15.2", "0.15", "0.15.20", "../../v0.15.2"} {
		t.Run("missing_"+query, func(t *testing.T) {
			got, ok := Find(query)
			if ok || !reflect.DeepEqual(got, Release{}) {
				t.Fatalf("unknown query %q produced a release", query)
			}
		})
	}
}

func TestFullReleaseMarkdown(t *testing.T) {
	if Header() != "# Release Notes" {
		t.Fatalf("header: %q", Header())
	}
	full := Full()
	if !strings.HasPrefix(full, Header()+"\n\n") || !strings.HasSuffix(full, "\n") || strings.HasSuffix(full, "\n\n") {
		t.Fatal("full Markdown lost header or canonical final newline")
	}
	position := len(Header())
	for _, release := range Releases() {
		if strings.Count(full, release.Markdown) != 1 {
			t.Fatalf("%s must appear once with unchanged Markdown", release.Tag)
		}
		next := strings.Index(full, release.Markdown)
		if next <= position {
			t.Fatalf("%s appears out of catalog order", release.Tag)
		}
		position = next
	}
}

func TestReleaseCatalogOwnership(t *testing.T) {
	for _, source := range []string{"releases", "find"} {
		for _, mutation := range []string{"release", "section", "change"} {
			t.Run(source+"_"+mutation, func(t *testing.T) {
				want := parseReleases()
				var got []Release
				if source == "releases" {
					got = Releases()
				} else {
					release, ok := Find(want[0].Tag)
					if !ok {
						t.Fatal("known release missing")
					}
					got = []Release{release}
				}
				switch mutation {
				case "release":
					original := got[0]
					t.Cleanup(func() { got[0] = original })
					got[0] = Release{Tag: "caller changed release"}
				case "section":
					original := got[0].Sections[0]
					t.Cleanup(func() { got[0].Sections[0] = original })
					got[0].Sections[0] = Section{Kind: "caller changed section"}
				case "change":
					original := got[0].Sections[0].Changes[0]
					t.Cleanup(func() { got[0].Sections[0].Changes[0] = original })
					got[0].Sections[0].Changes[0] = "caller changed change"
				}
				if !reflect.DeepEqual(Releases(), want) {
					t.Fatal("caller mutation changed subsequent release catalog")
				}
			})
		}
	}
}

func TestReleaseMarkdownParsing(t *testing.T) {
	markdown := "ignored introduction\n- orphan\n## [display version](https://example.test/release) — 2026-08-01\n\n### Added\ntext before first bullet\n- keep **Markdown** & <literal>  \n  wrapped continuation\n\n- second\n### Empty\n### Fixed\n- final\n"
	got := parseRelease("v1.2.3", markdown)
	want := Release{
		Version: "1.2.3", Tag: "v1.2.3", URL: "https://example.test/release", Date: "2026-08-01", Markdown: markdown,
		Sections: []Section{{Kind: "Added", Changes: []string{"keep **Markdown** & <literal> wrapped continuation", "second"}}, {Kind: "Empty"}, {Kind: "Fixed", Changes: []string{"final"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed release differs: %#v", got)
	}
	for _, heading := range []string{"## [1](https://example.test) - 2026-08-01", "## [1](https://example.test) — 2026-08-01  "} {
		url, date := parseHeading(heading)
		if url != "https://example.test" || date != "2026-08-01" {
			t.Fatalf("heading metadata lost: %q", heading)
		}
	}
	for _, markdown := range []string{"", "# Header\n- orphan", "## malformed\n## [1](https://example.test) - date"} {
		url, date := parseHeading(markdown)
		if url != "" || date != "" {
			t.Fatalf("unexpected metadata from %q", markdown)
		}
	}
}

func TestReleaseJSONShape(t *testing.T) {
	got, err := json.Marshal(Release{Version: "1.2.3", Tag: "v1.2.3", Markdown: "private renderer text", Sections: []Section{{Kind: "Empty"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":"1.2.3","tag":"v1.2.3","sections":[{"kind":"Empty","changes":null}]}`
	if string(got) != want {
		t.Fatalf("release JSON: %s; want %s", got, want)
	}
}

func TestReleaseCopyPreservesEmptyShape(t *testing.T) {
	for _, original := range []Release{
		{},
		{Sections: []Section{}},
		{Sections: []Section{{Kind: "Absent changes"}, {Kind: "Empty changes", Changes: []string{}}}},
	} {
		if got := cloneRelease(original); !reflect.DeepEqual(got, original) {
			t.Fatalf("copy changed absent/empty shape: %#v; want %#v", got, original)
		}
	}
}
