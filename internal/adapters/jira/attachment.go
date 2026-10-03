package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/matcra587/jira-cli/internal/attachmentfiles"
	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// xsrfHeader and xsrfNoCheck are Jira's own attachment-upload
// requirement, and the one header this client sets for a single
// endpoint.
//
// Jira refuses a multipart POST to the attachment endpoint without it,
// answering 403 with an XSRF complaint — the check exists because a
// browser can be made to submit a multipart form cross-origin, and this
// header is one a browser form cannot set. An API client opting out is
// what the documentation instructs, not a workaround.
const (
	xsrfHeader  = "X-Atlassian-Token"
	xsrfNoCheck = "no-check"
)

// attachmentFormField is the multipart field name the endpoint reads
// every file from. Jira's own name, and it is "file" for each part even
// when several are sent — the parts are distinguished by their
// filenames, not by their field names.
const attachmentFormField = "file"

// decodeAttachmentIssue validates the complete issue-backed attachment read.
// Other GetIssue projections retain their own field contracts.
func decodeAttachmentIssue(body io.Reader) (*wire.IssueBean, error) {
	decoder := json.NewDecoder(body)
	var issue *wire.IssueBean
	if err := decoder.Decode(&issue); err != nil {
		return nil, fmt.Errorf("%w: cannot decode attachment observation: %w", core.ErrAttachmentResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: attachment observation contains trailing data: %w", core.ErrAttachmentResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: attachment observation contains trailing data", core.ErrAttachmentResponseInvalid)
	}
	if err := core.ValidateAttachmentIssue(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// AddAttachment implements core.JiraClient: uploads files to an issue.
//
// **The body is STREAMED, not buffered.** An io.Pipe carries the
// multipart document from a goroutine that reads each file straight off
// disk into the request as it is sent, so a 50MB attachment costs a
// 32KB copy buffer rather than 50MB of heap. The obvious alternative —
// building the whole multipart body in a bytes.Buffer — is what most
// examples show and would put every uploaded file in memory at once.
//
// The cost of streaming is that the request has no GetBody and therefore
// cannot be replayed. That is not a loss here: this POST is a write, so
// isRead already excludes it from retry, and replaying an upload would
// attach the file twice (see the seam's own doc comment).
//
// Every file is opened BEFORE the request starts, so a missing or
// unreadable path fails as a local error with nothing sent, rather than
// half-way through a multipart body the server has already begun
// reading.
func (c *Client) AddAttachment(ctx context.Context, key string, paths []string) (core.AttachmentWrite, error) {
	files, err := attachmentfiles.Open(ctx, paths)
	if err != nil {
		return core.AttachmentWrite{}, err
	}
	defer attachmentfiles.Close(files)

	pr, pw := io.Pipe()
	defer pr.Close() //nolint:errcheck // read-only request stream; shutdown is explicit below
	defer pw.Close() //nolint:errcheck // producer reports its real error with CloseWithError
	form := multipart.NewWriter(pw)
	req, err := c.newStreamRequest(ctx, http.MethodPost,
		[]string{"rest", "api", "3", "issue", key, "attachments"}, pr, form.FormDataContentType())
	if err != nil {
		return core.AttachmentWrite{}, err
	}
	// Validation precedes producer creation. Every started producer is joined
	// before its input descriptors close, even when transport refuses or the
	// server responds without reading the complete body.
	done := make(chan error, 1)
	go func() {
		err := writeAttachmentParts(form, files)
		pw.CloseWithError(err) // forwards the producer's error to the request reader
		done <- err
	}()
	resp, info, err := c.doWithRetry(req)
	pr.Close() //nolint:errcheck // releases any producer blocked after early response or cancellation
	streamErr := <-done
	if err == nil {
		defer resp.Body.Close() //nolint:errcheck // read-only body; every acknowledged outcome releases it
	}
	if errors.Is(streamErr, attachmentfiles.ErrInvalid) {
		return core.AttachmentWrite{}, fmt.Errorf("%w: upload content changed after the request started; inspect attachments before retrying: %w", core.ErrAttachmentResponseInvalid, errors.Join(err, streamErr))
	}
	if err != nil {
		return core.AttachmentWrite{}, fmt.Errorf("add attachment: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 200, not 201: this endpoint answers OK with the created array,
		// which is its own shape rather than an oversight in Jira's API.
		return core.AttachmentWrite{}, c.apiError(resp, info)
	}
	if streamErr != nil {
		return core.AttachmentWrite{}, fmt.Errorf("%w: Jira acknowledged an incomplete upload stream; inspect attachments before retrying: %w", core.ErrAttachmentResponseInvalid, streamErr)
	}
	var created []wire.Attachment
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		// A 200 acknowledgement is not independent content verification.
		// An unreadable receipt cannot prove which writes landed; retrying
		// without inspection could duplicate attachments.
		return core.AttachmentWrite{}, fmt.Errorf("%w: Jira acknowledged the upload but its response could not be decoded; inspect attachments before retrying: %w", core.ErrAttachmentResponseInvalid, err)
	}
	if err := core.ValidateAttachmentReceipt(created, len(paths)); err != nil {
		return core.AttachmentWrite{}, err
	}
	return core.AttachmentWrite{Attachments: created, Retry: info}, nil
}

// writeAttachmentParts copies every opened file into the multipart
// document and closes it.
//
// filepath.Base on the part filename, deliberately: the part name is
// what Jira stores and shows, and sending `../../etc/passwd` as a
// filename would put a path in the attachment list of an issue other
// people read. The caller chose these paths, so this is hygiene rather
// than a trust boundary — the real one is on the DOWNLOAD side, where
// the name comes from the server.
func writeAttachmentParts(form *multipart.Writer, files []*os.File) error {
	var total int64
	for _, f := range files {
		part, err := form.CreateFormFile(attachmentFormField, filepath.Base(f.Name()))
		if err != nil {
			return fmt.Errorf("add attachment: composing the upload for %q: %w", f.Name(), err)
		}
		remaining := attachmentfiles.MaxUploadBytes - total
		written, err := io.Copy(part, io.LimitReader(f, remaining))
		if err != nil {
			return fmt.Errorf("add attachment: reading %q: %w", f.Name(), err)
		}
		total += written
		if written == remaining {
			var probe [1]byte
			n, err := f.Read(probe[:])
			if n > 0 {
				return fmt.Errorf("%w: content grew beyond the %d-byte batch limit at %q; inspect attachments before retrying", attachmentfiles.ErrInvalid, attachmentfiles.MaxUploadBytes, f.Name())
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("add attachment: reading %q: %w", f.Name(), err)
			}
		}
	}
	return form.Close()
}

// GetAttachment implements core.JiraClient: one attachment's metadata.
func (c *Client) GetAttachment(ctx context.Context, id string) (core.AttachmentRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "attachment", id}, nil, nil)
	if err != nil {
		return core.AttachmentRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.AttachmentRead{}, fmt.Errorf("get attachment: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.AttachmentRead{}, c.apiError(resp, info)
	}
	var meta wire.AttachmentMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return core.AttachmentRead{}, fmt.Errorf("get attachment: decode response: %w", err)
	}
	return core.AttachmentRead{Attachment: &meta, Retry: info}, nil
}

