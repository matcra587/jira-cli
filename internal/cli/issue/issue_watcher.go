package issue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/x/ptr"
	xslices "github.com/gechr/x/slices"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/issuekey"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/spf13/cobra"
)

// issueKeyArg is the standard annotation map every new issue-key
// positional carries — wires future issuekey predictor without per-call
// boilerplate.
var issueKeyArg = map[string]string{"clib": "dynamic-args='issuekey'"}

// WatcherCommands is the public wiring slice the root command-tree
// dispatcher in commands.go consumes — keeps the registration surface
// explicit so each new sub-command lands in one place. Exposing it
// avoids "unused symbol" lint flags while incremental delivery is in
// flight.
var WatcherCommands = []func() *cobra.Command{
	issueWatcherCommand,
	issueWatchCommand,
	issueUnwatchCommand,
}

// issueWatcherCommand groups `jira issue watchers list/add/remove`.
// The watch / unwatch shortcuts are registered separately at the
// `issue` group level via WatcherCommands.
func issueWatcherCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watchers",
		Short: "Manage issue watchers",
		Long: "List, add, and remove Jira issue watchers. Use this group when you need to " +
			"inspect or change who receives watcher notifications for an issue.\n\n" +
			"Add and remove accept `me`, `accountId:<id>`, or an email. Plain `--dry-run` " +
			"is local-only; `--validate-remote` opts into a read-only Jira user lookup.",
		Example: `# List watchers on an issue
$ jira issue watchers list PROJ-123

# Preview adding yourself as a watcher
$ jira issue watchers add PROJ-123 --user me --dry-run

# Remove yourself as a watcher
$ jira issue watchers remove PROJ-123 --user me`,
	}
	cmd.AddCommand(watcherListCommand())
	cmd.AddCommand(watcherAddCommand())
	cmd.AddCommand(watcherRemoveCommand())
	return cmd
}

