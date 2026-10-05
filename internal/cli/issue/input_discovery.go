package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gechr/x/human"
	xstrings "github.com/gechr/x/strings"
	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

const flagGenerateJSON = "generate-json"

func registerInputDiscovery(cmd *cobra.Command) {
	var selected bool
	command.AddBoolVar(cmd, &selected, flagGenerateJSON, false,
		"Read live create/edit metadata and generate a native JSON skeleton with field-value placeholders",
		command.FlagOpts{Terse: "Live input skeleton"})
}

// Refuse every explicitly supplied local option outside the read's surface,
// including false/empty values. Canonical names come from the existing alias
// owners; an authored-input alias cannot slip through to file or Jira I/O.
func validateInputDiscoveryFlags(cmd *cobra.Command, allowed ...string) error {
	aliases := append(slices.Clone(command.IssueFieldAliases), command.BodyFlagAliases...)
	aliases = append(aliases,
		command.HiddenAlias{Flag: command.FlagYes, Canonical: command.FlagForce},
		command.HiddenAlias{Flag: command.FlagSkipNotify, Canonical: command.FlagNotify})
	var refusal error
	// Cobra builds this set by copying flag pointers, without pflag's parsed
	// membership. VisitAll plus Changed observes the real parser's facts.
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Changed {
			return
		}
		name := flag.Name
		for _, alias := range aliases {
			if alias.Flag == name {
				name = alias.Canonical
				break
			}
		}
		if refusal == nil && name != flagGenerateJSON && !slices.Contains(allowed, name) {
			e := cli.NewInputError(cli.CodeFlagNotApplicable, fmt.Sprintf("--%s is not applicable with --%s; input discovery reads metadata without composing or submitting authored changes", name, flagGenerateJSON))
			e.Field = name
			refusal = e
		}
	})
	if refusal != nil {
		return refusal
	}
	if cmd.Flags().Changed(flagWeb) {
		e := cli.NewInputError(cli.CodeFlagNotApplicable, "--web does not open input metadata; drop it when using --generate-json")
		e.Field = flagWeb
		return e
	}
	return nil
}

func validateCreateInput(in *issueWriteFlags) error {
	if err := validateInputDiscoveryFlags(in.cmd, command.FlagProject, command.FlagIssueType); err != nil {
		return err
	}
	for _, fact := range []struct {
		flag  string
		value string
	}{
		{flag: command.FlagProject, value: in.fields.Project},
		{flag: command.FlagIssueType, value: in.fields.IssueType},
	} {
		if xstrings.IsBlank(fact.value) {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+fact.flag+" must name an explicit nonblank create-metadata context with --generate-json")
			e.Field = fact.flag
			return e
		}
	}
	return nil
}

func newInputDiscoveryClient(deps Deps) (InputDiscoveryClient, error) {
	if deps.IssueMetadata == nil {
		return nil, core.ErrIssueMetadataUnavailable
	}
	client, err := deps.IssueMetadata()
	if err == nil && client == nil {
		err = core.ErrIssueMetadataUnavailable
	}
	return client, err
}

func runCreateInput(ctx context.Context, deps Deps, project, issueType string) (envelope.IssueCreateInputOutput, error) {
	out := envelope.IssueCreateInputOutput{}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	client, err := newInputDiscoveryClient(deps)
	if err != nil {
		return out, err
	}
	_, err = command.Spin(ctx, "Reading create input metadata", func(ctx context.Context) (struct{}, error) {
		read, err := client.GetCreateIssueProject(ctx, project)
		if err != nil {
			return struct{}{}, err
		}
		// Do not trust decoded facts supplied beside a native document by
		// an alternate client. Both context and placeholders use this read.
		resolved, err := core.DecodeIssueCreateProject(read.Native)
		if err != nil {
			return struct{}{}, err
		}
		out.Project = resolved.Native
		metadata, err := core.DiscoverCreateIssueMetadata(ctx, client, resolved.ID, issueType)
		if err != nil {
			if resolution, ok := errors.AsType[*core.IssueTypeResolutionError](err); ok {
				out.IssueTypes = &resolution.Candidates
			}
			return struct{}{}, err
		}
		payload, err := buildInputSkeleton(metadata.Fields, resolved.ID, metadata.IssueTypeID)
		if err != nil {
			return struct{}{}, err
		}
		out.IssueType, out.Fields = metadata.IssueType, &metadata.Fields
		out.TypePages, out.FieldPages = metadata.TypePages, metadata.FieldPages
		out.Payload, out.Complete = payload, true
		return struct{}{}, ctx.Err()
	})
	return out, err
}

