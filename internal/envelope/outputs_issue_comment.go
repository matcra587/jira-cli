package envelope

import "encoding/json"

// IssueCommentListOutput is the `issue.comment.list` operation's
// payload: one API-paginated page of an issue's comments.
//
// This is the FIRST list command in the CLI, so it also sets the
// pagination-in-data precedent every later list command follows:
// Pagination lives inside data (see pagination.go), never in meta,
// because --output=compact prints bare data and --jq filters exactly
// that document — a consumer of either could not otherwise see it.
//
// Comments are returned as structured typed data plus pagination
// metadata, never a pre-rendered blob, and the page comes straight from
// Jira's own startAt/maxResults offset pagination rather than a
// fetch-everything-then-truncate walk: a thread with thousands of
// comments must not be pulled into memory just to show the first page.
type IssueCommentListOutput struct {
	// Identity is the shared embed, not a hand-declared Issue field: the
	// slot is identical across issue-scoped operations, and declaring it
	// again here is how the two drift. (issue view is deliberately NOT an
	// instance — its Issue is an IssueView, a superset carrying Fields
	// too — so this is the embed's first real use, alongside the
	// MutationState embed the config and auth outputs already share.)
	Identity
	// Comments is this page's comments, in the order `--order` asked for
	// — newest first unless it said otherwise (the server's own orderBy;
	// see core.CommentOrder).
	Comments []CommentView `json:"comments"`
	// Pagination reports which slice of the full thread this page is —
	// the caller's only way to tell a truncated view from the whole
	// thread. Under `--all` it describes the WHOLE walk rather than its
	// last page: start_at is 0, returned counts everything collected,
	// and an offset reader resumes from start_at + returned.
	Pagination OffsetPagination `json:"pagination"`
	// Truncated reports a safety cap or a failed/interrupted read. Valid
	// observed pages survive in the full failure frame. An ordinary successful
	// single page is not truncated; its native pagination describes the window.
	Truncated bool `json:"truncated"`
	// TruncatedReason distinguishes max_pages/max_comments from read_failed
	// and response_invalid. The latter reasons accompany an error, not a
	// successful cap. Empty whenever Truncated is false.
	TruncatedReason string `json:"truncated_reason,omitempty"`
	// WebLink carries `--web`'s own resolved URL at data.web_url — the
	// commented ISSUE's browse URL, since comments live on the issue
	// page (see its own doc comment, envelope.go). `--web` skips the
	// GetComments fetch entirely (internal/cli/issue/comment.go's
	// runCommentListWeb), so Comments stays empty and Pagination at its
	// zero value whenever WebURL is set.
	WebLink
}

// CommentView is one comment, shaped for the CLI's contract: the
// curated identity/authorship/timestamps as typed fields, and Body
// carrying the raw ADF verbatim — the registered passthrough boundary.
// A tenant's own comment content (mentions, custom marks, or any node
// type this CLI's ADF renderer does not know) rides through inside Body
// untouched, exactly like IssueView.Fields.Description.
type CommentView struct {
	// ID is the comment's own id — a comment has no separate self link
	// worth surfacing here, so this stays a plain field rather than a
	// second Ref type.
	ID string `json:"id"`
	// Author is who wrote it.
	Author AuthorRef `json:"author"`
	// Created and Updated are Jira's own RFC 3339 timestamps, verbatim —
	// wire timestamps are exempt from the CLI's own UTC-RFC-3339 rule by
	// design (M5), same as every other wire-sourced time in this CLI.
	Created string `json:"created"`
	Updated string `json:"updated"`
	// Body is the comment's ADF document, byte-for-byte from the wire.
	// internal/adf owns the typed model for RENDERING it (see
	// internal/cli/issue/render.go); this field is the byte-preserving
	// escape hatch --output=json always has, whatever internal/adf does
	// or does not yet know how to draw.
	Body json.RawMessage `json:"body"`
}

// AuthorRef is a comment's author, narrowed to what a caller actually
// needs to identify who wrote it — not the full wire.UserDetails, which
// carries account-management fields (active, accountType, timeZone, …)
// that have nothing to do with authorship.
type AuthorRef struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
}

