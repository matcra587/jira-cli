package adfcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const (
	flagEdited       = "markdown-file"
	flagCurrent      = "current"
	anchoredEditHelp = "Local anchored editing preserves unknown native nodes, attributes, marks and root extensions. " +
		"Keep the exact original ADF snapshot used by prepare, and keep anchor comments on their own top-level lines. " +
		"Editable regions may be changed, deleted or reordered; protected regions may be moved but must remain unchanged and present exactly once. " +
		"Markdown outside anchors inserts new blocks. Newly authored content is validated; preserved server content is not rejected against the older schema. " +
		"No credentials, Jira requests or input file modifications. Files and piped stdin share the body size limit; terminal stdin is refused. " +
		"At most one input may be - for stdin. These commands produce local artifacts, not remote write preconditions or an atomic Jira edit. "
	anchoredCandidateHelp = anchoredEditHelp + "Require --markdown-file for edited anchored text; an empty edited input deletes all editable regions. " +
		"--input selects the exact original native document (default -). JSON puts the candidate at .data.document; compact at .document. " +
		"A refused edit exits 3 and retains diagnostics with kind, region and reason, never a partial document. Extract .data.document with --jq for a native payload."
)

func newEdit() *cobra.Command {
	group := &cobra.Command{Use: "edit", Short: "Prepare and apply anchored edits to native ADF", Args: cobra.NoArgs}
	group.AddCommand(newEditPrepare(), newEditApply(), newEditMerge())
	return group
}

func newEditPrepare() *cobra.Command {
	var input string
	return command.MustNew(command.Spec[envelope.ADFEditPrepareOutput]{
		Op: "adf.edit.prepare", Use: "prepare", Short: "Project an immutable ADF snapshot to anchored Markdown",
		Long: anchoredEditHelp + "Pass the native document itself through --input (default -), without wrappers. " +
			"JSON returns anchored text at .data.markdown and region identity/protection at .data.regions; compact returns these fields directly. " +
			"Extract Markdown with --jq, edit that file, then use apply or merge with the original snapshot. Changing the original snapshot invalidates its anchors.",
		Example: "$ jira adf edit prepare --input document.json --output=json\n\n" +
			"$ jira adf edit prepare --input document.json --jq '.data.markdown'",
		Flags: func(c *cobra.Command) { addInput(c, &input, "Original ADF JSON") },
		Human: renderEditPrepared,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ADFEditPrepareOutput, error) {
			doc, err := readEditDocument(ctx, inv, flagInput, input)
			if err != nil {
				return envelope.ADFEditPrepareOutput{}, err
			}
			session, err := adf.PrepareEdit(doc)
			if err != nil {
				return envelope.ADFEditPrepareOutput{}, editInputError(flagInput, err)
			}
			if err := ctx.Err(); err != nil {
				return envelope.ADFEditPrepareOutput{}, err
			}
			regions := make([]envelope.ADFEditRegion, 0, len(session.Regions()))
			for _, region := range session.Regions() {
				regions = append(regions, envelope.ADFEditRegion{ID: region.ID, NodeType: region.NodeType, Protected: region.Protected})
			}
			return envelope.ADFEditPrepareOutput{Markdown: session.Markdown(), Regions: regions}, nil
		},
	})
}

func newEditApply() *cobra.Command {
	return newEditCandidate(command.Spec[envelope.ADFEditOutput]{
		Op: "adf.edit.apply", Use: "apply", Short: "Apply anchored Markdown to its original native snapshot",
		Long: anchoredCandidateHelp,
		Example: "$ jira adf edit apply --input document.json --markdown-file edited.md --output=json\n\n" +
			"$ jira adf edit apply --input document.json --markdown-file edited.md --jq '.data.document'",
		Human: renderEditCandidate,
	}, false)
}

func newEditMerge() *cobra.Command {
	return newEditCandidate(command.Spec[envelope.ADFEditOutput]{
		Op: "adf.edit.merge", Use: "merge", Short: "Reconcile anchored edits with a current native document",
		Long: anchoredCandidateHelp + " Require --current for a separately read current native document. Merge keeps its root extensions and untouched blocks. " +
			"Conflicting edits and ambiguous block identities are refused. Even a successful merge needs a separate server-enforced write precondition before replacing a remote document.",
		Example: "$ jira adf edit merge --input document.json --markdown-file edited.md --current current.json --output=json\n\n" +
			"$ jira adf edit merge --input document.json --markdown-file edited.md --current current.json --jq '.data.document'",
		Human: renderEditCandidate,
	}, true)
}