// issueWatchCommand wires the `jira issue watch KEY` shortcut — equivalent
// to `watchers add --user me`. Same envelope shape so consumers don't
// branch by command name.
func issueWatchCommand() *cobra.Command {
	var dryRun, noReadback, validateRemote bool
	var parallelism int
	cmd := &cobra.Command{
		Use:   "watch KEY...",
		Short: "Watch an issue",
		Long: "Start watching one or more issues as the current user. This is a shortcut for " +
			"`jira issue watchers add --user me`.\n\n" +
			"Plain `--dry-run` is local-only and does not contact Jira. Add " +
			"`--validate-remote` when you want a read-only check that the current user can " +
			"be resolved before the live mutation.",
		Args:        cobra.MinimumNArgs(1),
		Annotations: issueKeyArg,
		Example: `# Start watching an issue
$ jira issue watch PROJ-123

# Preview watching an issue
$ jira issue watch PROJ-123 --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			return runWatcherAddKeys(cmd, keys, parallelism, watcherMutationArgs{
				UserIdent: "me", DryRun: dryRun, NoReadback: noReadback,
				ValidateRemote: validateRemote,
			})
		},
	}
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Local preview only — does not contact Jira")
	cmdutil.AddBoolVar(cmd.Flags(), &noReadback, "no-readback", false, "Skip the post-mutation GET", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddBoolVar(cmd.Flags(), &validateRemote, "validate-remote", false, "Resolve `--user` against Jira (read-only); use with `--dry-run`", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

// issueUnwatchCommand wires `jira issue unwatch KEY` — equivalent to
// `watchers remove --user me`.
func issueUnwatchCommand() *cobra.Command {
	var dryRun, noReadback, validateRemote bool
	var parallelism int
	cmd := &cobra.Command{
		Use:   "unwatch KEY...",
		Short: "Stop watching an issue",
		Long: "Stop watching one or more issues as the current user. This is a shortcut for " +
			"`jira issue watchers remove --user me`.\n\n" +
			"Plain `--dry-run` is local-only and does not contact Jira. Add " +
			"`--validate-remote` when you want a read-only check that the current user can " +
			"be resolved before the live mutation.",
		Args:        cobra.MinimumNArgs(1),
		Annotations: issueKeyArg,
		Example: `# Stop watching an issue
$ jira issue unwatch PROJ-123

# Preview unwatching an issue
$ jira issue unwatch PROJ-123 --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			return runWatcherRemoveKeys(cmd, keys, parallelism, watcherMutationArgs{
				UserIdent: "me", DryRun: dryRun, NoReadback: noReadback,
				ValidateRemote: validateRemote,
			})
		},
	}
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Local preview only — does not contact Jira")
	cmdutil.AddBoolVar(cmd.Flags(), &noReadback, "no-readback", false, "Skip the post-mutation GET", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddBoolVar(cmd.Flags(), &validateRemote, "validate-remote", false, "Resolve `--user` against Jira (read-only); use with `--dry-run`", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

// watcherMutationArgs bundles the inputs to runWatcherAdd / runWatcherRemove
// so the helpers stay at ≤2 visible parameters (cmd + args) — easier to grow
// later (e.g. --visibility on watcher add) without ballooning the signatures.
type watcherMutationArgs struct {
	Key        string
	UserIdent  string
	DryRun     bool
	NoReadback bool
	// ValidateRemote opts a dry-run into resolving --user against Jira's
	// read-only /myself + /user search endpoints. Plain --dry-run is
	// local preview only and never contacts Jira.
	ValidateRemote bool
}

// ----- list -----------------------------------------------------------------

func watcherListCommand() *cobra.Command {
	var parallelism int
	cmd := &cobra.Command{
		Use:   "list KEY...",
		Short: "List watchers on an issue",
		Long: "List watcher state for one or more issues. Use it before changing watchers " +
			"or to confirm whether the active user is watching an issue.\n\n" +
			"Multiple issue keys are fetched with bounded parallelism and return per-key " +
			"results.",
		Args:        cobra.MinimumNArgs(1),
		Annotations: issueKeyArg,
		Example: `# List the watchers on an issue
$ jira issue watchers list PROJ-123

# List watchers on several issues as JSON
$ jira issue watchers list PROJ-123 PROJ-124 --output=json`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("jira base URL is required for issue.watchers.list")
			}
			service := cmdutil.ServicesForClient(client).Watcher()
			if len(keys) == 1 {
				var (
					watchers *jira.WatchersResponse
					resp     *jira.Response
				)
				if err := cmdutil.Spin(cmd, "issue.watchers.list", func(ctx context.Context) error {
					var e error
					watchers, resp, e = service.List(ctx, keys[0])
					return e
				}); err != nil {
					return err
				}
				return cmdutil.WriteEnvelopeWithResponse(cmd, "issue.watchers.list", watcherListEnvelopeData(keys[0], watchers), resp)
			}
			results, err := cmdutil.FanOutKeys(cmd.Context(), keys, parallelism, func(ctx context.Context, key string) (*jira.WatchersResponse, error) {
				watchers, _, err := service.List(ctx, key)
				return watchers, err
			})
			if err != nil {
				return err
			}
			return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.watchers.list", results, func(key string, watchers *jira.WatchersResponse) any {
				return watcherListEnvelopeData(key, watchers)
			})
		},
	}
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

func watcherListEnvelopeData(key string, watchers *jira.WatchersResponse) envelope.IssueWatchersListOutput {
	if watchers == nil {
		return envelope.IssueWatchersListOutput{
			Issue:      cmdutil.IssueRef{Key: key},
			Watchers:   []envelope.WatcherItem{},
			IsWatching: false,
			WatchCount: 0,
		}
	}
	return envelope.IssueWatchersListOutput{
		Issue:      cmdutil.IssueRef{Key: key},
		Watchers:   watcherItems(watchers.Watchers),
		IsWatching: watchers.IsWatching,
		WatchCount: watchers.WatchCount,
	}
}

// ----- add ------------------------------------------------------------------

