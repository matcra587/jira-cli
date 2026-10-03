package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	xos "github.com/gechr/x/os"
)

const (
	maxExpandedArchiveBytes int64 = 512 << 20
	maxArchiveEntries             = 1000
)

// Candidate owns private download files. Close it after publication, including
// failed publication; the descriptor, rather than its path, supplies the bytes.
type Candidate struct {
	File          *os.File
	SHA256        string
	ArchiveSHA256 string
	directory     string
}

// Close closes the candidate and removes only its uniquely owned directory.
func (c *Candidate) Close() error {
	return errors.Join(c.File.Close(), os.RemoveAll(c.directory))
}

// Download verifies the frozen archive against its frozen checksums asset before
// extraction. Only the packaged jira/jira.exe regular member is accepted, even
// when the installed executable was renamed. Clive's internal extractor has no
// public descriptor seam, expansion bound or duplicate-member refusal.
func (s *GitHubSource) Download(ctx context.Context, release Release, goos, arch string) (*Candidate, error) {
	name, err := release.ArchiveName(goos, arch)
	if err != nil {
		return nil, err
	}
	archiveAsset, err := release.asset(name, maxArchiveBytes)
	if err != nil {
		return nil, err
	}
	checksums, err := release.asset("checksums.txt", maxChecksumBytes)
	if err != nil {
		return nil, err
	}
	var manifest bytes.Buffer
	if err := s.download(ctx, checksums, &manifest, maxChecksumBytes); err != nil {
		return nil, err
	}
	expected, err := checksumFor(manifest.Bytes(), name)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "jira-update-")
	if err != nil {
		return nil, fmt.Errorf("create private update directory: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			os.RemoveAll(directory) //nolint:errcheck // best-effort cleanup of this unique private stage
		}
	}()
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open private update directory: %w", err)
	}
	defer root.Close() //nolint:errcheck // success close is checked before handing off candidate
	archive, err := root.OpenFile("archive", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create private archive: %w", err)
	}
	defer archive.Close() //nolint:errcheck // success close is checked before handing off candidate
	hash := sha256.New()
	if err := s.download(ctx, archiveAsset, io.MultiWriter(archive, hash), maxArchiveBytes); err != nil {
		return nil, err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return nil, fmt.Errorf("%w: archive SHA-256 differs from checksums.txt", ErrIntegrity)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind verified archive: %w", err)
	}
	file, err := root.OpenFile("candidate", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create private candidate: %w", err)
	}
	keepFile := false
	defer func() {
		if !keepFile {
			file.Close() //nolint:errcheck // failure cleanup; no publication occurred
		}
	}()
	hash.Reset()
	destination := io.MultiWriter(file, hash)
	if goos == xos.PlatformWindows {
		err = extractZIP(ctx, archive, archiveAsset.size, destination)
	} else {
		err = extractTar(ctx, archive, destination)
	}
	if err != nil {
		return nil, err
	}
	if err := errors.Join(archive.Close(), root.Close(), ctx.Err()); err != nil {
		return nil, fmt.Errorf("finish verified download: %w", err)
	}
	keepFile = true
	owned = false
	return &Candidate{File: file, SHA256: hex.EncodeToString(hash.Sum(nil)), ArchiveSHA256: actual, directory: directory}, nil
}

func checksumFor(manifest []byte, name string) (string, error) {
	var selected string
	for line := range strings.SplitSeq(string(manifest), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("%w: malformed checksum manifest", ErrIntegrity)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("%w: invalid SHA-256 in manifest", ErrIntegrity)
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			if selected != "" {
				return "", fmt.Errorf("%w: duplicate archive checksum", ErrIntegrity)
			}
			selected = strings.ToLower(fields[0])
		}
	}
	if selected == "" {
		return "", fmt.Errorf("%w: archive checksum missing", ErrIntegrity)
	}
	return selected, nil
}

func copyExecutable(ctx context.Context, destination io.Writer, source io.Reader, size int64) error {
	if size <= 0 || size > MaxExecutableBytes {
		return fmt.Errorf("%w: executable size is outside the update bound", ErrReleaseInvalid)
	}
	count, err := io.Copy(destination, io.LimitReader(readerWithContext(ctx, source), MaxExecutableBytes+1))
	if err != nil {
		return fmt.Errorf("extract update executable: %w", err)
	}
	if count != size || count > MaxExecutableBytes {
		return fmt.Errorf("%w: executable size differs from archive metadata", ErrReleaseInvalid)
	}
	return ctx.Err()
}

func extractTar(ctx context.Context, archive io.Reader, destination io.Writer) error {
	gz, err := gzip.NewReader(readerWithContext(ctx, archive))
	if err != nil {
		return fmt.Errorf("%w: open gzip archive: %w", ErrReleaseInvalid, err)
	}
	defer gz.Close() //nolint:errcheck // read-only decoder; full stream validation is below
	bounded := &io.LimitedReader{R: readerWithContext(ctx, gz), N: maxExpandedArchiveBytes + 1}
	tarReader := tar.NewReader(bounded)
	found := false
	for entry := 0; ; entry++ {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: read tar archive: %w", ErrReleaseInvalid, err)
		}
		if entry >= maxArchiveEntries {
			return fmt.Errorf("%w: archive has too many members", ErrReleaseInvalid)
		}
		if header.Name != "jira" {
			continue
		}
		if found || header.Typeflag != tar.TypeReg {
			return fmt.Errorf("%w: expected one regular jira archive member", ErrReleaseInvalid)
		}
		if err := copyExecutable(ctx, destination, tarReader, header.Size); err != nil {
			return err
		}
		found = true
	}
	// Reading through gzip EOF validates its trailer, including corruption after
	// the selected executable. Tar EOF alone does not consume the gzip trailer.
	if _, err := io.Copy(io.Discard, bounded); err != nil {
		return fmt.Errorf("%w: finish gzip archive: %w", ErrReleaseInvalid, err)
	}
	if bounded.N <= 0 || !found {
		return fmt.Errorf("%w: executable missing or archive expansion exceeds bound", ErrReleaseInvalid)
	}
	return ctx.Err()
}

func extractZIP(ctx context.Context, archive io.ReaderAt, size int64, destination io.Writer) error {
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return fmt.Errorf("%w: open ZIP archive: %w", ErrReleaseInvalid, err)
	}
	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("%w: archive has too many members", ErrReleaseInvalid)
	}
	var selected *zip.File
	var expanded uint64
	for _, file := range reader.File {
		if file.UncompressedSize64 > uint64(maxExpandedArchiveBytes)-expanded {
			return fmt.Errorf("%w: archive expansion exceeds bound", ErrReleaseInvalid)
		}
		expanded += file.UncompressedSize64
		if file.Name != "jira.exe" {
			continue
		}
		if selected != nil || !file.Mode().IsRegular() || file.UncompressedSize64 == 0 || file.UncompressedSize64 > uint64(MaxExecutableBytes) {
			return fmt.Errorf("%w: expected one bounded regular jira.exe archive member", ErrReleaseInvalid)
		}
		selected = file
	}
	if selected == nil {
		return fmt.Errorf("%w: executable missing from ZIP archive", ErrReleaseInvalid)
	}
	member, err := selected.Open()
	if err != nil {
		return fmt.Errorf("%w: open ZIP executable: %w", ErrReleaseInvalid, err)
	}
	copyErr := copyExecutable(ctx, destination, member, int64(selected.UncompressedSize64))
	return errors.Join(copyErr, member.Close())
}
