package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/nativepath"
	"github.com/matcra587/jira-cli/internal/output"
)

// This command's own flag names, spelled once so registration and every
// refusal name the same strings. flagTo is shared with `issue link
// create`, which spells its own target the same way.
const (
	flagMaxDownloadBytes          = "max-download-bytes"
	defaultMaxDownloadBytes int64 = 100 << 20
	flagAs                        = "as"
	flagOverwrite                 = "overwrite"
)

// newAttachmentDownload returns `jira issue attachment download`.
//
// Downloading writes local state while reading Jira. Three decisions
// follow from that ownership:
//
//   - it is declared MutatesLocalState rather than a read, because it
//     changes THIS machine even though it sends nothing to Jira — which
//     is what earns it a --dry-run at all;
//   - the destination is sandboxed against the server-supplied filename
//     (attachment_path.go, which owns that whole argument);
//   - the filename is a site-resolved value, so a bare preview publishes
//     it unresolved and warns, exactly as `@me` does (design §8 item 0).
//
// It takes an ATTACHMENT ID and no issue key, for `link delete`'s
// reason: GET /rest/api/3/attachment/{id} declares one parameter,
// because an attachment id is unique across the site. A key positional
// would be an argument the command reads and cannot send.
func newAttachmentDownload(deps Deps) *cobra.Command {
	// WithValidateRemote and nothing else. --force has nothing to skip
	// (writing a file prompts for no confirmation, and the collision case
	// has its own flag), --verify has nothing to read back, and
	// --no-readback would suppress a readback this command never makes.
	// The remote flag earns its place because the FILENAME lives on the
	// site, so a preview that wants to report where the bytes would land
	// has to be allowed to ask.
	var mut command.MutationFlags
	var to, as string
	var overwrite bool
	var maxBytes int64
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueAttachmentDownloadOutput]{
		Op: "issue.attachment.download",
		// NOT WritesJira: this command only ever GETs. It IS a mutation,
		// of the local filesystem — which is what MutatesLocalState means
		// and what makes --dry-run mandatory here. Classifying it as a
		// read would have left a command that can overwrite a file
		// with no preview at all.
		MutatesLocalState: true,
		Use:               "download ATTACHMENT_ID",
		Short:             "Download an attachment to a file",
		Long: "Download one attachment and write it to disk.\n\n" +
			"It takes the attachment's id and no issue key. An attachment id is " +
			"unique across the whole site and Jira's endpoints take nothing " +
			"else, so asking you for a key as well would be asking for something " +
			"this command cannot check or send. Get the id from `jira issue " +
			"attachment list KEY-123`.\n\n" +
			"The attachment supplies the default filename. Directory components " +
			"are reduced to the final name; a remaining name that cannot safely " +
			"identify a file is refused. The file is written through a sandbox on " +
			"`--to`, so nothing can be written outside that directory even if a " +
			"symlink inside it points elsewhere. Use `--as` to choose a plain " +
			"filename yourself; caller-supplied directories are refused. " +
			"Directory paths are literal. Windows extended directory paths retain " +
			"trailing dots and spaces; `--to` shell completion suggests directories. " +
			"Windows filename suffixes are also literal. Results include extended " +
			"path syntax when needed to reopen them. Links at those literal " +
			"filenames are refused during overwrite checks.\n\n" +
			"Downloads are limited to 100 MiB by default. Use a positive " +
			"`--max-download-bytes` value to allow a larger attachment. An exceeded " +
			"limit removes the partial download and preserves an overwrite target.\n\n" +
			"An existing file is never replaced silently: the command stops and " +
			"names the file, and `--overwrite` is the explicit opt-in. That is a " +
			"separate flag from `--force`, which everywhere else in this CLI " +
			"means \"skip a confirmation\". An overwrite replaces the entry only " +
			"after the complete download has been saved. Failed requests or streams " +
			"leave the original file intact.\n\n" +
			"`--dry-run` writes nothing, and on its own it reads nothing either " +
			"— so it can tell you the directory but not the filename, which " +
			"lives on the site. `data.filename_resolved` says which of those you " +
			"are looking at. Use `--as` to resolve the name locally, or add " +
			"`--validate-remote` to read the attachment's metadata. A resolved " +
			"preview checks the destination and refuses collisions unless " +
			"`--overwrite` is set. It writes nothing; a later download checks " +
			"again because files can change after the preview.\n\n" +
			"`--web` is refused here. It opens an issue page, and this command " +
			"is not scoped to an issue at all.",
		Example: "# download into the current directory, under its own name\n" +
			"$ jira issue attachment download 10042\n\n" +
			"# into a directory of your choosing\n" +
			"$ jira issue attachment download 10042 --to ~/Downloads\n\n" +
			"# under a name you pick\n" +
			"$ jira issue attachment download 10042 --as report.pdf\n\n" +
			"# replace a file that is already there\n" +
			"$ jira issue attachment download 10042 --overwrite\n\n" +
			"# find out exactly what would be written, without writing it\n" +
			"$ jira issue attachment download 10042 --dry-run --validate-remote\n\n" +
			"# find the id first\n" +
			"$ jira issue attachment list KEY-123 --output=json --jq '.data.results[].data.attachments[] | {id, filename}'",
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ATTACHMENT_ID"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddMutationFlags(c, &mut, command.WithValidateRemote())
			command.AddStringVar(c, &to, flagTo, ".",
				"The directory to write into; it must already exist",
				command.FlagOpts{Terse: "Output directory", Placeholder: "dir", Hint: "dir"})
			command.AddStringVar(c, &as, flagAs, "",
				"Write under this name instead of the attachment's own",
				command.FlagOpts{Terse: "Output name", Placeholder: "name"})
			command.AddInt64Var(c, &maxBytes, flagMaxDownloadBytes, defaultMaxDownloadBytes,
				"Maximum attachment bytes to save; must be positive",
				command.FlagOpts{Terse: "Download byte limit", Placeholder: "bytes"})
			command.AddBoolVar(c, &overwrite, flagOverwrite, false,
				"Replace the file if it already exists",
				command.FlagOpts{Terse: "Replace existing"})
		},
		Validate: func() error {
			if err := validateDownloadLimit(maxBytes); err != nil {
				return err
			}
			if err := validateDownloadName(as); err != nil {
				return err
			}
			return refuseWebOnLinkDelete(cmd)
		},
		Human: renderAttachmentDownload,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueAttachmentDownloadOutput, error) {
			return runAttachmentDownload(ctx, deps, inv, downloadOptions{
				to:             to,
				maxBytes:       maxBytes,
				as:             as,
				overwrite:      overwrite,
				validateRemote: mut.ValidateRemote,
			})
		},
	})
}