func watcherAddCommand() *cobra.Command {
	var user string
	var dryRun, noReadback, validateRemote bool
	var parallelism int
	cmd := &cobra.Command{
		Use:   "add KEY...",
		Short: "Add a watcher to an issue",
		Long: "Add a user as a watcher on one or more issues. Use `--user me` for the " +
			"active profile, `accountId:<id>` for an exact Jira account, or an email when " +
			"a live Jira lookup is acceptable.\n\n" +
			"Plain `--dry-run` is local-only and can resolve only `me` or `accountId:<id>` " +
			"from local state. Add `--validate-remote` to make dry-run perform a read-only " +
			"user lookup.",
		Args:        cobra.MinimumNArgs(1),
		Annotations: issueKeyArg,
		Example: `# Add yourself as a watcher
$ jira issue watchers add PROJ-123 --user me

# Add a watcher by account id
$ jira issue watchers add PROJ-123 --user accountId:5b10ac8d82e05b22cc7d4ef5

# Preview adding a watcher without mutating
$ jira issue watchers add PROJ-123 --user me --dry-run`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if user == "" {
				return fmt.Errorf("validation: --user is required (me / accountId:<id> / email)")
			}
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			return runWatcherAddKeys(cmd, keys, parallelism, watcherMutationArgs{
				UserIdent: user, DryRun: dryRun, NoReadback: noReadback,
				ValidateRemote: validateRemote,
			})
		},
	}
	cmdutil.AddStringVar(cmd.Flags(), &user, "user", "", "User identifier (me / accountId:<id> / email)", clib.FlagExtra{Group: "User", Placeholder: "IDENTIFIER"})
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Local preview only — does not contact Jira")
	cmdutil.AddBoolVar(cmd.Flags(), &noReadback, "no-readback", false, "Skip the post-mutation GET", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddBoolVar(cmd.Flags(), &validateRemote, "validate-remote", false, "Resolve `--user` against Jira (read-only); use with `--dry-run`", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

// ----- remove ---------------------------------------------------------------

func watcherRemoveCommand() *cobra.Command {
	var user string
	var dryRun, noReadback, validateRemote bool
	var parallelism int
	cmd := &cobra.Command{
		Use:   "remove KEY...",
		Short: "Remove a watcher from an issue",
		Long: "Remove a user from the watcher list on one or more issues. Use `--user me` " +
			"for the active profile, `accountId:<id>` for an exact Jira account, or an " +
			"email when a live Jira lookup is acceptable.\n\n" +
			"Plain `--dry-run` is local-only and can resolve only `me` or `accountId:<id>` " +
			"from local state. Add `--validate-remote` to make dry-run perform a read-only " +
			"user lookup.",
		Args:        cobra.MinimumNArgs(1),
		Annotations: issueKeyArg,
		Example: `# Remove yourself as a watcher
$ jira issue watchers remove PROJ-123 --user me

# Remove a watcher by account id
$ jira issue watchers remove PROJ-123 --user accountId:5b10ac8d82e05b22cc7d4ef5

# Preview removing a watcher without mutating
$ jira issue watchers remove PROJ-123 --user me --dry-run`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if user == "" {
				return fmt.Errorf("validation: --user is required (me / accountId:<id> / email)")
			}
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			return runWatcherRemoveKeys(cmd, keys, parallelism, watcherMutationArgs{
				UserIdent: user, DryRun: dryRun, NoReadback: noReadback,
				ValidateRemote: validateRemote,
			})
		},
	}
	cmdutil.AddStringVar(cmd.Flags(), &user, "user", "", "User identifier (me / accountId:<id> / email)", clib.FlagExtra{Group: "User", Placeholder: "IDENTIFIER"})
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Local preview only — does not contact Jira")
	cmdutil.AddBoolVar(cmd.Flags(), &noReadback, "no-readback", false, "Skip the post-mutation GET", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddBoolVar(cmd.Flags(), &validateRemote, "validate-remote", false, "Resolve `--user` against Jira (read-only); use with `--dry-run`", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

// ----- shared add/remove drivers -------------------------------------------

func runWatcherAddKeys(cmd *cobra.Command, keys []string, parallelism int, args watcherMutationArgs) error {
	if len(keys) == 1 {
		args.Key = keys[0]
		return runWatcherAdd(cmd, args)
	}
	return runWatcherMutationMany(cmd, "issue.watchers.add", keys, parallelism, args, watcherMutationAdd)
}

func runWatcherRemoveKeys(cmd *cobra.Command, keys []string, parallelism int, args watcherMutationArgs) error {
	if len(keys) == 1 {
		args.Key = keys[0]
		return runWatcherRemove(cmd, args)
	}
	return runWatcherMutationMany(cmd, "issue.watchers.remove", keys, parallelism, args, watcherMutationRemove)
}

type watcherMutationKind int

const (
	watcherMutationAdd watcherMutationKind = iota
	watcherMutationRemove
)

func runWatcherMutationMany(
	cmd *cobra.Command,
	command string,
	keys []string,
	parallelism int,
	args watcherMutationArgs,
	kind watcherMutationKind,
) error {
	if args.DryRun {
		return watcherDryRunPreviewMany(cmd, command, keys, args)
	}
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for %s", command)
	}
	var accountID string
	if err := cmdutil.Spin(cmd, "user.resolve", func(ctx context.Context) error {
		var e error
		accountID, e = cmdutil.ServicesForClient(client).User().ResolveUser(ctx, args.UserIdent)
		return e
	}); err != nil {
		return handleResolveErr(cmd, command, err)
	}
	watcherSvc := cmdutil.ServicesForClient(client).Watcher()
	results, err := cmdutil.FanOutKeys(cmd.Context(), keys, parallelism, func(ctx context.Context, key string) (envelope.IssueWatcherMutationOutput, error) {
		perKey := args
		perKey.Key = key
		switch kind {
		case watcherMutationAdd:
			return watcherAddData(ctx, watcherSvc, accountID, perKey)
		case watcherMutationRemove:
			return watcherRemoveData(ctx, watcherSvc, accountID, perKey)
		default:
			return envelope.IssueWatcherMutationOutput{}, fmt.Errorf("unknown watcher mutation kind")
		}
	})
	if err != nil {
		return err
	}
	return cmdutil.WriteKeyedResultsEnvelope(cmd, command, results, func(_ string, data envelope.IssueWatcherMutationOutput) any {
		return data
	})
}

func watcherDryRunPreviewMany(cmd *cobra.Command, command string, keys []string, args watcherMutationArgs) error {
	accountID := ""
	userResolved := false
	if args.ValidateRemote {
		client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("jira base URL is required for %s --validate-remote", command)
		}
		userSvc := cmdutil.ServicesForClient(client).User()
		if id, ok := accountIDFromIdentifier(args.UserIdent); ok {
			err = cmdutil.Spin(cmd, "user.resolve", func(ctx context.Context) error {
				var e error
				accountID, e = userSvc.ResolveAccountID(ctx, id)
				return e
			})
		} else {
			err = cmdutil.Spin(cmd, "user.resolve", func(ctx context.Context) error {
				var e error
				accountID, e = userSvc.ResolveUser(ctx, args.UserIdent)
				return e
			})
		}
		if err != nil {
			return handleResolveErr(cmd, command, err)
		}
		userResolved = true
	} else if id, ok := localResolveUser(cmd, args.UserIdent); ok {
		accountID = id
		userResolved = true
	}
	results := xslices.Map(keys, func(key string) cmdutil.KeyResult[envelope.IssueWatcherMutationOutput] {
		perKey := args
		perKey.Key = key
		data := watcherMutationData(perKey, accountID, userResolved, true)
		return cmdutil.KeyResult[envelope.IssueWatcherMutationOutput]{Key: key, Value: data}
	})
	return cmdutil.WriteKeyedResultsEnvelope(cmd, command, results, func(_ string, data envelope.IssueWatcherMutationOutput) any {
		return data
	})
}

func runWatcherAdd(cmd *cobra.Command, args watcherMutationArgs) error {
	if args.DryRun {
		return watcherDryRunPreview(cmd, "issue.watchers.add", args)
	}

	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for issue.watchers.add")
	}
	user := cmdutil.ServicesForClient(client).User()

	// Resolve the user first — bailing on a not-found / ambiguous query
	// before the pre-state readback avoids a wasted /watchers GET on the
	// unhappy path.
	var accountID string
	if err := cmdutil.Spin(cmd, "user.resolve", func(ctx context.Context) error {
		var e error
		accountID, e = user.ResolveUser(ctx, args.UserIdent)
		return e
	}); err != nil {
		return handleResolveErr(cmd, "issue.watchers.add", err)
	}

	// Capture pre-state when readback is requested so we can populate
	// `was_already_watching` in the envelope .
	watcherSvc := cmdutil.ServicesForClient(client).Watcher()
	var data envelope.IssueWatcherMutationOutput
	if err := cmdutil.Spin(cmd, "issue.watchers.add", func(ctx context.Context) error {
		var e error
		data, e = watcherAddData(ctx, watcherSvc, accountID, args)
		return e
	}); err != nil {
		return err
	}
	return cmdutil.WriteEnvelope(cmd, "issue.watchers.add", data)
}

func watcherAddData(ctx context.Context, watcherSvc jira.WatcherService, accountID string, args watcherMutationArgs) (envelope.IssueWatcherMutationOutput, error) {
	var preState *jira.WatchersResponse
	if !args.NoReadback {
		var err error
		preState, _, err = watcherSvc.List(ctx, args.Key)
		if err != nil {
			preState = nil
		}
	}

	if _, err := watcherSvc.Add(ctx, args.Key, accountID); err != nil {
		return envelope.IssueWatcherMutationOutput{}, err
	}

	data := watcherMutationData(args, accountID, true, false)
	if args.NoReadback {
		data.AccountID = accountID
		data.Attempted = true
		return data, nil
	}

	post, _, err := watcherSvc.List(ctx, args.Key)
	if err != nil {
		return envelope.IssueWatcherMutationOutput{}, err
	}
	wasAlready := containsAccount(preState, accountID)
	watchers := watcherItems(post.Watchers)
	data.Watchers = &watchers
	data.IsWatching = &post.IsWatching
	data.WatchCount = &post.WatchCount
	data.WasAlreadyWatching = &wasAlready
	return data, nil
}

func runWatcherRemove(cmd *cobra.Command, args watcherMutationArgs) error {
	if args.DryRun {
		return watcherDryRunPreview(cmd, "issue.watchers.remove", args)
	}

	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for issue.watchers.remove")
	}
	user := cmdutil.ServicesForClient(client).User()

	var accountID string
	if err := cmdutil.Spin(cmd, "user.resolve", func(ctx context.Context) error {
		var e error
		accountID, e = user.ResolveUser(ctx, args.UserIdent)
		return e
	}); err != nil {
		return handleResolveErr(cmd, "issue.watchers.remove", err)
	}

	watcherSvc := cmdutil.ServicesForClient(client).Watcher()
	var data envelope.IssueWatcherMutationOutput
	if err := cmdutil.Spin(cmd, "issue.watchers.remove", func(ctx context.Context) error {
		var e error
		data, e = watcherRemoveData(ctx, watcherSvc, accountID, args)
		return e
	}); err != nil {
		return err
	}
	return cmdutil.WriteEnvelope(cmd, "issue.watchers.remove", data)
}