// IssueCommentAddOutput is the `issue.comment.add` operation's payload:
// the comment that was posted to each named issue.
//
// THE FIRST JIRA MUTATION'S OUTPUT, so its layout is the one the writes
// that follow copy. Three placement decisions carry the reasoning.
//
// **The composed request lives at the top, not per key.** DryRun, Body,
// BodyRoute and Visibility are facts about the RUN — one body, one
// visibility, resolved once and sent to every key — so duplicating them
// into each of N results would be N copies of one fact, and an ADF
// document is not small. This is the same placement dry_run already
// forces: a preview flag cannot be per key, and the request that flag
// previews belongs beside it. What genuinely differs per key is the
// comment Jira created, and that is the only thing in results[].
//
// **The request survives BOTH paths.** A preview and a live write carry
// identical Body, BodyRoute and Visibility, which is what makes
// `--dry-run` worth running: a caller diffs the preview against what
// they expected, then drops the flag and gets the same request sent.
// Publishing the payload only on the preview would make the live
// envelope unable to answer "what did you actually send?".
//
// **The created comment is conditional.** It exists only after Jira
// creates it, so it is absent on a preview — the rule that server-created
// objects stay conditional while request context does not.
type IssueCommentAddOutput struct {
	// MutationState carries dry_run on both paths — true on preview,
	// false on the live write — so a caller never infers from a missing
	// field whether anything happened.
	MutationState
	// SelectorState records a --jql/--filter/--from-file run's own query,
	// absent when the caller named the keys.
	SelectorState
	// Body is the ADF document that was posted, or on a preview the one
	// that WOULD be posted, byte-for-byte as composed. Raw ADF for the
	// same reason CommentView.Body is: internal/adf owns the typed model
	// and this contract preserves bytes.
	Body json.RawMessage `json:"body"`
	// BodyRoute names which of the three input routes produced Body —
	// "adf" for --json-input, "markdown" for --markdown/--markdown-file.
	// Published because the routes are not interchangeable: markdown is
	// a lossy convenience layer and adf is lossless, so a caller
	// reviewing a preview needs to know which one they are looking at.
	BodyRoute string `json:"body_route"`
	// Visibility is the restriction the comment carries, absent when the
	// comment is visible to everyone who can see the issue.
	Visibility *CommentVisibilityView `json:"visibility,omitempty"`
	// Results is one entry per key the caller named, in the order they
	// named them. Total failure and interruption retain all joined rows,
	// including any created identities, alongside the authored request.
	KeyedResults[IssueCommentAddData]
}

// IssueCommentAddData is one key's outcome.
//
// It holds only what is genuinely per key. On a live write that is the
// comment Jira created; on a preview it is empty, and an empty object is
// the honest answer rather than an omission — the key WAS processed, and
// what it produced is nothing yet.
//
// A note on what a preview's `ok: true` means here, because it is the
// one place this envelope could mislead. It means the payload for that
// key composed successfully. It does NOT mean Jira would accept it: a
// dry run is local-only and never contacts the site, so it cannot know
// the issue exists or that the caller may comment on it. A preview that
// claimed otherwise would be inventing a verification it never did.
type IssueCommentAddData struct {
	// Comment is the comment Jira created, absent on a preview.
	//
	// Published unconditionally on the live path, with no --verify or
	// --no-readback flag gating it, because it costs nothing: the POST's
	// own 201 response carries the whole comment. A readback flag exists
	// to let a caller decline an EXTRA round trip, and there is none here
	// to decline. It also carries the one value a caller cannot get any
	// other way without re-listing the thread — the new comment's id,
	// which `comment edit` and `comment delete` both take.
	Comment *CommentView `json:"comment,omitempty"`
}