type editInputOutcome struct {
	result  envelope.KeyedResult[envelope.IssueEditInputData]
	err     error
	started bool
}

func runEditInput(ctx context.Context, deps Deps, inv command.Invocation, selector command.SelectorFlags, parallelism int) (envelope.IssueEditInputOutput, error) {
	var selectedClient core.JiraClient
	targets := command.Targets{Keys: inv.Args}
	if ctx.Err() == nil {
		var err error
		newClient := oneClient(func() (core.JiraClient, error) {
			if deps.JiraClient == nil {
				return nil, core.ErrIssueMetadataUnavailable
			}
			client, err := deps.JiraClient()
			if err == nil && client == nil {
				err = core.ErrIssueMetadataUnavailable
			}
			return client, err
		})
		selectedClient, targets, err = command.ResolveTargets(ctx, newClient, selector, inv.Args, false, false, inv.Stdin, inv.StdinIsTerminal)
		if err != nil {
			return envelope.IssueEditInputOutput{}, err
		}
	}
	var client core.IssueMetadataClient
	var setupErr error
	if ctx.Err() == nil {
		if selectedClient != nil {
			var ok bool
			client, ok = selectedClient.(core.IssueMetadataClient)
			if !ok {
				setupErr = core.ErrIssueMetadataUnavailable
			}
		} else {
			client, setupErr = newInputDiscoveryClient(deps)
		}
	}
	outcomes := make([]editInputOutcome, len(targets.Keys))
	_, err := command.Spin(ctx, "Reading input metadata for "+human.Pluralize(len(targets.Keys), "issue", "issues"), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(targets.Keys), func(i int) {
			outcomes[i] = readEditInput(ctx, client, targets.Keys[i], setupErr)
		})
		return struct{}{}, nil
	})
	out := envelope.IssueEditInputOutput{SelectorState: envelope.SelectorState{Selector: targets.Summary}}
	out.Results = make([]envelope.KeyedResult[envelope.IssueEditInputData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, outcome := range outcomes {
		out.Results[i], raw[i] = outcome.result, outcome.err
		out.ReadStarted = out.ReadStarted || outcome.started
	}
	return out, errors.Join(err, command.KeyedFailure(out.Results, raw))
}

func readEditInput(ctx context.Context, client core.IssueMetadataClient, key string, setupErr error) editInputOutcome {
	row := envelope.KeyedResult[envelope.IssueEditInputData]{Issue: envelope.IssueRef{Key: key}}
	started := false
	err := ctx.Err()
	if err == nil {
		err = setupErr
	}
	if err == nil {
		started = true
		read, readErr := client.GetEditIssueFields(ctx, key)
		err = readErr
		if err == nil {
			var metadata core.IssueEditMetadataRead
			metadata, err = core.DecodeEditIssueFields(read.Native)
			if err == nil {
				var payload json.RawMessage
				payload, err = buildInputSkeleton(metadata.Fields, "", "")
				if err == nil {
					row.Data = &envelope.IssueEditInputData{Metadata: metadata.Native, Fields: metadata.Fields, Payload: payload}
				}
			}
		}
	}
	if err != nil {
		class := cli.MapError(err)
		row.Err = &envelope.KeyError{Code: class.Code, Message: class.Message, Hint: class.Hint}
	} else {
		row.OK = true
	}
	return editInputOutcome{result: row, err: err, started: started}
}

type issueInputSkeleton struct {
	Fields map[string]json.RawMessage              `json:"fields"`
	Update map[string][]map[string]json.RawMessage `json:"update,omitempty"`
}

// Metadata supplies identities and operations, never chosen field values.
// Prefer a settable field's native fields route. For other advertised operations,
// publish separate update placeholders in returned order; callers keep only the
// wanted actions. Fields advertising no operations remain in metadata alone.
func buildInputSkeleton(fields map[string]json.RawMessage, projectID, issueTypeID string) (json.RawMessage, error) {
	payload := issueInputSkeleton{Fields: make(map[string]json.RawMessage), Update: make(map[string][]map[string]json.RawMessage)}
	for id, raw := range fields {
		// Exact native keys matter: a future "Operations" member must not
		// shadow the validated "operations" array through v1 case folding.
		var field map[string]json.RawMessage
		if err := json.Unmarshal(raw, &field); err != nil {
			return nil, fmt.Errorf("%w: decoding field definition: %w", core.ErrIssueMetadataInvalid, err)
		}
		var operations []string
		if err := json.Unmarshal(field["operations"], &operations); err != nil {
			return nil, fmt.Errorf("%w: decoding field operations: %w", core.ErrIssueMetadataInvalid, err)
		}
		if slices.Contains(operations, "set") {
			payload.Fields[id] = json.RawMessage("null")
			continue
		}
		for _, operation := range operations {
			payload.Update[id] = append(payload.Update[id], map[string]json.RawMessage{operation: json.RawMessage("null")})
		}
	}
	for _, identity := range []struct {
		field string
		value string
	}{
		{field: "project", value: projectID},
		{field: "issuetype", value: issueTypeID},
	} {
		if identity.value == "" {
			continue
		}
		value, err := jira.MarshalNoEscape(struct {
			ID string `json:"id"`
		}{ID: identity.value})
		if err != nil {
			return nil, err
		}
		payload.Fields[identity.field] = value
		delete(payload.Update, identity.field)
	}
	return jira.MarshalNoEscape(payload)
}

func renderCreateInput(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCreateInputOutput)
	if !ok {
		return fmt.Errorf("issue.create.input: human renderer received %T", env.Data)
	}
	if !out.Complete {
		v.Line(cli.Text("Create input discovery did not complete."))
		return nil
	}
	v.Hint("Null values are placeholders. Fill wanted values and remove unwanted optional fields or update operations before submitting.")
	v.Section(cli.Text("Native create skeleton"))
	v.Block(string(out.Payload))
	return renderInputFields(v, *out.Fields)
}