func watcherRemoveData(ctx context.Context, watcherSvc jira.WatcherService, accountID string, args watcherMutationArgs) (envelope.IssueWatcherMutationOutput, error) {
	var preState *jira.WatchersResponse
	if !args.NoReadback {
		var err error
		preState, _, err = watcherSvc.List(ctx, args.Key)
		if err != nil {
			preState = nil
		}
	}

	if _, err := watcherSvc.Remove(ctx, args.Key, accountID); err != nil {
		return envelope.IssueWatcherMutationOutput{}, err
	}

	data := watcherMutationData(args, accountID, true, false)
	if args.NoReadback {
		data.AccountID = accountID
		data.Attempted = true
		return data, nil
	}

	post, _, err := watcherSvc.List(ctx, args.Key)
	if err != nil {
		return envelope.IssueWatcherMutationOutput{}, err
	}
	wasAlready := containsAccount(preState, accountID)
	watchers := watcherItems(post.Watchers)
	data.Watchers = &watchers
	data.IsWatching = &post.IsWatching
	data.WatchCount = &post.WatchCount
	data.WasAlreadyWatching = &wasAlready
	return data, nil
}

// localResolveUser resolves a watcher --user identifier WITHOUT
// contacting Jira. It succeeds only for identifiers that are derivable
// from local state:
//
//   - "accountId:<id>" → the id, parsed from the prefix.
//   - "me" / "@me"     → the active profile's AccountID, when set.
//
// A bare name or email genuinely requires a /user/search hop, so it
// returns ("", false): the caller must report it unresolved rather than
// fabricate a result or sneak in a live call.
func localResolveUser(cmd *cobra.Command, ident string) (string, bool) {
	v := strings.TrimSpace(ident)
	if id, ok := strings.CutPrefix(v, "accountId:"); ok {
		id = strings.TrimSpace(id)
		return id, id != ""
	}
	switch strings.ToLower(v) {
	case "me", "@me":
		profile, err := cmdutil.ProfileForCommand(cmd)
		if err != nil {
			return "", false
		}
		return profile.AccountID, profile.AccountID != ""
	default:
		return "", false
	}
}