// IssueCommentEditOutput is the `issue.comment.edit` operation's
// payload: one comment's new body, and the comment as Jira stored it.
//
// THE FIRST MUTATION IN THIS CLI THAT IS NOT KEYED, and the shape
// difference is the thing to read off this type. `comment add` puts its
// per-key outcome at data.results[].data because it addresses N issues;
// this command addresses ONE comment on ONE issue, so the same operation
// object sits straight at `data`. That is exactly the placement rule
// KeyedResults' own doc comment states — being keyed changes WHERE the
// object goes, never what it looks like — seen from the other side for
// the first time.
//
// It is not keyed because a comment id is not meaningful across issues.
// `KEY-1 KEY-2 10042` would ask Jira for comment 10042 on two different
// issues, which is either a 404 or, far worse, a real comment on each
// that has nothing to do with the other. Making this keyed for symmetry
// with its sibling would have invented that invocation.
//
// The top-level/per-key split `comment add` established still applies,
// with N=1: the composed request (Body, BodyRoute, Visibility) survives
// BOTH paths so a preview can be diffed against a live run, and the
// stored comment is conditional because it exists only once Jira has
// stored it.
type IssueCommentEditOutput struct {
	// Identity places the commented issue at data.issue, the same slot
	// every issue-scoped operation uses.
	Identity
	// MutationState carries dry_run on both paths.
	MutationState
	// Notify reports whether the request asked Jira to notify watchers of
	// the edit. Serialized unconditionally, for IssueEditOutput.Notify's
	// reason — though without that field's permission caveat: the spec
	// attaches no permission requirement to notifyUsers on this endpoint.
	Notify bool `json:"notify"`
	// CommentID is the comment that was edited, echoed from the argument.
	// Request context, so it survives the preview — a caller reading a
	// dry run must be able to see WHICH comment would change, and it is
	// the one thing in this envelope they cannot recover from data.issue.
	CommentID string `json:"comment_id"`
	// Body is the ADF document sent, or on a preview the one that WOULD
	// be sent, byte-for-byte as composed.
	Body json.RawMessage `json:"body"`
	// BodyRoute names which input route produced Body — "adf" or
	// "markdown", exactly as IssueCommentAddOutput.BodyRoute does.
	BodyRoute string `json:"body_route"`
	// Visibility is the restriction being applied, absent when the
	// command was not asked to change one. Absent is NOT "visible to
	// everyone" here, unlike on `comment add`: an edit that names no
	// visibility leaves the comment's current restriction exactly as it
	// was (see jira.UpdateCommentRequest), so reading absence as
	// unrestricted would misreport a comment that stays restricted.
	Visibility *CommentVisibilityView `json:"visibility,omitempty"`
	// VisibilityCleared reports that --clear-visibility was given, so the
	// request removed whatever restriction the comment carried.
	// Serialized unconditionally, because it is the ONLY thing that
	// distinguishes "leave the restriction alone" (Visibility absent,
	// this false) from "remove it" (Visibility absent, this true) — two
	// different requests that would otherwise publish identically.
	VisibilityCleared bool `json:"visibility_cleared"`
	// Comment is the comment as Jira stored it, absent on a preview.
	// Published unconditionally on the live path with no --verify to gate
	// it, for IssueCommentAddData.Comment's reason: the PUT's own 200
	// response carries the whole object, so there is no extra round trip
	// to decline. A validated joined receipt also survives cancellation or
	// timeout; the full failure frame still reports the interruption.
	Comment *CommentView `json:"comment,omitempty"`
}

// IssueCommentDeleteOutput is the `issue.comment.delete` operation's
// payload: which comment was removed.
//
// The smallest envelope in this CLI, and every field it does not have is
// a decision.
//
// There is no `deleted` boolean. Data on a live run is published only after a
// validated HTTP 204 acknowledgement. That receipt survives a joined deadline
// or cancellation, even though the overall error frame has ok=false. A preview
// has dry_run=true. An unacknowledged failure has no deletion data.
//
// There is no deleted comment object either, and its absence is not the
// usual "server-created objects stay conditional" rule. Jira answers 204
// with no body, so there is nothing to publish; fetching the comment
// first purely to echo it back would spend a request describing
// something that no longer exists.
//
// What IS here is the request context — which issue, which comment — and
// it survives both paths, so a preview and a live run publish the same
// identification of the thing at stake.
type IssueCommentDeleteOutput struct {
	// Identity places the issue at data.issue.
	Identity
	// MutationState carries dry_run on both paths.
	MutationState
	// CommentID is the comment that was removed, echoed from the
	// argument. See the type's own doc comment for why it is the only
	// operation-specific field here.
	CommentID string `json:"comment_id"`
}

