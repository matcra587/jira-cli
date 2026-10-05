package envelope

import xslices "github.com/gechr/x/slices"

// RecentIssueKeys exposes actual typed identities without inspecting arbitrary
// fields or serializing outputs. Failed and queued keyed rows still identify
// the requested issues; the shared command path excludes previews.
func (r KeyedResults[T]) RecentIssueKeys() []string {
	return xslices.Map(r.Results, func(row KeyedResult[T]) string { return row.Issue.Key })
}

// HasJiraResponse retains factual native work independently of the one error
// selected for a failed batch. Public taxonomy codes are not observation facts.
func (r KeyedResults[T]) HasJiraResponse() bool {
	for _, row := range r.Results {
		if row.Err != nil && row.Err.JiraResponse {
			return true
		}
	}
	return false
}

func (out IssueViewOutput) RecentIssueKeys() []string { return []string{out.Issue.Key} }

func (out IssueListOutput) RecentIssueKeys() []string {
	return xslices.Map(out.Issues, func(issue IssueView) string { return issue.Key })
}

func (out IssueCreateOutput) RecentIssueKeys() []string {
	if out.Issue == nil {
		return nil
	}
	return []string{out.Issue.Key}
}

func (out IssueCloneOutput) RecentIssueKeys() []string {
	keys := out.KeyedResults.RecentIssueKeys()
	for _, row := range out.Results {
		if row.Data != nil && row.Data.Clone != nil {
			keys = append(keys, row.Data.Clone.Key)
		}
	}
	return keys
}

func (out IssueRankOutput) RecentIssueKeys() []string {
	return append(out.KeyedResults.RecentIssueKeys(), out.Anchor)
}