func newEditCandidate(spec command.Spec[envelope.ADFEditOutput], merge bool) *cobra.Command {
	var input, edited, current string
	spec.Passthrough = true
	spec.Flags = func(c *cobra.Command) {
		addInput(c, &input, "Original ADF JSON")
		command.AddStringVar(c, &edited, flagEdited, "", "Edited anchored Markdown file; - reads piped stdin", command.FlagOpts{Terse: "Edited Markdown", Placeholder: "file", Hint: "file"})
		if merge {
			command.AddStringVar(c, &current, flagCurrent, "", "Current native ADF file; - reads piped stdin", command.FlagOpts{Terse: "Current ADF", Placeholder: "file", Hint: "file"})
		}
	}
	spec.Validate = func() error { return validateEditInputs(input, edited, current, merge) }
	spec.RetainOnError = func(out envelope.ADFEditOutput) bool { return len(out.Diagnostics) > 0 }
	spec.Run = func(ctx context.Context, inv command.Invocation) (envelope.ADFEditOutput, error) {
		doc, err := readEditDocument(ctx, inv, flagInput, input)
		if err != nil {
			return envelope.ADFEditOutput{}, err
		}
		session, err := adf.PrepareEdit(doc)
		if err != nil {
			return envelope.ADFEditOutput{}, editInputError(flagInput, err)
		}
		markdown, err := readTextInput(ctx, inv, flagEdited, edited, true)
		if err != nil {
			return envelope.ADFEditOutput{}, err
		}
		var candidate *adf.Document
		if merge {
			remote, readErr := readEditDocument(ctx, inv, flagCurrent, current)
			if readErr != nil {
				return envelope.ADFEditOutput{}, readErr
			}
			candidate, err = session.Merge(string(markdown), remote)
		} else {
			candidate, err = session.Apply(string(markdown))
		}
		if err != nil {
			out := envelope.ADFEditOutput{}
			if failure, ok := errors.AsType[*adf.EditError](err); ok {
				if merge && failure.Kind == adf.EditInvalidSource {
					return out, editInputError(flagCurrent, err)
				}
				out.Diagnostics = []envelope.ADFEditDiagnostic{{Kind: string(failure.Kind), Region: failure.Region, Reason: failure.Reason}}
			}
			return out, editInputError(flagEdited, err)
		}
		data, err := candidate.Marshal()
		if err != nil {
			return envelope.ADFEditOutput{}, err
		}
		if err := ctx.Err(); err != nil {
			return envelope.ADFEditOutput{}, err
		}
		return envelope.ADFEditOutput{Document: data, Diagnostics: []envelope.ADFEditDiagnostic{}}, nil
	}
	return command.MustNew(spec)
}

func validateEditInputs(input, edited, current string, merge bool) error {
	for _, field := range []struct {
		name, value string
		required    bool
	}{
		{name: flagEdited, value: edited, required: true},
		{name: flagCurrent, value: current, required: merge},
	} {
		if field.required && field.value == "" {
			err := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+field.name+" is required")
			err.Field = field.name
			return err
		}
	}
	stdinCount := 0
	for _, path := range []string{input, edited, current} {
		if path == command.StdinPath {
			stdinCount++
		}
	}
	if stdinCount > 1 {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "only one editing input can read stdin; supply files for the others")
		err.Field = flagEdited
		return err
	}
	return nil
}

func readEditDocument(ctx context.Context, inv command.Invocation, field, path string) (*adf.Document, error) {
	data, err := readTextInput(ctx, inv, field, path, false)
	if err != nil {
		return nil, err
	}
	// The preservation owner refuses duplicate object keys. Check that native
	// invariant before any authored edit can mask a malformed current input.
	// Structural comparison imposes no schema restriction on future nodes.
	if _, err := adf.StructuralEqual(data, data); err != nil {
		return nil, editInputError(field, err)
	}
	doc, err := adf.ParseDocument(data)
	if err != nil {
		return nil, editInputError(field, err)
	}
	// Editing and merging require version 1, but preserved future nodes must
	// not be rejected against the older schema. Keep native-input failures
	// attached to their own field before applying the authored Markdown.
	if version, err := doc.Version(); err != nil || version != 1 {
		failure := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+field+" must be a native ADF document with version 1")
		failure.Field = field
		return nil, failure
	}
	return doc, nil
}

func editInputError(field string, err error) error {
	input := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+field+" cannot be used for anchored ADF editing: "+err.Error())
	input.Field = field
	return input
}

func renderEditPrepared(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFEditPrepareOutput)
	if !ok {
		return fmt.Errorf("adf.edit.prepare: unexpected payload %T", env.Data)
	}
	v.Section("Anchored Markdown")
	for _, line := range strings.Split(out.Markdown, "\n") {
		v.Line(cli.Text(line))
	}
	return nil
}

func renderEditCandidate(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFEditOutput)
	if !ok {
		return fmt.Errorf("adf.edit: unexpected payload %T", env.Data)
	}
	if len(out.Diagnostics) > 0 {
		for _, diagnostic := range out.Diagnostics {
			v.Fields(output.Field{Label: cli.Text(diagnostic.Kind), Value: cli.Text(diagnostic.Reason)})
		}
		return nil
	}
	// Reuse the native candidate renderer without converting or rebuilding ADF.
	env.Data = envelope.ADFConvertOutput{Document: out.Document}
	return renderConverted(v, env)
}