// CommentVisibilityView is a comment's read restriction, in the CLI's
// own snake_case contract rather than the wire's shape.
//
// Type is "group" or "role". The pair holds exactly one restriction,
// which is why the two flags that set it cannot both be given.
type CommentVisibilityView struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func init() {
	Register("issue.comment.add", IssueCommentAddOutput{}, map[string]any{
		"description": "The comment posted to each named issue, with the request that produced it.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. A preview composes the request and stops; it never contacts Jira, so it cannot report whether the issues exist.",
			},
			"body": map[string]any{
				"description": "The ADF document posted, or on a preview the one that would be posted. Identical on both paths.",
			},
			"body_route": map[string]any{
				"description": "Which input route produced the body: adf (--json-input) or markdown (--markdown/--markdown-file).",
			},
			"visibility": map[string]any{
				"description": "The comment's read restriction. Absent when the comment is visible to everyone who can see the issue.",
			},
			"results": map[string]any{
				"description": "One entry per key in input order, retaining the authored request and all joined outcomes on total failure, cancellation or timeout. Completed rows carry the created comment identity; failed or uncertain rows require inspection before retrying because an add may already have been stored.",
			},
		},
	})

	Register("issue.comment.edit", IssueCommentEditOutput{}, map[string]any{
		"description": "One comment's new body, with the request that produced it and the comment as Jira stored it.",
		"properties": map[string]any{
			"issue": map[string]any{
				"description": "The commented issue's identity.",
			},
			"dry_run": map[string]any{
				"description": "True when this was a preview. A preview composes the request and stops; it never contacts Jira, so it cannot report whether the comment exists or whether you may edit it.",
			},
			"notify": map[string]any{
				"description": "Whether the request asked Jira to notify the comment's watchers of the edit. It rides the query string, so it does not appear in body. Unlike issue.edit, this endpoint attaches no permission requirement to the request.",
			},
			"comment_id": map[string]any{
				"description": "The comment that was edited, echoed from the argument. Present on both paths.",
			},
			"body": map[string]any{
				"description": "The ADF document sent, or on a preview the one that would be sent. Identical on both paths.",
			},
			"body_route": map[string]any{
				"description": "Which input route produced the body: adf (--json-input) or markdown (--markdown/--markdown-file).",
			},
			"visibility": map[string]any{
				"description": "The read restriction being applied. Absent means the comment's current restriction is left alone, NOT that the comment is unrestricted.",
			},
			"visibility_cleared": map[string]any{
				"description": "True when --clear-visibility removed the comment's restriction. The only thing distinguishing 'leave it alone' from 'remove it', since both leave visibility absent.",
			},
			"comment": map[string]any{
				"description": "The validated comment as Jira stored it, absent on a preview or unusable acknowledgement. The PUT's own response carries it, so it costs no extra request. Any valid joined receipt survives cancellation or timeout in the full failure frame.",
			},
		},
	})

	Register("issue.comment.delete", IssueCommentDeleteOutput{}, map[string]any{
		"description": "Which comment would be removed in a preview or was acknowledged deleted by HTTP 204. Valid joined acknowledgements survive interruption in the full failure frame; unacknowledged failures have no data.",
		"properties": map[string]any{
			"issue": map[string]any{
				"description": "The commented issue's identity.",
			},
			"dry_run": map[string]any{
				"description": "True when this was a preview. A preview composes the request and stops; nothing was deleted, and it never contacts Jira, so it cannot report whether the comment exists.",
			},
			"comment_id": map[string]any{
				"description": "The comment that was removed, echoed from the argument. Present on both paths.",
			},
		},
	})

	Register("issue.comment.list", IssueCommentListOutput{}, map[string]any{
		"description": "Native offset-paginated comments, newest first unless --order changes it. Valid observed pages survive failed or interrupted reads in the full failure frame; malformed pages are not published.",
		"properties": map[string]any{
			"truncated": map[string]any{
				"description": "True when a safety cap or a failed/interrupted read prevented uninterrupted completion.",
			},
			"truncated_reason": map[string]any{
				"description": "The safety cap or failure that stopped the read. read_failed and response_invalid accompany an error; retained pagination comes from the last valid page.",
				"enum":        []string{TruncatedReasonMaxPages, TruncatedReasonMaxComments, TruncatedReasonReadFailed, TruncatedReasonResponseInvalid},
			},
			"issue": map[string]any{
				"description": "The commented issue's identity.",
			},
			"comments": map[string]any{
				"description": "Comments in the requested order, newest first by default. Each comment's body is its raw ADF document.",
			},
			"pagination": map[string]any{
				"description": "Which slice of the full thread this page is (start_at/max_results/total/returned).",
			},
			"web_url": map[string]any{
				"description": "The commented issue's browse URL. Present only when --web was given; --web skips the fetch, so comments stays empty.",
			},
		},
	})
}
