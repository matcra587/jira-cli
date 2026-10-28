package root

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/matcra587/docent"
	"github.com/matcra587/docent/docenttest"

	"github.com/matcra587/jira-cli/internal/agentguides"
	"github.com/matcra587/jira-cli/internal/cli/discovery"
)

func TestEmbeddedGuidesMatchAssembledCommandTree(t *testing.T) {
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	if guides.Len() != 13 {
		t.Fatalf("embedded guide corpus has %d guides, want 13", guides.Len())
	}
	program := NewProgram(testDeps(t), io.Discard, io.Discard)
	tree, err := discovery.Build(program.Root)
	if err != nil {
		t.Fatal(err)
	}
	docenttest.Validate(t, agentguides.Files(), tree)
}

func TestEmbeddedGuideValidationDetectsViolations(t *testing.T) {
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	program := NewProgram(testDeps(t), io.Discard, io.Discard)
	tree, err := discovery.Build(program.Root)
	if err != nil {
		t.Fatal(err)
	}
	discover, _ := guides.Get("discover-cli")
	machine, _ := guides.Get("machine-output")
	base := string(discover.Raw)
	description := "description: Read the installed CLI's guides, canonical command tree, flags and output shapes."
	when := "when_to_use: When starting a Jira CLI task or recovering from an unfamiliar command or flag."
	tests := []struct {
		name, file, body string
		extra            string
		want             error
	}{
		{"missing frontmatter", "discover-cli.md", strings.TrimPrefix(base, "---\n"), "", docent.ErrMissingFrontmatter},
		{"invalid frontmatter", "discover-cli.md", strings.Replace(base, "slug: discover-cli", "slug: [", 1), "", docent.ErrInvalidFrontmatter},
		{"missing field", "discover-cli.md", strings.Replace(base, when+"\n", "", 1), "", docent.ErrMissingField},
		{"slug mismatch", "discover-cli.md", strings.Replace(base, "slug: discover-cli", "slug: other-guide", 1), "", docent.ErrSlugMismatch},
		{"invalid slug", "BAD.md", strings.Replace(base, "slug: discover-cli", "slug: BAD", 1), "", docent.ErrInvalidSlug},
		{"description budget", "discover-cli.md", strings.Replace(base, description, "description: "+strings.Repeat("x", docent.MaxSkillDescription+1), 1), "", docent.ErrDescriptionTooLong},
		{"compatibility budget", "discover-cli.md", strings.Replace(base, "order: 10", "order: 10\ncompatibility: "+strings.Repeat("x", docent.MaxSkillCompatibility+1), 1), "", docent.ErrCompatibilityTooLong},
		{"sections", "discover-cli.md", strings.Replace(base, "## Decide", "## Unknown", 1), "", docent.ErrInvalidSections},
		{"duplicate order", "discover-cli.md", strings.Replace(base, "order: 10", "order: 20", 1), string(machine.Raw), docent.ErrDuplicateOrder},
		{"alias collision", "discover-cli.md", strings.Replace(base, "order: 10", "order: 10\naliases: [machine-output]", 1), string(machine.Raw), docent.ErrAliasCollision},
		{"multiline field", "discover-cli.md", strings.Replace(base, description, "description: |\n  first line\n  second line", 1), "", docent.ErrMultilineField},
		{"form feed", "discover-cli.md", base + "\f", "", docent.ErrFormFeed},
		{"unknown command", "discover-cli.md", strings.Replace(base, "jira agent schema", "jira absent-command", 1), "", docent.ErrCommandNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := fstest.MapFS{tt.file: &fstest.MapFile{Data: []byte(tt.body)}}
			if tt.extra != "" {
				files["machine-output.md"] = &fstest.MapFile{Data: []byte(tt.extra)}
			}
			for _, problem := range docenttest.Check(files, tree) {
				if errors.Is(problem, tt.want) {
					return
				}
			}
			t.Fatalf("guide violation did not report %v", tt.want)
		})
	}
}
