package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// IssueViewOutput is the `issue view` operation's payload: one issue,
// under the shared identity slot every issue-scoped envelope carries.
//
// data.issue is an IssueRef object, never a bare key, so `.data.issue.key`
// keeps its type across every issue command. The fields hang off it as a
// registered passthrough — the curated wire model plus its Extra map, so
// a tenant's custom fields survive verbatim rather than being dropped for
// lacking a typed field. That gap is the leading Jira CLI's most-requested
// missing feature; here it costs nothing, because the wire type already
// captures the unmodelled remainder.
type IssueViewOutput struct {
	Issue IssueView `json:"issue"`
	// WebLink carries `--web`'s own resolved browse URL at data.web_url —
	// see its own doc comment (envelope.go) for why this rides an
	// embed shared with issue.list and issue.comment.list rather than a
	// hand-declared field here. `--web` skips the GetIssue fetch
	// entirely (internal/cli/issue/view.go's runViewWeb), so Issue
	// carries only the key the caller passed — no id/self, no fields —
	// whenever WebURL is set.
	WebLink
}

// IssueView is the identity object with its fields attached: key/id/self
// from IssueRef, then the issue's fields at data.issue.fields.
type IssueView struct {
	IssueRef
	// Fields is the passthrough boundary. The curated members
	// (summary, status, assignee, …) are typed; everything the tenant
	// adds rides IssueFields.Extra and round-trips unchanged.
	Fields jira.IssueFields `json:"fields"`
	// Details retains Jira's native metadata and expansion members, including
	// explicit nulls and unknown members. Identity and fields stay in their
	// existing slots; this native passthrough contains neither a second copy
	// nor a projection that would discard future expansion properties.
	Details jira.IssueBean `json:"details,omitzero"`
}

// IssueViewFromBean projects identity and fields while preserving all other
// native members. An entirely absent metadata record omits details.
func IssueViewFromBean(bean jira.IssueBean) IssueView {
	out := IssueView{
		IssueRef: IssueRef{ID: bean.ID.Value, Key: bean.Key.Value, Self: bean.Self.Value},
		Fields:   bean.Fields.Value,
	}
	var absentID jira.Nullable[string]
	var absentFields jira.Nullable[jira.IssueFields]
	bean.ID, bean.Key, bean.Self = absentID, absentID, absentID
	bean.Fields = absentFields
	if len(bean.Extra) == 0 {
		bean.Extra = nil
	}
	out.Details = bean
	return out
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.view", IssueViewOutput{}, map[string]any{
		"description": "One issue: its identity and its fields.",
		"properties": map[string]any{
			"issue": map[string]any{
				"description": "The issue's identity, with its fields at .issue.fields.",
			},
			"web_url": map[string]any{
				"description": "The issue's browse URL. Present only when --web was given; --web skips the fetch, so .issue carries only the key.",
			},
		},
	})
}