// downloadOptions is what runAttachmentDownload needs from the flag set,
// gathered into one struct so the handler's signature does not grow a
// parameter per flag — commentAddOptions' own reasoning.
type downloadOptions struct {
	maxBytes       int64
	to             string
	as             string
	overwrite      bool
	validateRemote bool
}

func validateDownloadLimit(limit int64) error {
	if limit > 0 {
		return nil
	}
	err := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+flagMaxDownloadBytes+" must be positive")
	err.Field = flagMaxDownloadBytes
	return err
}

// validateDownloadName refuses an `--as` that is not a plain file name.
//
// **It is STRICTER than safeAttachmentName, and the difference is the
// whole point.** That function REDUCES a hostile name — `../evil.txt`
// becomes `evil.txt` — which is right for a name the server chose,
// because the alternative is failing a download over a filename the
// caller had no say in. It is wrong for a name the CALLER typed: silently
// writing `evil.txt` when they asked for `../evil.txt` is the CLI
// deciding it knows better, and a script assembling a name out of
// variables would never learn that its path component was dropped.
//
// So this requires the name to already BE plain: the reduction must be a
// no-op. Checked before any request, so the refusal arrives before a
// download rather than after one has streamed.
func validateDownloadName(as string) error {
	if as == "" {
		return nil
	}
	reduced, err := safeAttachmentName(as)
	if err == nil && reduced == as {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
		"--%s must be a plain file name with no directories in it, and %q is not — choose the directory with --%s",
		flagAs, as, flagTo,
	))
	e.Field = flagAs
	return e
}

