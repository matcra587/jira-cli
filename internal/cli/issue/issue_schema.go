package issue

import (
	"context"
	"errors"

	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/pipeline"
)

// screenSchemaLookup is the narrow dependency a create / clone command
// needs to validate fields against the Jira create screen. It is one
// method — not the whole ProjectService — so the command depends only
// on createmeta retrieval, not on project listing or any broader
// runtime.
type screenSchemaLookup interface {
	GetFieldSchemaForProfile(ctx context.Context, profile, projectKey, issueType string) (*jira.ProjectFieldSchema, *jira.Response, error)
}

// editSchemaLookup is the narrow dependency an edit / move command needs
// to validate fields against the Jira edit screen for a specific issue.
type editSchemaLookup interface {
	GetEditSchemaForProfile(ctx context.Context, profile, issueKey string) (*jira.ProjectFieldSchema, *jira.Response, error)
}

// newEditScreenSchemaFetcher builds a pipeline.SchemaFetcher that
// resolves the edit screen for one issue via editmeta. edit and move
// validate against the edit screen because field configuration depends
// on the issue's current status, project and type — not just its type.
func newEditScreenSchemaFetcher(ctx context.Context, lookup editSchemaLookup, profile, issueKey string) pipeline.SchemaFetcher {
	return func() (pipeline.ScreenSchema, error) {
		if issueKey == "" {
			return pipeline.ScreenSchema{}, pipeline.ErrSchemaUnknown
		}
		schema, _, err := lookup.GetEditSchemaForProfile(ctx, profile, issueKey)
		if err != nil {
			return pipeline.ScreenSchema{}, classifySchemaError(err)
		}
		if schema == nil || len(schema.Fields) == 0 {
			return pipeline.ScreenSchema{}, pipeline.ErrSchemaUnknown
		}
		return screenSchemaFromFieldSchema(schema), nil
	}
}

// classifySchemaError maps a jira-package lookup error onto the
// pipeline's two schema-miss sentinels. A 404 (unknown project / issue
// type) is reported as ErrSchemaNotFound — a definite user error, fatal
// in every mode. Any other failure (transport error, timeout,
// missing-permission) is reported as the transient ErrSchemaUnknown so
// the pipeline's refresh-once + strict/best-effort policy governs the
// outcome. The underlying error is preserved for the message.
func classifySchemaError(err error) error {
	// A bad --type value is a definite user error — fatal in every mode like a
	// 404, but it stays a validation failure (exit 3), reclassified downstream
	// by MapError. It carries no HTTP status, so match it explicitly.
	if _, ok := errors.AsType[*jira.IssueTypeUnknownError](err); ok {
		return errors.Join(pipeline.ErrSchemaNotFound, err)
	}
	var apiErr *jira.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == 404 || apiErr.Type == jira.ErrorTypeNotFound) {
		return errors.Join(pipeline.ErrSchemaNotFound, err)
	}
	return errors.Join(pipeline.ErrSchemaUnknown, err)
}

// newScreenSchemaFetcher builds a pipeline.SchemaFetcher that resolves
// the create/edit screen schema for one project + issue type under one
// profile. The returned fetcher is what RunMutation calls at stage 3.
//
// projectKey and issueType must both be non-empty: without them Jira's
// createmeta endpoint cannot identify a screen. A fetcher built with
// either missing returns ErrSchemaUnknown so the pipeline applies its
// strict-abort / best-effort-fallback policy uniformly.
//
// Custom-field ids resolved here are instance-specific; the fetcher is
// scoped to one profile and is never shared across Jira sites.
func newScreenSchemaFetcher(ctx context.Context, lookup screenSchemaLookup, profile, projectKey, issueType string) pipeline.SchemaFetcher {
	return func() (pipeline.ScreenSchema, error) {
		if xstrings.AnyEmpty(projectKey, issueType) {
			return pipeline.ScreenSchema{}, pipeline.ErrSchemaUnknown
		}
		schema, _, err := lookup.GetFieldSchemaForProfile(ctx, profile, projectKey, issueType)
		if err != nil {
			// A 404 is reported as ErrSchemaNotFound (fatal in every
			// mode); any other failure is the transient ErrSchemaUnknown
			// so the pipeline's refresh-once + strict/best-effort policy
			// governs the outcome instead of leaking a raw HTTP error.
			return pipeline.ScreenSchema{}, classifySchemaError(err)
		}
		if schema == nil || len(schema.Fields) == 0 {
			return pipeline.ScreenSchema{}, pipeline.ErrSchemaUnknown
		}
		return screenSchemaFromFieldSchema(schema), nil
	}
}

// screenSchemaFromFieldSchema converts the jira-package field schema
// into the pipeline's ScreenSchema: a field-id whitelist plus the
// schema.custom token map the stage-4 encoder branches on.
func screenSchemaFromFieldSchema(schema *jira.ProjectFieldSchema) pipeline.ScreenSchema {
	out := pipeline.ScreenSchema{
		Project:     schema.ProjectKey,
		IssueType:   schema.IssueType,
		ValidFields: make(map[string]bool, len(schema.Fields)),
	}
	var types map[string]string
	for _, f := range schema.Fields {
		if f.ID == "" {
			continue
		}
		out.ValidFields[f.ID] = true
		if f.Custom != "" {
			if types == nil {
				types = make(map[string]string)
			}
			types[f.ID] = f.Custom
		}
	}
	out.FieldTypes = types
	return out
}
