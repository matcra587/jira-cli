package epic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	xslices "github.com/gechr/x/slices"
	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

type membershipOptions struct {
	keyed command.KeyedFlags
	mut   command.MutationFlags
	cmd   *cobra.Command
}

const membershipHelp = "Explicit child keys accept comma lists and inclusive ranges such as KEY-1:3 or KEY-1..KEY-3, " +
	"with at most 1,000 distinct targets. Duplicate targets are processed once in first-occurrence order. " +
	"Dry-run composes the exact parent payload locally without credentials or network access. " +
	"Add --validate-remote to a preview for parent checks without writing. " +
	"Explicit targets follow the shared mutation policy; --no-input disables prompting. Writes are never automatically replayed. " +
	"Each issue has an ordered result; mixed success retains failures inline. " +
	"--verify requests an immediate issue read, independent of the search index. " +
	"A failed or mismatched readback keeps the acknowledged write successful and reports verify_failed; inspect before retrying."

func (o *membershipOptions) flags(cmd *cobra.Command) {
	o.cmd = cmd
	command.AddKeyedFlags(cmd, &o.keyed)
	command.AddMutationFlags(cmd, &o.mut, command.WithValidateRemote(), command.WithVerify(), command.WithNotify())
}

func (o *membershipOptions) validate() error {
	if err := o.keyed.Validate(); err != nil {
		return err
	}
	return o.mut.ResolveNotify(o.cmd.Flags())
}

func newAdd(deps Deps) *cobra.Command {
	o := membershipOptions{}
	return command.MustNew(command.Spec[envelope.EpicMembershipOutput]{
		Op: "epic.add", WritesJira: true, Identity: true, Keyed: true,
		Use: "add ISSUE_KEY... EPIC_KEY", Short: "Assign explicit issues to an epic",
		Long: "The last positional argument is one epic key; preceding arguments are child keys. " +
			"The destination is read and checked as an epic before any PUT. This replaces each child's current parent. " + membershipHelp,
		Example: "$ jira epic add KEY-1 KEY-2 KEY-100 --no-input\n\n# Preview the exact assignment\n$ jira epic add KEY-1:3 KEY-100 --dry-run --output=json",
		Args:    command.ArgSpec{Min: 2, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}, {Name: "EPIC_KEY"}}},
		Flags:   o.flags, Validate: o.validate, Human: renderMembership, Warnings: membershipWarnings,
		RetainOnError: func(out envelope.EpicMembershipOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.EpicMembershipOutput, error) {
			parent, err := command.ParseIssueKey(inv.Args[len(inv.Args)-1])
			if err != nil {
				return envelope.EpicMembershipOutput{}, err
			}
			return runMembership(ctx, deps, inv, inv.Args[:len(inv.Args)-1], parent, o)
		},
	})
}

func newRemove(deps Deps) *cobra.Command {
	o := membershipOptions{}
	return command.MustNew(command.Spec[envelope.EpicMembershipOutput]{
		Op: "epic.remove", WritesJira: true, Identity: true, Keyed: true,
		Use: "remove ISSUE_KEY...", Short: "Detach explicit issues from their epics",
		Long: "Read each child's current parent and confirm it is an epic before clearing fields.parent. " +
			"Subtasks and non-epic parents are refused. An already unparented issue succeeds without a PUT. " +
			"Local previews do not establish the current parent. Parent checks and writes are separate requests; Jira provides no atomic compare-and-set here. " + membershipHelp,
		Example: "$ jira epic remove KEY-1 KEY-2 --no-input\n\n# Preview removals without contacting Jira\n$ jira epic remove KEY-1:3 --dry-run --output=json",
		Args:    command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
		Flags:   o.flags, Validate: o.validate, Human: renderMembership, Warnings: membershipWarnings,
		RetainOnError: func(out envelope.EpicMembershipOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.EpicMembershipOutput, error) {
			return runMembership(ctx, deps, inv, inv.Args, "", o)
		},
	})
}

