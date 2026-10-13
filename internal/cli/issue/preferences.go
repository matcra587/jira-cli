package issue

import (
	"context"
	"errors"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// issuePreferences is local-only and checks cancellation around synchronous
// public settings resolution. No store or client is constructed by this seam.
func issuePreferences(ctx context.Context, deps Deps) (core.IssuePreferences, error) {
	if err := ctx.Err(); err != nil {
		return core.IssuePreferences{}, err
	}
	if deps.Preferences == nil {
		return core.IssuePreferences{}, nil
	}
	preferences, err := deps.Preferences()
	if contextErr := ctx.Err(); contextErr != nil {
		return core.IssuePreferences{}, contextErr
	}
	return preferences, err
}

func createInputContext(ctx context.Context, deps Deps, in *issueWriteFlags) (string, string, error) {
	preferences, err := issuePreferences(ctx, deps)
	if err != nil {
		return "", "", err
	}
	project, issueType := in.fields.Project, in.fields.IssueType
	if !in.cmd.Flags().Changed(command.FlagProject) && strings.TrimSpace(project) == "" {
		project = strings.TrimSpace(preferences.DefaultProject)
	}
	if !in.cmd.Flags().Changed(command.FlagIssueType) && strings.TrimSpace(issueType) == "" {
		issueType = strings.TrimSpace(preferences.DefaultIssueType)
	}
	for _, fact := range []struct{ flag, value string }{{command.FlagProject, project}, {command.FlagIssueType, issueType}} {
		if strings.TrimSpace(fact.value) == "" {
			input := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+fact.flag+" must name an explicit nonblank create-metadata context or have a configured default with --generate-json")
			input.Field = fact.flag
			return "", "", input
		}
	}
	return project, issueType, nil
}

// defaultListBoard reads only the public account-scoped saved-filter snapshot.
// A missing context must refuse rather than expand into an unscoped search.
func defaultListBoard(ctx context.Context, deps Deps, preferences core.IssuePreferences, input *command.JQLInput) (listBoardResolution, error) {
	if err := ctx.Err(); err != nil {
		return listBoardResolution{}, err
	}
	unavailable := func(reason string) error {
		return cli.NewCodedError(cli.CodeBoardCacheUnavailable, "default_board: "+reason)
	}
	board, written, err := boardscope.ResolveCached(ctx, deps.CacheFor, preferences, strings.TrimSpace(preferences.DefaultBoard), 0)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return listBoardResolution{}, err
	}
	if err != nil {
		return listBoardResolution{}, unavailable(err.Error())
	}
	input.BoardJQL, input.BoardFromDefault = board.JQL, true
	query, err := command.ComposeJQL(*input)
	if err != nil {
		return listBoardResolution{}, err
	}
	if err := ctx.Err(); err != nil {
		return listBoardResolution{}, err
	}
	return listBoardResolution{jql: query, view: &envelope.IssueBoardView{ID: board.ID, Name: board.Name, JQL: board.JQL, Precedence: "default_board", CachedAt: &written}}, nil
}

// bindDefaultBoardClient checks the constructed client's exact public scope
// before search/count/browser entry, then shares that same client across the
// runner. Local as-jql and missing-cache paths never enter this owner.
func bindDefaultBoardClient(ctx context.Context, deps Deps, preferences core.IssuePreferences) (Deps, error) {
	if err := ctx.Err(); err != nil {
		return deps, err
	}
	if deps.JiraClient == nil {
		return deps, cli.NewCodedError(cli.CodeConfigInvalid, "default_board needs a live client with the matching public site/account")
	}
	client, err := deps.JiraClient(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		return deps, contextErr
	}
	if err != nil {
		return deps, err
	}
	account, ok := client.(core.JiraAccountClient)
	if !ok || strings.TrimSpace(account.Account()) == "" || credentials.NormalizeSite(client.BaseURL()) != credentials.NormalizeSite(preferences.BaseURL) || !strings.EqualFold(strings.TrimSpace(account.Account()), strings.TrimSpace(preferences.Account)) {
		return deps, cli.NewCodedError(cli.CodeConfigInvalid, "default_board snapshot does not match the resolved live site/account; align public base_url/email with auth status before retrying")
	}
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return client, nil
	}
	return deps, nil
}
