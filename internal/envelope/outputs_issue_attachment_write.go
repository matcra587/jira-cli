package envelope

// The attachment mutation payloads. Uploads carry local input paths;
// downloads carry local publication facts; deletion carries a resource ID.

// IssueAttachmentAddOutput is the `issue.attachment.add` operation's
// payload: the files attached to each named issue.
//
// The composed request at the top and the per-key outcome in results[],
// which is IssueCommentAddOutput's split unchanged: one set of paths is
// inspected once and reopened for every key, so paths belong at the top rather
// than copied into each of N results.
type IssueAttachmentAddOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// Files are the local paths that were uploaded, as the caller gave
	// them. Request context survives the preview alongside inspected
	// metadata, without inventing created attachment resources.
	//
	// The paths are published as typed, not resolved to absolute. They
	// are the caller's own input echoed back for confirmation, and
	// rewriting them would make the envelope disagree with the command
	// line that produced it.
	Files []string `json:"files"`
	// FileDetails carries local preflight metadata on previews and live runs.
	// It describes inspection, not a promise that files cannot change later.
	FileDetails []AttachmentFileInput `json:"file_details"`
	// Results is one entry per key, in argv order. Once fan-out starts,
	// total failures and cancellation retain the joined outcomes.
	KeyedResults[IssueAttachmentAddData]
}

// AttachmentFileInput is an inspected local input. MIME is inferred from the
// filename extension; no contents are read to determine it.
type AttachmentFileInput struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	MIMEInferred string `json:"mime_inferred"`
}

// IssueAttachmentAddData is one key's outcome: the attachments Jira
// created there.
//
// An ARRAY, where most per-key payloads carry one object, because one
// upload request can carry several files and Jira answers with all of
// them. Absent on a preview — nothing was created.
type IssueAttachmentAddData struct {
	// Attachments are the attachments Jira created on this issue, with
	// the id each is downloaded or deleted by. Published unconditionally
	// on the live path with no --verify to gate it, for
	// IssueCommentAddData.Comment's reason: the upload's own response
	// carries them, so there is no extra round trip to decline.
	//
	// It reuses AttachmentView — the same object `attachment list`
	// publishes — because it is the same resource read from the same
	// shape, and an agent that learned it once reads it in both places.
	Attachments []AttachmentView `json:"attachments,omitempty"`
}

// IssueAttachmentDownloadOutput is the `issue.attachment.download`
// operation's payload: where the file went, or would go.
//
// A download reads Jira but mutates local state, so it carries dry_run.
// The default filename needs a metadata read. An unresolved preview raises
// a warning; --as resolves the name locally, while --validate-remote reads
// metadata without downloading content or writing a file.
type IssueAttachmentDownloadOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// AttachmentID is the attachment asked for, echoed from the argument.
	AttachmentID string `json:"attachment_id"`
	// Directory is the ABSOLUTE directory the file is written into,
	// resolved from --to. Absolute rather than as-typed, because a
	// machine reader that gets "." is being told nothing — it does not
	// know the process's working directory.
	Directory string `json:"directory"`
	// FilenameResolved reports whether a name is known, either from --as
	// or metadata. False only when a local preview has neither source.
	//
	// Serialized unconditionally, because it is what makes an absent
	// filename unambiguous — without it a caller cannot tell "not looked
	// up" from "the attachment has no name".
	FilenameResolved bool `json:"filename_resolved"`
	// Filename is the name the file is written under, after the
	// server-supplied name has been reduced to something safe. Absent
	// until FilenameResolved is true.
	//
	// Server-supplied directory components are reduced to the final name.
	// Caller-supplied --as must already be plain; unsafe names are refused.
	Filename string `json:"filename,omitempty"`
	// Path is Directory and Filename joined — the exact file that was or
	// would be written. Published rather than left for a caller to
	// assemble, because the join is the CLI's own decision and a caller
	// recomputing it could get a different answer.
	Path string `json:"path,omitempty"`
	// BytesWritten is how much was actually written. Present only on a
	// live run: a preview writes nothing, and publishing the metadata's
	// claimed size here instead would report a number the command never
	// produced.
	// A pointer preserves a complete empty download's zero count while
	// keeping previews absent: a preview has not produced a file.
	BytesWritten *int64 `json:"bytes_written,omitempty"`
	// Overwrote reports that an existing file was replaced. Serialized
	// unconditionally on a live run, because "wrote a new file" and
	// "replaced one you had" are different things to have done to
	// somebody's disk.
	Overwrote *bool `json:"overwrote,omitempty"`
}