// GetAttachmentContent implements core.JiraClient: the bytes.
//
// **The response body is NOT closed here**, which breaks this file's own
// pattern and is the whole point of the method: the stream IS the
// result, and closing it would hand the caller an exhausted reader. Every
// failure path closes it before returning, so the handle escapes only
// alongside a nil error — which is the contract core.AttachmentContent
// states.
func (c *Client) GetAttachmentContent(ctx context.Context, id string) (core.AttachmentContent, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "attachment", "content", id}, nil, nil)
	if err != nil {
		return core.AttachmentContent{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.AttachmentContent{}, fmt.Errorf("get attachment content: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close() //nolint:errcheck // failure path; the body is only read for its error document
		return core.AttachmentContent{}, c.apiError(resp, info)
	}
	// The 303 the endpoint documents never reaches here: the transport
	// follows it (a redirect on a GET, which its own policy allows), so
	// this is the FINAL response — the media host's 200, or Jira's own
	// when the site serves content directly.
	return core.AttachmentContent{Body: resp.Body, Size: resp.ContentLength, Retry: info}, nil
}

// DeleteAttachment implements core.JiraClient: removes one attachment.
func (c *Client) DeleteAttachment(ctx context.Context, id string) (core.AttachmentDeleteWrite, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, []string{"rest", "api", "3", "attachment", id}, nil, nil)
	if err != nil {
		return core.AttachmentDeleteWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.AttachmentDeleteWrite{}, fmt.Errorf("delete attachment: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusNoContent {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.AttachmentDeleteWrite{}, fmt.Errorf("%w: attachment deletion returned HTTP %d instead of 204", core.ErrAttachmentResponseInvalid, resp.StatusCode)
		}
		return core.AttachmentDeleteWrite{}, c.apiError(resp, info)
	}
	return core.AttachmentDeleteWrite{StatusCode: resp.StatusCode, Retry: info}, nil
}
