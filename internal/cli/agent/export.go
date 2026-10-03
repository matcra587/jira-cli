package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/x/shell"
	"github.com/matcra587/docent"
	"github.com/matcra587/docent/harness"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/agentguides"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// ExportWriter is the local filesystem integration boundary, including partial
// publication evidence. Help and previews never invoke it.
type ExportWriter func(context.Context, string, []agentguides.ExportArtifact, bool) (agentguides.ExportReceipt, error)

type exportOptions struct {
	format, directory, scope, harness string
	force                             bool
}

func newExport(guides *docent.GuideSet, write ExportWriter) *cobra.Command {
	if write == nil {
		write = agentguides.WriteExport
	}
	var options exportOptions
	return command.MustNew(command.Spec[envelope.GuideExportOutput]{
		Op: "agent.export", Use: "export", Short: "Export embedded guides as agent skills",
		MutatesLocalState: true,
		Long: "Export every embedded guide in canonical order, without Jira credentials or requests. " +
			"Each skill is named jira-<guide-slug> and written as <name>/SKILL.md. " +
			"agent-skill is portable; claude-skill carries Claude's native when_to_use field.\n\n" +
			"Choose --dir with --format, or --scope project/user with a selected or detected --harness. " +
			"Supported harnesses are claude-code and codex. Scope defaults to that harness's format; --format overrides it. " +
			"Project scope uses the working directory, user scope the home directory. " +
			"--harness requires --scope. No destination is chosen implicitly.\n\n" +
			"--dry-run renders the complete plan and digests without inspecting or changing destination files. " +
			"An existing artifact requires --force; no interactive prompt is used. " +
			"Publication is confined beneath the selected directory. Files are published one at a time, " +
			"so a failure or cancellation retains the complete plan, written_paths and attempted_path. " +
			"Inspect those facts before rerunning. --force replaces entries without following file symlinks. " +
			"Jira read-only policy does not prohibit local skill exports.",
		Example: "$ jira agent export --format agent-skill --dir skills --dry-run --output=json\n\n" +
			"$ jira agent export --format agent-skill --dir skills --output=json\n\n" +
			"$ jira agent export --scope project --harness codex --dry-run --output=compact",
		Flags: func(c *cobra.Command) {
			for _, flag := range []struct {
				name, usage, terse string
				value              *string
				enum               []string
			}{
				{"format", "Skill format; required with --dir", "Export format", &options.format, agentguides.ExportFormats()},
				{"dir", "Directory for exported artifacts (requires --format)", "Export directory", &options.directory, nil},
				{"scope", "Resolve a harness skills directory: project or user", "Install scope", &options.scope, []string{harness.ScopeProject, harness.ScopeUser}},
				{"harness", "Select an agent harness (requires --scope)", "Agent harness", &options.harness, harnessNames()},
			} {
				command.AddStringVar(c, flag.value, flag.name, "", flag.usage, command.FlagOpts{Terse: flag.terse, Placeholder: flag.name})
				clibcobra.Extend(c.Flags().Lookup(flag.name), clibcobra.FlagExtra{Terse: flag.terse, Placeholder: flag.name, Enum: flag.enum})
			}
			command.AddBoolVar(c, &options.force, "force", false, "Allow replacement of existing exported artifacts", command.FlagOpts{Terse: "Replace artifacts"})
			command.AddDryRunFlag(c)
			c.MarkFlagsMutuallyExclusive("dir", "scope")
		},
		Human: renderExport,
		RetainOnError: func(out envelope.GuideExportOutput) bool {
			return out.Attempted
		},
		Run: func(ctx context.Context, inv command.Invocation) (envelope.GuideExportOutput, error) {
			return runExport(ctx, guides, options, inv.DryRun, os.LookupEnv, write)
		},
	})
}

func harnessNames() []string {
	var names []string
	for _, h := range harness.Supported() {
		names = append(names, h.Name)
	}
	return names
}

