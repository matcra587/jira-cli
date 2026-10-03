// Package attachmentfiles owns local upload preflight and descriptor validation.
// It reads metadata without file contents during inspection; opened inputs are
// rechecked because a path can change after a preview or before a request.
package attachmentfiles

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// MaxUploadBytes is the file and aggregate content ceiling: exactly 100 MiB.
// Multipart framing is not included in this content limit.
const MaxUploadBytes int64 = 100 << 20

// ErrInvalid identifies local input that cannot be uploaded as requested.
var ErrInvalid = errors.New("invalid attachment file")

// Metadata describes a local input, with MIME inferred from its extension.
// MIMEInferred does not inspect bytes or promise the server's detected type.
type Metadata struct {
	Path         string
	Size         int64
	MIMEInferred string
}

// Inspect validates all paths and sizes without opening or reading contents.
// Symlinks to regular files are permitted, matching ordinary named file input.
func Inspect(ctx context.Context, paths []string) ([]Metadata, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: at least one file is required", ErrInvalid)
	}
	metadata := make([]Metadata, 0, len(paths))
	var total int64
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: inspect %q: %w", ErrInvalid, path, err)
		}
		if err := validate(path, info, total); err != nil {
			return nil, err
		}
		total += info.Size()
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		contentType, _, _ = strings.Cut(contentType, ";")
		metadata = append(metadata, Metadata{Path: path, Size: info.Size(), MIMEInferred: contentType})
	}
	return metadata, ctx.Err()
}

// Open owns no handles on failure. On success the caller must close every
// returned file. Descriptor checks protect against replacement after Inspect;
// Unix nonblocking opens also avoid waiting on substituted FIFO inputs.
func Open(ctx context.Context, paths []string) ([]*os.File, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: at least one file is required", ErrInvalid)
	}
	files := make([]*os.File, 0, len(paths))
	ok := false
	defer func() {
		if !ok {
			Close(files)
		}
	}()
	var total int64
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := openFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: open %q: %w", ErrInvalid, path, err)
		}
		files = append(files, file)
		info, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("%w: inspect opened %q: %w", ErrInvalid, path, err)
		}
		if err := validate(path, info, total); err != nil {
			return nil, err
		}
		total += info.Size()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ok = true
	return files, nil
}

func validate(path string, info os.FileInfo, total int64) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q is not a regular file", ErrInvalid, path)
	}
	if info.Size() < 0 || info.Size() > MaxUploadBytes {
		return fmt.Errorf("%w: %q size %d exceeds the %d-byte file limit", ErrInvalid, path, info.Size(), MaxUploadBytes)
	}
	// Subtraction keeps the aggregate check safe from integer overflow.
	if info.Size() > MaxUploadBytes-total {
		return fmt.Errorf("%w: total content exceeds the %d-byte batch limit at %q", ErrInvalid, MaxUploadBytes, path)
	}
	return nil
}

// Close releases read-only inputs. It cannot lose writes or uploaded data.
func Close(files []*os.File) {
	for _, file := range files {
		file.Close() //nolint:errcheck // read-only descriptors have no buffered writes
	}
}
