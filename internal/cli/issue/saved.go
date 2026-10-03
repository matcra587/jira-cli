package issue

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const flagSaved = "saved"

// readSavedQuery resolves preferences and local files without a credential
// store. Its descriptive project metadata never changes the authored query.
func readSavedQuery(ctx context.Context, name string) (*envelope.SavedQueryView, error) {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return nil, err
	}
	queries, err := config.LoadQueries(ctx, resolved.Config.QueriesPath)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		e := cli.NewInputError(cli.CodeSavedQueryInvalid, err.Error())
		e.Field = flagSaved
		return nil, e
	}
	query, found := queries[name]
	if !found {
		e := cli.NewInputError(cli.CodeSavedQueryUnknown, fmt.Sprintf("saved query %q was not found in queries_path", name))
		e.Field = flagSaved
		e.Suggestions = slices.Sorted(maps.Keys(queries))
		return nil, e
	}
	return &envelope.SavedQueryView{Key: name, Name: query.Name, Description: query.Description, Project: query.Project, JQL: query.JQL}, nil
}
