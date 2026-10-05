package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

var (
	_ core.IssueMetadataClient      = (*Client)(nil)
	_ core.IssueCreateProjectClient = (*Client)(nil)
)

// GetCreateIssueProject resolves the project's actual ID and key for a native
// create skeleton. It uses the same authenticated, bounded GET owner as metadata.
func (c *Client) GetCreateIssueProject(ctx context.Context, project string) (core.IssueCreateProjectRead, error) {
	response, err := c.readIssueMetadata(ctx, []string{"rest", "api", "3", "project", project}, nil)
	if err != nil {
		return core.IssueCreateProjectRead{}, err
	}
	read, err := core.DecodeIssueCreateProject(response.Body)
	read.Retry = response.Retry
	return read, err
}

// GetCreateIssueTypes reads one project-specific issue-type metadata page.
func (c *Client) GetCreateIssueTypes(ctx context.Context, project string, startAt, maxResults int) (core.IssueMetadataRead, error) {
	return c.getCreateMetadata(ctx, []string{"rest", "api", "3", "issue", "createmeta", project, "issuetypes"}, startAt, maxResults, core.DecodeCreateIssueTypes)
}

// GetCreateIssueFields reads one field page for an explicit issue-type ID.
func (c *Client) GetCreateIssueFields(ctx context.Context, project, issueTypeID string, startAt, maxResults int) (core.IssueMetadataRead, error) {
	return c.getCreateMetadata(ctx, []string{"rest", "api", "3", "issue", "createmeta", project, "issuetypes", issueTypeID}, startAt, maxResults, core.DecodeCreateIssueFields)
}

func (c *Client) getCreateMetadata(ctx context.Context, path []string, startAt, maxResults int, decode func([]byte, int, int) (*core.IssueMetadataPage, error)) (core.IssueMetadataRead, error) {
	if startAt < 0 || maxResults < 1 || maxResults > 200 {
		return core.IssueMetadataRead{}, fmt.Errorf("metadata page offset must be nonnegative and size between 1 and 200")
	}
	response, err := c.readIssueMetadata(ctx, path, agilePageQuery(startAt, maxResults))
	if err != nil {
		return core.IssueMetadataRead{}, err
	}
	page, err := decode(response.Body, startAt, maxResults)
	return core.IssueMetadataRead{Page: page, Retry: response.Retry}, err
}

// GetEditIssueFields reads native edit metadata without administrative screen
// or editability overrides. It does not fetch or change the issue's values.
func (c *Client) GetEditIssueFields(ctx context.Context, key string) (core.IssueEditMetadataRead, error) {
	response, err := c.readIssueMetadata(ctx, []string{"rest", "api", "3", "issue", key, "editmeta"}, nil)
	if err != nil {
		return core.IssueEditMetadataRead{}, err
	}
	read, err := core.DecodeEditIssueFields(response.Body)
	read.Retry = response.Retry
	return read, err
}

func (c *Client) readIssueMetadata(ctx context.Context, path []string, query url.Values) (*core.APIResponse, error) {
	if err := validatePathSegments(path); err != nil {
		return nil, err
	}
	// Reuse the raw transport's bounded capture and retry/auth/scope owners.
	// DoAPI takes raw segments and its shared request builder encodes them.
	// Pre-encoding would reject legitimate Unicode or spaces as unsafe input.
	response, err := c.DoAPI(ctx, core.APIRequest{
		Method: http.MethodGet, Endpoint: "/" + strings.Join(path, "/"), Query: query,
		MaxResponseBytes: core.DefaultMaxAPIResponseBytes,
	})
	if err != nil {
		return nil, err
	}
	if response == nil || response.StatusCode != http.StatusOK || response.Truncated {
		return nil, fmt.Errorf("%w: metadata read requires a complete HTTP 200 response", core.ErrIssueMetadataInvalid)
	}
	return response, nil
}