// runAttachmentDownload previews or performs the download.
//
// **The unresolved preview builds no client and opens no file.** It resolves
// the directory — which is local, and the one thing it CAN answer — and
// stops. The filename is deliberately left unresolved rather than
// guessed at, and the warning says so: design §8 item 0's standing rule
// for a value only the site can supply, applied to a filename exactly as
// command.MeUnresolvedWarning applies it to an account id.
//
// **--validate-remote reads the metadata and still writes nothing.** It
// is the same split `issue transition` uses for screen state: dry-run
// stays honestly free of network, and a caller who wants the real answer
// asks for it.
func runAttachmentDownload(ctx context.Context, deps Deps, inv command.Invocation, opts downloadOptions) (envelope.IssueAttachmentDownloadOutput, error) {
	var zero envelope.IssueAttachmentDownloadOutput

	if opts.maxBytes == 0 {
		opts.maxBytes = defaultMaxDownloadBytes
	}
	if err := validateDownloadLimit(opts.maxBytes); err != nil {
		return zero, err
	}

	id := inv.Args[0]
	dir, err := resolveDir(opts.to)
	if err != nil {
		return zero, err
	}
	out := envelope.IssueAttachmentDownloadOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		AttachmentID:  id,
		Directory:     dir,
	}

	// --as needs no site read: the caller supplied the name, so even a
	// bare preview knows exactly where the bytes would go. Resolving it
	// here rather than only on the remote path is what makes
	// `--as x --dry-run` report a real path instead of a warning about a
	// lookup it does not need.
	if opts.as != "" {
		name, err := safeAttachmentName(opts.as)
		if err != nil {
			return zero, err
		}
		out.FilenameResolved = true
		out.Filename = name
		out.Path = nativepath.FilePath(dir, name)
	}

	// The preview's three cases, in the order that keeps a client out of
	// two of them. --as resolves everything locally, so that preview is
	// complete and must not build a client to prove it; a bare preview
	// has nothing left it is allowed to ask, so it warns and stops; only
	// --validate-remote falls through, and only as far as the metadata.
	if inv.DryRun {
		if out.FilenameResolved {
			return out, previewDownloadDestination(dir, out.Filename, opts.overwrite)
		}
		if !opts.validateRemote {
			cli.AddWarning(filenameUnresolvedWarning())
			return out, nil
		}
	}

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	if !out.FilenameResolved {
		name, err := resolveAttachmentName(ctx, client, id)
		if err != nil {
			return zero, err
		}
		out.FilenameResolved = true
		out.Filename = name
		out.Path = nativepath.FilePath(dir, name)
	}
	if inv.DryRun {
		// The remote preview stops here: it has answered the question it
		// exists to answer — which file, at which path — and the code
		// that writes is not on this path.
		return out, previewDownloadDestination(dir, out.Filename, opts.overwrite)
	}

	written, overwrote, err := writeAttachment(ctx, client, id, dir, out.Filename, opts)
	if err != nil {
		return zero, err
	}
	out.BytesWritten = &written
	out.Overwrote = &overwrote
	return out, nil
}

// filenameUnresolvedWarning is the heads-up a bare preview publishes in
// place of the metadata read it deliberately did not perform.
//
// Shaped after command.MeUnresolvedWarning, and Lossy is false for its
// reason: nothing was dropped, the value is simply not known yet and
// will be when the command runs for real.
func filenameUnresolvedWarning() envelope.Warning {
	return envelope.Warning{
		Type: envelope.WarningFilenameUnresolved,
		Message: fmt.Sprintf(
			"the filename is left unresolved in this preview: it comes from the attachment's own metadata, which --dry-run never reads. A live run resolves it; add --%s to resolve it here, or --%s to choose the name yourself.",
			command.FlagValidateRemote, flagAs,
		),
		Lossy: false,
	}
}

// resolveAttachmentName reads the attachment's metadata and reduces its
// filename to something writable, or refuses.
func resolveAttachmentName(ctx context.Context, client core.JiraClient, id string) (string, error) {
	read, err := command.Spin(ctx, "Looking up attachment "+id, func(ctx context.Context) (core.AttachmentRead, error) {
		return client.GetAttachment(ctx, id)
	})
	if err != nil {
		return "", err
	}
	if read.Attachment == nil {
		// GetAttachment's contract is a non-nil Attachment alongside a nil
		// error. Failing loudly beats trusting it: a zero value would
		// carry an empty filename, which safeAttachmentName would then
		// refuse with a message about the SERVER's name rather than about
		// the response being empty.
		return "", fmt.Errorf("issue.attachment.download: empty metadata for attachment %q after a successful read", id)
	}
	return safeAttachmentName(read.Attachment.Filename.Value)
}

// writeAttachment streams the attachment into the sandboxed destination
// and reports how much landed.
//
// **The destination or staging file is created before the content request.**
// A collision or a refused path fails here, with nothing downloaded — which matters for a
// large attachment, since the alternative is streaming fifty megabytes
// and then discovering the destination was unwritable.
//
// **A failed copy removes the partial file.** A truncated download left
// on disk under the right name is worse than no file: the next reader
// has no way to tell it apart from a complete one. The removal goes
// through the same sandbox handle that created it.
func writeAttachment(ctx context.Context, client core.JiraClient, id, dir, name string, opts downloadOptions) (written int64, overwrote bool, err error) {
	if opts.overwrite {
		return writeAttachmentOverwrite(ctx, client, id, dir, name, opts.maxBytes)
	}
	file, path, root, err := createInDir(dir, name, false)
	if err != nil {
		return 0, false, err
	}
	defer root.Close() //nolint:errcheck // root owns path resolution, not buffered contents
	defer file.Close() //nolint:errcheck // successful close is checked below

	written, err = copyAttachment(ctx, client, id, file, path, opts.maxBytes)
	if err != nil {
		file.Close()                  //nolint:errcheck // preserve the stream failure; release before removal
		nativepath.Remove(root, name) //nolint:errcheck // best-effort removal of this download's partial entry
		return 0, false, err
	}
	if err := file.Close(); err != nil {
		nativepath.Remove(root, name) //nolint:errcheck // best-effort cleanup of an incomplete publication
		return 0, false, cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf("closing download for %s: %v", path, err))
	}
	return written, false, nil
}