func resolveExport(options exportOptions, lookup func(string) (string, bool)) (exportOptions, error) {
	invalid := func(code, field, message string) (exportOptions, error) {
		err := cli.NewInputError(code, message)
		err.Field = field
		err.Suggestions = []string{"jira agent export --format agent-skill --dir skills --dry-run --output=json"}
		return exportOptions{}, err
	}
	if options.directory != "" && options.scope != "" {
		return invalid(cli.CodeFlagConflict, "scope", "--dir and --scope are mutually exclusive")
	}
	if options.directory == "" && options.scope == "" {
		return invalid(cli.CodeFlagRequired, "dir", "choose --dir with --format, or --scope with a supported harness")
	}
	if options.scope == "" {
		if options.harness != "" {
			return invalid(cli.CodeFlagNotApplicable, "harness", "--harness requires --scope")
		}
		if options.format == "" {
			return invalid(cli.CodeFlagRequired, "format", "--dir requires --format")
		}
	} else {
		if err := harness.ValidateScope(options.scope); err != nil {
			return invalid(cli.CodeFlagValueInvalid, "scope", err.Error())
		}
		selected, detected := harness.Detect(lookup)
		if options.harness != "" {
			detected = false
			for _, h := range harness.Supported() {
				if h.Name == options.harness {
					selected, detected = h, true
					break
				}
			}
		}
		if !detected {
			return invalid(cli.CodeFlagValueInvalid, "harness", "select --harness claude-code or codex; no supported harness was resolved")
		}
		options.harness = selected.Name
		if options.format == "" {
			options.format = selected.DefaultFormat
		}
		var err error
		options.directory, err = selected.SkillsPath(options.scope)
		if err != nil {
			return exportOptions{}, cli.NewCodedError(cli.CodeLocalIO, err.Error())
		}
	}
	if !slices.Contains(agentguides.ExportFormats(), options.format) {
		return invalid(cli.CodeFlagValueInvalid, "format", "unsupported skill format: "+options.format)
	}
	if strings.TrimSpace(options.directory) == "" || strings.ContainsFunc(options.directory, unicode.IsControl) {
		return invalid(cli.CodeFlagValueInvalid, "dir", "export directory must be a nonempty path without control characters")
	}
	directory, err := filepath.Abs(options.directory)
	if err != nil {
		return exportOptions{}, cli.NewCodedError(cli.CodeLocalIO, err.Error())
	}
	options.directory = directory
	return options, nil
}

func runExport(ctx context.Context, guides *docent.GuideSet, options exportOptions, dryRun bool, lookup func(string) (string, bool), write ExportWriter) (envelope.GuideExportOutput, error) {
	options, err := resolveExport(options, lookup)
	if err != nil {
		return envelope.GuideExportOutput{}, err
	}
	artifacts, err := agentguides.PlanExport(guides, options.format)
	if err != nil {
		return envelope.GuideExportOutput{}, cli.NewCodedError(cli.CodeLocalIO, err.Error())
	}
	out := envelope.GuideExportOutput{
		ContractVersion: envelope.ContractVersion, Format: options.format, Directory: options.directory,
		Scope: options.scope, Harness: options.harness, WrittenPaths: []string{},
		Artifacts: make([]envelope.GuideExportArtifact, 0, len(artifacts)),
	}
	out.DryRun = dryRun
	for _, artifact := range artifacts {
		out.Artifacts = append(out.Artifacts, envelope.GuideExportArtifact{
			Slug: artifact.Slug, Path: artifact.Path, Bytes: len(artifact.Content), SHA256: fmt.Sprintf("%x", sha256.Sum256(artifact.Content)),
		})
	}
	if dryRun {
		return out, ctx.Err()
	}
	out.Attempted = true
	receipt, err := write(ctx, options.directory, artifacts, options.force)
	out.AttemptedPath = receipt.Attempted
	out.WrittenPaths = append(out.WrittenPaths, receipt.Written...)
	for i := range out.Artifacts {
		out.Artifacts[i].Written = slices.Contains(receipt.Written, out.Artifacts[i].Path)
	}
	if errors.Is(err, os.ErrExist) {
		conflict := cli.NewInputError(cli.CodeFlagConflict, "an exported artifact already exists; replacement requires --force")
		conflict.Field = "force"
		conflict.Suggestions = []string{"jira agent export --format " + options.format + " --dir " + shell.Quote(options.directory) + " --force --output=json"}
		return out, conflict
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		err = cli.NewCodedError(cli.CodeLocalIO, err.Error())
	}
	return out, err
}

func renderExport(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.GuideExportOutput)
	if !ok {
		return fmt.Errorf("agent.export: renderer received %T, want GuideExportOutput", env.Data)
	}
	v.Line("# " + cli.Text(out.Format+" "+out.Harness+" "+out.Directory))
	for _, artifact := range out.Artifacts {
		state := "not written"
		if out.DryRun {
			state = "would write"
		} else if artifact.Written {
			state = "written"
		}
		v.Line(state + " " + cli.Text(artifact.Path))
	}
	return nil
}
