package issue

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// ScreenDescription prepares native anchors through the bound read owner.
func ScreenDescription(ctx context.Context, client core.JiraClient, key string) (*core.IssueDescriptionSession, error) {
	reader, ok := client.(core.IssueDescriptionClient)
	if !ok {
		return nil, core.ErrIssueDescriptionUnavailable
	}
	session, _, err := core.PrepareIssueDescription(ctx, reader, key)
	return session, err
}

// ScreenReviewDescription validates local anchors before a bound merge read.
// The result is a candidate for review, not a write or server precondition.
func ScreenReviewDescription(ctx context.Context, client core.JiraClient, session *core.IssueDescriptionSession, markdown string) (core.IssueDescriptionMerge, error) {
	reader, ok := client.(core.IssueDescriptionClient)
	if !ok {
		return core.IssueDescriptionMerge{}, core.ErrIssueDescriptionUnavailable
	}
	return session.Merge(ctx, reader, markdown)
}

// ScreenReplaceDescription rechecks the reviewed candidate before one explicit
// unconditional write. The final read is not CAS: a later writer can still race.
// The returned key is the last native observation, not an independent readback.
func ScreenReplaceDescription(ctx context.Context, client core.JiraClient, session *core.IssueDescriptionSession, markdown string, review core.IssueDescriptionMerge) (core.IssueActionReceipt, string, error) {
	var receipt core.IssueActionReceipt
	if err := ctx.Err(); err != nil {
		return receipt, "", err
	}
	if _, err := session.Apply(markdown); err != nil {
		return receipt, "", err
	}
	reader, ok := client.(core.IssueDescriptionClient)
	if !ok {
		return receipt, "", core.ErrIssueDescriptionUnavailable
	}
	if err := session.CheckScope(reader); err != nil {
		return receipt, "", err
	}
	snapshot := session.Snapshot()
	if snapshot.IssueID != review.Current.IssueID || snapshot.Site != review.Current.Site || snapshot.Account != review.Current.Account || snapshot.APIBaseURL != review.Current.APIBaseURL {
		return receipt, "", core.ErrIssueDescriptionScopeChanged
	}
	req := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"description": review.Description}}
	if _, err := editFieldPreflight(ctx, client, snapshot.IssueID, req); err != nil {
		return receipt, "", err
	}
	current, err := session.Merge(ctx, reader, markdown)
	if err != nil {
		return receipt, "", err
	}
	if !bytes.Equal(current.Description, review.Description) || !bytes.Equal(current.Current.Description, review.Current.Description) {
		return receipt, current.Current.IssueKey, &adf.EditError{Kind: adf.EditMergeConflict, Reason: "native description changed since review; prepare a new review"}
	}
	if !current.Changed {
		return receipt, current.Current.IssueKey, nil
	}
	if err := ctx.Err(); err != nil {
		return receipt, "", err
	}
	req.Fields["description"] = bytes.Clone(current.Description)
	outcome := editOne(ctx, client, snapshot.IssueID, req, false, true)
	return core.IssueActionReceipt{Attempted: true, Applied: outcome.result.OK}, current.Current.IssueKey, outcome.err
}