// watcherDryRunPreview renders the local preview for watch / unwatch.
//
// Plain --dry-run is local-only: it contacts Jira for nothing. An
// identifier that is locally derivable (accountId:<id>, or "me" when the
// profile carries an AccountID) is resolved here and reported in
// `account_id_resolved`; a bare name or email cannot be resolved without
// a live call, so it is echoed back with `user_resolved:false` and no
// `account_id_resolved`. --validate-remote opts into resolving the user
// against Jira's read-only /myself + /user search endpoints — never a
// watcher POST/DELETE. The `user_resolved` flag keeps the preview honest
// about whether resolution actually ran.
func watcherDryRunPreview(cmd *cobra.Command, command string, args watcherMutationArgs) error {
	data := watcherMutationData(args, "", false, true)
	if !args.ValidateRemote {
		if id, ok := localResolveUser(cmd, args.UserIdent); ok {
			data.AccountIDResolved = id
			data.UserResolved = true
		}
		return cmdutil.WriteEnvelope(cmd, command, data)
	}
	// --validate-remote: resolve via the read-only user service. No
	// mutation is ever issued on this path.
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for %s --validate-remote", command)
	}
	userSvc := cmdutil.ServicesForClient(client).User()
	var accountID string
	rerr := cmdutil.Spin(cmd, "user.resolve", func(ctx context.Context) error {
		var e error
		if id, ok := accountIDFromIdentifier(args.UserIdent); ok {
			accountID, e = userSvc.ResolveAccountID(ctx, id)
		} else {
			accountID, e = userSvc.ResolveUser(ctx, args.UserIdent)
		}
		return e
	})
	if rerr != nil {
		return handleResolveErr(cmd, command, rerr)
	}
	data.AccountIDResolved = accountID
	data.UserResolved = true
	return cmdutil.WriteEnvelope(cmd, command, data)
}