func renderEditInput(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueEditInputOutput)
	if !ok {
		return fmt.Errorf("issue.edit.input: human renderer received %T", env.Data)
	}
	v.Hint("Null values are placeholders. Fill wanted values and remove unwanted fields or update operations before submitting.")
	for i, row := range out.Results {
		if i > 0 {
			v.Line()
		}
		v.Section(cli.Text(row.Issue.Key))
		if !row.OK {
			v.Line(cli.Text(row.Err.Message))
			v.Hint(row.Err.Hint)
			continue
		}
		v.Block(string(row.Data.Payload))
		if err := renderInputFields(v, row.Data.Fields); err != nil {
			return err
		}
	}
	return nil
}

func renderInputFields(v *output.View, fields map[string]json.RawMessage) error {
	for _, id := range slices.Sorted(maps.Keys(fields)) {
		// Keep unknown case variants raw; only exact validated native names
		// supply the facts displayed to a person.
		var field map[string]json.RawMessage
		if err := json.Unmarshal(fields[id], &field); err != nil {
			return fmt.Errorf("rendering input field metadata: %w", err)
		}
		var name string
		if err := json.Unmarshal(field["name"], &name); err != nil {
			return fmt.Errorf("rendering input field name: %w", err)
		}
		var required bool
		if err := json.Unmarshal(field["required"], &required); err != nil {
			return fmt.Errorf("rendering input field requiredness: %w", err)
		}
		v.Section(cli.Text(id + " " + name))
		v.Fields(output.Field{Label: "Required", Value: cli.Text(strconv.FormatBool(required))}, output.Field{Label: "Schema", Value: cli.Text(string(field["schema"]))})
		if allowedValues, present := field["allowedValues"]; present {
			v.Fields(output.Field{Label: "Allowed values", Value: cli.Text(strings.TrimSpace(string(allowedValues)))})
		}
	}
	return nil
}