func runMembership(ctx context.Context, deps Deps, inv command.Invocation, inputs []string, parent string, o membershipOptions) (envelope.EpicMembershipOutput, error) {
	out := envelope.EpicMembershipOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Epic: parent, Notify: o.mut.Notify}
	keys, err := command.ParseIssueKeys(inputs)
	if err != nil {
		return out, err
	}
	for _, key := range keys {
		if key == parent {
			return out, relationError("an epic cannot be its own child")
		}
	}
	var operand *jira.KeyRef
	if parent != "" {
		operand = &jira.KeyRef{Key: parent}
	}
	var request jira.IssueWriteRequest
	if err := request.SetField("parent", operand); err != nil {
		return out, err
	}
	out.Payload, err = jira.MarshalNoEscape(request)
	if err != nil {
		return out, err
	}
	if inv.DryRun && !o.mut.ValidateRemote {
		out.Results = xslices.Map(keys, func(key string) envelope.KeyedResult[envelope.EpicMembershipData] {
			return envelope.KeyedResult[envelope.EpicMembershipData]{Issue: envelope.IssueRef{Key: key}, OK: true, Data: &envelope.EpicMembershipData{}}
		})
		return out, nil
	}
	client, err := deps.JiraClient()
	if err != nil {
		return out, err
	}
	var outcomes []membershipOutcome
	_, err = command.Spin(ctx, "Updating epic membership", func(ctx context.Context) (struct{}, error) {
		if parent != "" {
			if err := requireEpic(ctx, client, parent); err != nil {
				return struct{}{}, err
			}
		}
		outcomes = make([]membershipOutcome, len(keys))
		xsync.Parallel(o.keyed.Parallelism, len(keys), func(i int) {
			outcomes[i] = membershipOne(ctx, client, keys[i], parent, request, inv.DryRun, o.mut)
		})
		return struct{}{}, nil
	})
	// A destination preflight failure has no per-child outcomes to retain.
	if len(keys) > 0 && outcomes == nil {
		return out, err
	}
	out.Results = xslices.Map(outcomes, func(o membershipOutcome) envelope.KeyedResult[envelope.EpicMembershipData] { return o.result })
	if err != nil {
		return out, err
	}
	return out, command.KeyedFailure(out.Results, xslices.Map(outcomes, func(o membershipOutcome) error { return o.err }))
}

type membershipOutcome struct {
	result envelope.KeyedResult[envelope.EpicMembershipData]
	err    error
}

func membershipOne(ctx context.Context, client core.JiraClient, key, parent string, request jira.IssueWriteRequest, dryRun bool, mut command.MutationFlags) membershipOutcome {
	data := &envelope.EpicMembershipData{RemoteValidated: true}
	if err := ctx.Err(); err != nil {
		return membershipFailure(key, err)
	}
	if parent == "" {
		read, err := client.GetIssue(ctx, key, []string{"parent", "issuetype"})
		if err != nil {
			return membershipFailure(key, err)
		}
		observed, err := parentFromIssue(read)
		if err != nil {
			return membershipFailure(key, err)
		}
		typ := read.Issue.Fields.Value.Issuetype
		if !typ.Set || typ.Null || !typ.Value.Subtask.Set || typ.Value.Subtask.Null || typ.Value.Subtask.Value {
			return membershipFailure(key, relationError("epic remove requires an explicitly non-subtask issue type"))
		}
		data.Parent = observed
		if observed.Key == "" {
			data.AlreadyUnparented = true
			return membershipOutcome{result: envelope.KeyedResult[envelope.EpicMembershipData]{Issue: envelope.IssueRef{Key: key}, OK: true, Data: data}}
		}
		if err := requireEpic(ctx, client, observed.Key); err != nil {
			return membershipFailure(key, err)
		}
	}
	if !dryRun {
		if err := ctx.Err(); err != nil {
			return membershipFailure(key, err)
		}
		if _, err := client.EditIssue(ctx, key, request, mut.Notify); err != nil {
			return membershipFailure(key, err)
		}
		data.Applied = true
		// The preflight read is not a post-write observation.
		data.Parent = nil
		if mut.Verify {
			// Jira can omit fields entirely when the only requested field is
			// an unset parent. Summary supplies a populated companion field.
			read, err := client.GetIssue(ctx, key, []string{"parent", "summary"})
			if err == nil {
				data.Parent, err = parentFromIssue(read)
			}
			if err == nil && data.Parent.Key != parent {
				err = relationError("acknowledged parent edit does not match the immediate readback; inspect before retrying")
			}
			if err != nil {
				data.VerifyFailed = true
				data.VerificationError = membershipKeyError(err)
			}
		}
	}
	return membershipOutcome{result: envelope.KeyedResult[envelope.EpicMembershipData]{Issue: envelope.IssueRef{Key: key}, OK: true, Data: data}}
}