func watcherMutationData(
	args watcherMutationArgs,
	accountID string,
	userResolved, dryRun bool,
) envelope.IssueWatcherMutationOutput {
	return envelope.IssueWatcherMutationOutput{
		Issue:             cmdutil.IssueRef{Key: args.Key},
		User:              args.UserIdent,
		UserResolved:      userResolved,
		AccountIDResolved: accountID,
		DryRun:            dryRun,
	}
}

func accountIDFromIdentifier(ident string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(ident), "accountId:")
	if !ok {
		return "", false
	}
	id = strings.TrimSpace(id)
	return id, id != ""
}

// handleResolveErr maps the resolver's (ErrUserNotFound / *AmbiguousUserError /
// transport) error into the right exit-code envelope:
//   - 0 matches  → exit 2 with `errors[].type = not_found`, input echoed
//   - 2+ matches → exit 3 with `errors[].type = validation` and a
//     structured `errors[].candidates: [...]` per envelope-shapes.md.
//     Returns a cmdutil.EnvelopeWrittenError so the central error writer
//     doesn't overwrite the richer envelope we already emitted.
func handleResolveErr(cmd *cobra.Command, command string, err error) error {
	if _, ok := errors.AsType[*jira.AmbiguousUserError](err); ok {
		// Route through the central error-envelope builder so the
		// ambiguity failure carries a stable code, meta.exit_code, and
		// the same shape as every other error envelope. cli.MapError
		// recognizes *jira.AmbiguousUserError and flattens its
		// /user/search candidates into errors[].candidates.
		env := cli.ErrorEnvelope(command, err)
		if len(env.Errors) == 1 {
			env.Errors[0].Hint = "Re-run with --user accountId:<id>."
		}
		// Machine mode: the candidates envelope goes to stdout, the same stream
		// as success, so a consumer parses one stream regardless of outcome.
		ambiguityErr := fmt.Errorf("validation: %w", err)
		if writeErr := cli.WriteEnvelope(cmd.OutOrStdout(), env); writeErr != nil {
			return cli.PreservePrimaryError(ambiguityErr, writeErr)
		}
		return cmdutil.EnvelopeWritten(ambiguityErr)
	}
	if errors.Is(err, jira.ErrUserNotFound) {
		// Typed not_found (exit 2): the live /user/search genuinely found
		// nobody — a missing Jira resource, not bad input. The wrap keeps
		// the sentinel visible to errors.Is.
		return cli.NewNotFoundError("", err)
	}
	return err
}

// containsAccount reports whether `pre` already lists the supplied
// accountID. Pre-state may be nil if the readback GET failed before the
// mutation; treat that as "unknown → false" rather than panicking.
func containsAccount(pre *jira.WatchersResponse, accountID string) bool {
	if pre == nil {
		return false
	}
	for _, u := range pre.Watchers {
		if u != nil && u.AccountID != nil && *u.AccountID == accountID {
			return true
		}
	}
	return false
}

func watcherItems(users []*jira.User) []envelope.WatcherItem {
	out := make([]envelope.WatcherItem, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		item := envelope.WatcherItem{
			AccountID:   ptr.Deref(u.AccountID),
			DisplayName: ptr.Deref(u.DisplayName),
		}
		if u.EmailAddress != nil {
			email := *u.EmailAddress
			item.EmailAddress = &email
		}
		out = append(out, item)
	}
	return out
}