// copyAttachment streams and syncs bytes without publishing an overwrite.
func copyAttachment(ctx context.Context, client core.JiraClient, id string, file *os.File, path string, maxBytes int64) (int64, error) {
	content, err := command.Spin(ctx, "Downloading attachment "+id, func(ctx context.Context) (core.AttachmentContent, error) {
		return client.GetAttachmentContent(ctx, id)
	})
	if err != nil {
		return 0, err
	}
	defer content.Body.Close() //nolint:errcheck // read-only stream; a close failure loses nothing

	written, err := io.Copy(file, io.LimitReader(content.Body, maxBytes))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %w", cli.NewCodedError(cli.CodeLocalIO, "writing "+path), err)
	}
	if written == maxBytes {
		// Probe beyond the bound without ever writing an excess byte.
		var extra [1]byte
		n, readErr := io.ReadFull(content.Body, extra[:])
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		if n != 0 {
			return 0, cli.NewCodedError(cli.CodeAttachmentDownloadLimit, fmt.Sprintf("attachment exceeds the %d-byte download limit", maxBytes))
		}
		if !errors.Is(readErr, io.EOF) {
			return 0, fmt.Errorf("%w: %w", cli.NewCodedError(cli.CodeLocalIO, "reading "+path), readErr)
		}
	}
	// Sync before the deferred Close, so a write that failed only at
	// flush time is reported rather than swallowed by a deferred close
	// nobody checks. Without this the command could report success for
	// bytes that never reached the disk.
	if err := file.Sync(); err != nil {
		return 0, cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf("flushing %s: %v", path, err))
	}
	return written, nil
}

// renderAttachmentDownload writes the human view. Preview tense is
// structural (axis H2): the branches are separate functions sharing no
// sentence.
func renderAttachmentDownload(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueAttachmentDownloadOutput)
	if !ok {
		return fmt.Errorf("issue.attachment.download: human renderer received %T, want envelope.IssueAttachmentDownloadOutput", env.Data)
	}
	if out.DryRun {
		renderDownloadPreview(v, out)
		return nil
	}
	return renderDownloadResult(v, out)
}

// renderDownloadPreview draws the dry-run view, saying the exact path
// when it knows one and admitting the gap when it does not.
func renderDownloadPreview(v *output.View, out envelope.IssueAttachmentDownloadOutput) {
	if out.FilenameResolved {
		v.Line(cli.Text("Would write attachment ") + cli.Entity(out.AttachmentID) + cli.Text(" to ") + cli.Entity(out.Path) + cli.Text("."))
		v.Hint("Nothing was written. Drop --dry-run to download it.")
		return
	}
	v.Line(cli.Text("Would write attachment ") + cli.Entity(out.AttachmentID) + cli.Text(" into ") + cli.Entity(out.Directory) + cli.Text("."))
	v.Hint("The filename comes from the attachment itself, so this preview cannot say what the file would be called — add --validate-remote to look it up, or --as to choose it.")
	v.Hint("Nothing was written. Drop --dry-run to download it.")
}

// renderDownloadResult draws the live view: where the bytes went and how
// many, and whether something was replaced to make room.
func renderDownloadResult(v *output.View, out envelope.IssueAttachmentDownloadOutput) error {
	verb := "Wrote"
	if out.Overwrote != nil && *out.Overwrote {
		verb = "Replaced"
	}
	// cli.Bytes for the size, the same sink the attachment table uses for
	// the same quantity — and no cli.Text around the assembled line,
	// because wrapping an already-colored span would strip its own SGR
	// bytes back out (nextPageHint's own reasoning).
	if out.BytesWritten == nil {
		return fmt.Errorf("issue.attachment.download: live payload lacks its byte count")
	}
	v.Line(cli.Text(verb+" ") + cli.Entity(out.Path) + cli.Text(" (") + cli.Bytes(*out.BytesWritten) + cli.Text(")."))
	return nil
}