// WarningFilenameUnresolved is what a LOCAL download preview publishes
// in place of the metadata read it deliberately did not perform.
//
// The same shape command.MeUnresolvedWarning uses for `@me`, and for the
// identical reason recorded in design §8 item 0: the value comes from
// the site, `--dry-run` never contacts the site, so the preview says
// what it does not know rather than inventing it or staying quiet.
const WarningFilenameUnresolved = "filename_unresolved"

// IssueAttachmentDeleteOutput is the `issue.attachment.delete`
// operation's payload: which attachment was removed.
//
// It carries NO issue, and the absence is the contract rather than an
// oversight — the second envelope in this CLI with that property, after
// IssueLinkDeleteOutput. An attachment id is unique across the site
// (DELETE /rest/api/3/attachment/{id} takes nothing else), so there is
// no issue this operation is scoped to and an Identity embed would
// publish a data.issue the command never resolved.
type IssueAttachmentDeleteOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// AttachmentID is the attachment that was removed, echoed from the
	// argument — the whole request, since the endpoint takes nothing
	// else.
	AttachmentID string `json:"attachment_id"`
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.attachment.add", IssueAttachmentAddOutput{}, map[string]any{
		"description": "The files attached to each named issue, with the ids they can be downloaded or deleted by.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Local metadata is checked for regular files and per-file/batch content of at most 100 MiB. No contents are read or uploaded and Jira is never contacted; readability and issue existence remain unverified.",
			},
			"files": map[string]any{
				"description": "The local paths uploaded, as typed. Identical on both paths.",
			},
			"file_details": map[string]any{
				"description": "Local preflight metadata in input order: authored path, size in bytes including zero, and MIME type inferred from the extension. Present on previews and live runs; inputs can change after inspection and are rechecked when opened.",
			},
			"results": map[string]any{
				"description": "One entry per key, in input order. Successful entries carry created attachment identities. After fan-out starts, total failure and cancellation retain all joined outcomes in the failure frame. Inspect uncertain failed targets before repeating a write; a failed entry does not prove no files were stored.",
			},
		},
	})

	Register("issue.attachment.download", IssueAttachmentDownloadOutput{}, map[string]any{
		"description": "Where an attachment was written on this machine. Reads from Jira and writes locally, so it is previewable like any mutation.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. A preview writes no file. Without --as or --validate-remote, the filename remains unresolved. A resolved preview checks the destination and collision policy; the live writer checks again.",
			},
			"attachment_id": map[string]any{
				"description": "The attachment asked for, echoed from the argument. Present on both paths.",
			},
			"directory": map[string]any{
				"description": "The absolute directory the file is written into, resolved from --to. Present on both paths.",
			},
			"filename_resolved": map[string]any{
				"description": "Whether the filename is known from --as or attachment metadata. False on a local preview without --as, distinguishing an unresolved filename from an unnamed attachment.",
			},
			"filename": map[string]any{
				"description": "The plain filename supplied with --as or reduced from the attachment's server filename. Unsafe names are refused. Absent until filename_resolved is true.",
			},
			"path": map[string]any{
				"description": "The exact file written or that would be written — directory and filename joined by the CLI.",
			},
			"bytes_written": map[string]any{
				"description": "The actual byte count, including zero for a complete empty download. Present only on a live run; absent in previews.",
			},
			"overwrote": map[string]any{
				"description": "True when an existing file was replaced (--overwrite). Present only on a live run.",
			},
		},
	})

	Register("issue.attachment.delete", IssueAttachmentDeleteOutput{}, map[string]any{
		"description": "Which attachment was previewed or acknowledged as deleted by HTTP 204. Validated acknowledgements survive cancellation or timeout; unacknowledged failures publish no deletion data. Deletion cannot be undone, and the site-wide attachment identity names no issue.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Nothing was deleted, and it never contacts Jira, so it cannot report whether the attachment exists.",
			},
			"attachment_id": map[string]any{
				"description": "The requested attachment identity on a local preview or a validated HTTP 204 deletion acknowledgement. It does not independently prove remote absence.",
			},
		},
	})
}
