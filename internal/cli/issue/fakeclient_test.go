package issue

import (
	"context"
	"fmt"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// unusedJiraClient implements every core.JiraClient method by failing.
//
// **Embed it in a test fake and override only the calls that fake's
// command actually makes.** Every method here fails loudly and names
// itself, so a test that reaches an unexpected seam gets a message saying
// which one rather than a nil-pointer panic or a silent zero value.
//
// It exists because the alternative had a running cost this package kept
// paying. core.JiraClient has many methods and each fake spelled out
// every one of them; adding a seam meant editing ten files, and when two
// branches each added one, the merge was textually clean and the test
// build broke in both directions — neither side's fakes had the other's
// stubs. One embed makes a new seam a one-line change here instead.
//
// Every fake in this package now embeds it and declares only the methods
// its own command actually calls. Keep it that way: a fake that spells out
// a seam it never reaches is re-creating the merge hazard above, and a
// stub returning a zero value with a nil error is worse still — it turns
// an unexpected call into a silent success instead of a named failure.
type unusedJiraClient struct{}

func (unusedJiraClient) ParseJQL(context.Context, []string, core.JQLValidationMode) (core.JQLParseRead, error) {
	return core.JQLParseRead{}, unused("ParseJQL")
}

func (unusedJiraClient) GetJQLReference(context.Context) (core.JQLReferenceRead, error) {
	return core.JQLReferenceRead{}, unused("GetJQLReference")
}

func (unusedJiraClient) SubmitIssueMove(context.Context, jira.IssueBulkMovePayload) (core.IssueMoveSubmission, error) {
	return core.IssueMoveSubmission{}, unused("SubmitIssueMove")
}

func (unusedJiraClient) GetBulkOperationProgress(context.Context, string) (core.BulkOperationRead, error) {
	return core.BulkOperationRead{}, unused("GetBulkOperationProgress")
}

func (unusedJiraClient) GetWorklogs(context.Context, string, int, int) (core.WorklogRead, error) {
	return core.WorklogRead{}, unused("GetWorklogs")
}

func (unusedJiraClient) AddWorklog(context.Context, string, jira.AddWorklogRequest, bool) (core.WorklogWrite, error) {
	return core.WorklogWrite{}, unused("AddWorklog")
}

func (unusedJiraClient) DoAPI(context.Context, core.APIRequest) (*core.APIResponse, error) {
	return nil, unused("DoAPI")
}

// BaseURL is the one method with a real answer rather than a failure: a
// site URL is needed to build a cache key or a browse link, both of which
// are local, and a fake that refused it would fail tests that never make a
// request at all.
func (unusedJiraClient) BaseURL() string { return "https://example.atlassian.net" }

func (unusedJiraClient) GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error) {
	return core.IssueRead{}, unused("GetIssue")
}

func (unusedJiraClient) GetComments(context.Context, string, int, int, core.CommentOrder) (core.CommentRead, error) {
	return core.CommentRead{}, unused("GetComments")
}

func (unusedJiraClient) GetWatchers(context.Context, string) (core.WatcherRead, error) {
	return core.WatcherRead{}, unused("GetWatchers")
}

func (unusedJiraClient) CurrentUser(context.Context) (core.UserRead, error) {
	return core.UserRead{}, unused("CurrentUser")
}

func (unusedJiraClient) GetUser(context.Context, string) (core.UserRead, error) {
	return core.UserRead{}, unused("GetUser")
}

func (unusedJiraClient) SearchUsers(context.Context, string, int, int) (core.UserSearchRead, error) {
	return core.UserSearchRead{}, unused("SearchUsers")
}

func (unusedJiraClient) SearchIssues(context.Context, string, []string, string, int, ...string) (core.IssueSearchRead, error) {
	return core.IssueSearchRead{}, unused("SearchIssues")
}

func (unusedJiraClient) CountIssues(context.Context, string) (core.IssueCountRead, error) {
	return core.IssueCountRead{}, unused("CountIssues")
}

func (unusedJiraClient) GetFilter(context.Context, string) (core.FilterRead, error) {
	return core.FilterRead{}, unused("GetFilter")
}

func (unusedJiraClient) GetBoards(context.Context, string, int, int) (core.BoardRead, error) {
	return core.BoardRead{}, unused("GetBoards")
}

func (unusedJiraClient) GetBoardProjects(context.Context, int64, int, int) (core.BoardProjectRead, error) {
	return core.BoardProjectRead{}, unused("GetBoardProjects")
}

func (unusedJiraClient) GetBoardConfiguration(context.Context, int64) (core.BoardConfigurationRead, error) {
	return core.BoardConfigurationRead{}, unused("GetBoardConfiguration")
}