func relationError(message string) error {
	return cli.NewInputError(cli.CodeArgValueInvalid, message)
}

// Hierarchy level is authoritative when Jira supplies it. Older responses can
// identify the standard Epic by name only when explicitly non-subtask.
func requireEpic(ctx context.Context, client core.JiraClient, key string) error {
	read, err := client.GetIssue(ctx, key, []string{"issuetype"})
	if err != nil {
		return err
	}
	if read.Issue == nil || !read.Issue.Fields.Set || read.Issue.Fields.Null {
		return relationError("cannot establish the epic type from the issue response")
	}
	typ := read.Issue.Fields.Value.Issuetype
	if !typ.Set || typ.Null || !typ.Value.Subtask.Set || typ.Value.Subtask.Null || typ.Value.Subtask.Value {
		return relationError("parent must be an explicitly non-subtask epic")
	}
	level := typ.Value.HierarchyLevel
	if level.Set && !level.Null {
		if level.Value == 1 {
			return nil
		}
		return relationError("parent must have epic hierarchy level 1")
	}
	if typ.Value.Name.Set && !typ.Value.Name.Null && typ.Value.Name.Value == "Epic" {
		return nil
	}
	return relationError("cannot establish that the parent is an epic; inspect its issue type")
}

func parentFromIssue(read core.IssueRead) (*envelope.EpicParentRead, error) {
	if read.Issue == nil || !read.Issue.Fields.Set || read.Issue.Fields.Null {
		return nil, relationError("parent read lacks issue fields")
	}
	raw, exists := read.Issue.Fields.Value.Extra["parent"]
	if !exists {
		return &envelope.EpicParentRead{State: "absent"}, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return &envelope.EpicParentRead{State: "null"}, nil
	}
	var ref jira.KeyRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil, relationError("parent read is not a null or keyed object")
	}
	key, err := command.ParseIssueKey(ref.Key)
	if err != nil {
		return nil, relationError("parent read lacks a valid issue key")
	}
	return &envelope.EpicParentRead{State: "value", Key: key}, nil
}

func membershipKeyError(err error) *envelope.KeyError {
	class := cli.MapError(err)
	return &envelope.KeyError{Code: class.Code, Message: class.Message, Hint: class.Hint}
}

func membershipFailure(key string, err error) membershipOutcome {
	return membershipOutcome{result: envelope.KeyedResult[envelope.EpicMembershipData]{Issue: envelope.IssueRef{Key: key}, Err: membershipKeyError(err)}, err: err}
}

func membershipWarnings(out envelope.EpicMembershipOutput) []envelope.Warning {
	for _, row := range out.Results {
		if row.Data != nil && row.Data.VerifyFailed {
			return []envelope.Warning{{Type: "epic_verification_failed", Message: "An acknowledged parent write was not confirmed. Inspect per-issue verification_error before retrying."}}
		}
	}
	return nil
}

func renderMembership(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.EpicMembershipOutput)
	if !ok {
		return fmt.Errorf("epic membership: expected EpicMembershipOutput, got %T", env.Data)
	}
	for _, row := range out.Results {
		key := cli.Entity(row.Issue.Key)
		switch {
		case !row.OK:
			v.Line(fmt.Sprintf("%s: %s", key, cli.Text(row.Err.Message)))
		case out.DryRun && out.Epic != "":
			v.Line(fmt.Sprintf("Would assign %s to epic %s.", key, cli.Entity(out.Epic)))
		case out.DryRun:
			v.Line(fmt.Sprintf("Would remove %s from its epic if parent checks pass.", key))
		case row.Data.AlreadyUnparented:
			v.Line(fmt.Sprintf("%s already has no parent; no write was needed.", key))
		case out.Epic != "":
			v.Line(fmt.Sprintf("Assigned %s to epic %s.", key, cli.Entity(out.Epic)))
		default:
			v.Line(fmt.Sprintf("Removed %s from its epic.", key))
		}
		if row.Data != nil && row.Data.VerifyFailed {
			v.Hint("Parent write acknowledged; readback failed or differed. Inspect before retrying.")
		}
	}
	return nil
}
