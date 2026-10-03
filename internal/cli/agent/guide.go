package agent

import (
	"context"
	"fmt"
	"strings"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/matcra587/docent"
	docentexport "github.com/matcra587/docent/export"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const flagSection = "section"

// NewGuide mounts the human door onto the same source and handler as agent guide.
func NewGuide(guides *docent.GuideSet) *cobra.Command {
	return newGuide(guides, command.Spec[envelope.GuideOutput]{Op: "guide", Human: renderGuide})
}

func newGuide(guides *docent.GuideSet, spec command.Spec[envelope.GuideOutput]) *cobra.Command {
	var section string
	var all bool
	prefix := "jira " + strings.ReplaceAll(spec.Op, ".", " ")
	var slugs []string
	for g := range guides.All() {
		slugs = append(slugs, g.Slug)
	}
	spec.Use = "guide [SLUG]"
	spec.Short = "Read embedded CLI workflow guides"
	spec.Long = "Read workflow instructions without credentials or network access. " +
		"No slug returns a frontmatter index; a slug returns its six-section runbook. " +
		"Aliases, case-insensitive names and unambiguous name fragments resolve through the guide source. " +
		"--section selects one section, and --all returns the complete guide set. " +
		"--section needs a slug; --all cannot accompany a slug or --section.\n\n" +
		"Available guides: " + strings.Join(slugs, ", ") + ".\n\n" +
		"JSON carries the document at .data.document; compact mode at .document. " +
		"--jq '.data.document' extracts canonical Markdown. --all uses form-feed separators in that document. " +
		"Human mode prints Markdown with blank lines between guides. The top-level guide and agent guide doors use the same source."
	spec.Example = "$ " + prefix + " --output=json\n\n" +
		"$ " + prefix + " raw-api --section Run --jq '.data.document'\n\n" +
		"$ " + prefix + " --all --output=compact"
	spec.Args = command.ArgSpec{Min: 0, Max: 1, Positions: []command.Arg{{Name: "SLUG", Predictor: "guide"}}}
	spec.Flags = func(c *cobra.Command) {
		command.AddStringVar(c, &section, flagSection, "", "Read one named section (requires a guide slug)", command.FlagOpts{Terse: "Guide section", Placeholder: "section"})
		clibcobra.Extend(c.Flags().Lookup(flagSection), clibcobra.FlagExtra{Terse: "Guide section", Placeholder: "section", Enum: docent.SectionHeadings()})
		command.AddBoolVar(c, &all, "all", false, "Read every guide in canonical order", command.FlagOpts{Terse: "All guides"})
		c.MarkFlagsMutuallyExclusive(flagSection, "all")
	}
	spec.Run = func(_ context.Context, inv command.Invocation) (envelope.GuideOutput, error) {
		return readGuide(guides, prefix, inv.Args, section, all)
	}
	return command.MustNew(spec)
}

func readGuide(guides *docent.GuideSet, prefix string, args []string, section string, all bool) (envelope.GuideOutput, error) {
	out := envelope.GuideOutput{ContractVersion: envelope.ContractVersion}
	if all && len(args) != 0 {
		err := cli.NewInputError(cli.CodeFlagConflict, "--all cannot accompany a guide slug")
		err.Field = "all"
		return out, err
	}
	if section != "" && len(args) == 0 {
		err := cli.NewInputError(cli.CodeFlagNotApplicable, "--section requires a guide slug")
		err.Field = flagSection
		err.Suggestions = []string{prefix + " --output=json"}
		return out, err
	}
	if len(args) == 0 {
		selected := guides.Guides()
		out.Guides = guideSummaries(selected)
		if all {
			out.Kind, out.Document = "all", docentexport.Concat(selected)
			return out, nil
		}
		out.Kind = "index"
		var err error
		out.Document, err = docentexport.Index(selected, envelope.ContractVersion)
		return out, err
	}
	guide, ok := guides.Resolve(args[0])
	if !ok {
		err := cli.NewInputError(cli.CodeArgValueInvalid, "unknown or ambiguous guide name: "+args[0])
		err.Field = "slug"
		err.Suggestions = []string{prefix + " --output=json"}
		return out, err
	}
	out.Slug = guide.Slug
	out.Guides = guideSummaries([]docent.Guide{guide})
	if section != "" {
		part, ok := guide.Section(section)
		if !ok {
			err := cli.NewInputError(cli.CodeFlagValueInvalid, "unknown guide section: "+section)
			err.Field = flagSection
			for _, heading := range docent.SectionHeadings() {
				err.Suggestions = append(err.Suggestions, prefix+" "+guide.Slug+" --section "+heading+" --output=json")
			}
			return out, err
		}
		out.Kind, out.Section, out.Document = "section", part.Heading, part.Body+"\n"
		return out, nil
	}
	out.Kind, out.Document = "runbook", docentexport.Runbook{}.Render(guide)
	return out, nil
}

func guideSummaries(guides []docent.Guide) []envelope.GuideSummary {
	summaries := make([]envelope.GuideSummary, 0, len(guides))
	for _, g := range guides {
		summaries = append(summaries, envelope.GuideSummary{
			Slug: g.Slug, Title: g.Title,
			Description: g.Description, WhenToUse: g.WhenToUse, Commands: g.Commands,
			Aliases: g.Aliases, Order: g.Order,
		})
	}
	return summaries
}

func renderGuide(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.GuideOutput)
	if !ok {
		return fmt.Errorf("agent: renderer received %T, want GuideOutput", env.Data)
	}
	// Form feeds belong to the canonical machine document. The human layout
	// uses blank separators while every source line passes through its sink.
	document := strings.ReplaceAll(out.Document, docentexport.GuideSeparator, "\n")
	for _, line := range strings.Split(strings.TrimSuffix(document, "\n"), "\n") {
		v.Line(cli.Text(line))
	}
	return nil
}