func (unusedJiraClient) AddComment(context.Context, string, jira.AddCommentRequest) (core.CommentWrite, error) {
	return core.CommentWrite{}, unused("AddComment")
}

func (unusedJiraClient) UpdateComment(context.Context, string, string, jira.UpdateCommentRequest, bool) (core.CommentWrite, error) {
	return core.CommentWrite{}, unused("UpdateComment")
}

func (unusedJiraClient) DeleteComment(context.Context, string, string) (core.CommentDeleteWrite, error) {
	return core.CommentDeleteWrite{}, unused("DeleteComment")
}

func (unusedJiraClient) AddWatcher(context.Context, string, string) (core.WatcherWrite, error) {
	return core.WatcherWrite{}, unused("AddWatcher")
}

func (unusedJiraClient) RemoveWatcher(context.Context, string, string) (core.WatcherWrite, error) {
	return core.WatcherWrite{}, unused("RemoveWatcher")
}

func (unusedJiraClient) GetIssueLinkTypes(context.Context) (core.LinkTypeRead, error) {
	return core.LinkTypeRead{}, unused("GetIssueLinkTypes")
}

func (unusedJiraClient) LinkIssues(context.Context, jira.LinkIssueRequest) (core.LinkWrite, error) {
	return core.LinkWrite{}, unused("LinkIssues")
}

func (unusedJiraClient) DeleteIssueLink(context.Context, string) (core.LinkDeleteWrite, error) {
	return core.LinkDeleteWrite{}, unused("DeleteIssueLink")
}

func (unusedJiraClient) GetRemoteLinks(context.Context, string) (core.RemoteLinkRead, error) {
	return core.RemoteLinkRead{}, unused("GetRemoteLinks")
}

func (unusedJiraClient) AddRemoteLink(context.Context, string, jira.RemoteLinkRequest) (core.RemoteLinkWrite, error) {
	return core.RemoteLinkWrite{}, unused("AddRemoteLink")
}

func (unusedJiraClient) DeleteRemoteLink(context.Context, string, string) (core.RemoteLinkDeleteWrite, error) {
	return core.RemoteLinkDeleteWrite{}, unused("DeleteRemoteLink")
}

func (unusedJiraClient) GetTransitions(context.Context, string) (core.TransitionRead, error) {
	return core.TransitionRead{}, unused("GetTransitions")
}

func (unusedJiraClient) DoTransition(context.Context, string, jira.TransitionRequest) (core.TransitionWrite, error) {
	return core.TransitionWrite{}, unused("DoTransition")
}

func (unusedJiraClient) CreateIssue(context.Context, jira.IssueWriteRequest) (core.IssueCreateWrite, error) {
	return core.IssueCreateWrite{}, unused("CreateIssue")
}

func (unusedJiraClient) EditIssue(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error) {
	return core.IssueEditWrite{}, unused("EditIssue")
}

// unused names the seam a test reached that its fake never meant to
// serve. The method name is in the message because the alternative — a
// bare "not used" — leaves whoever hits it grepping for which of
// twenty-one calls fired.
func unused(method string) error {
	return fmt.Errorf("fake: %s was called, but this fake's command is not supposed to reach it", method)
}

// The attachment, delete and rank half of core.JiraClient, added when
// those seams landed. Every embedder inherits them, which is the whole
// point of this base: before it existed, a new seam meant editing ten
// hand-written fakes, and two branches each adding one broke the test
// build in both directions after a textually clean merge.
func (unusedJiraClient) AddAttachment(context.Context, string, []string) (core.AttachmentWrite, error) {
	return core.AttachmentWrite{}, unused("AddAttachment")
}

func (unusedJiraClient) GetAttachment(context.Context, string) (core.AttachmentRead, error) {
	return core.AttachmentRead{}, unused("GetAttachment")
}

func (unusedJiraClient) GetAttachmentContent(context.Context, string) (core.AttachmentContent, error) {
	return core.AttachmentContent{}, unused("GetAttachmentContent")
}

func (unusedJiraClient) DeleteAttachment(context.Context, string) (core.AttachmentDeleteWrite, error) {
	return core.AttachmentDeleteWrite{}, unused("DeleteAttachment")
}

func (unusedJiraClient) DeleteIssue(context.Context, string, bool) (core.IssueDeleteWrite, error) {
	return core.IssueDeleteWrite{}, unused("DeleteIssue")
}

func (unusedJiraClient) RankIssues(context.Context, jira.RankRequest) (core.RankWrite, error) {
	return core.RankWrite{}, unused("RankIssues")
}
