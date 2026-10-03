package attachmentfiles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectPreservesAuthoredPathsAndExplicitEmptySizes(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "empty.txt"), filepath.Join(dir, "binary.unknown")}
	for i, path := range paths {
		if err := os.WriteFile(path, make([]byte, i), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := Inspect(t.Context(), paths)
	if err != nil || len(metadata) != 2 || metadata[0].Path != paths[0] || metadata[0].Size != 0 || metadata[0].MIMEInferred != "text/plain" || metadata[1].Path != paths[1] || metadata[1].Size != 1 || metadata[1].MIMEInferred != "application/octet-stream" {
		t.Fatalf("preflight lost local input facts: %v %+v", err, metadata)
	}
}

func TestUploadFileAndBatchCeilingsAllowExactBoundary(t *testing.T) {
	for _, sizes := range [][]int64{{100 << 20, 0}, {50 << 20, 50 << 20}, {(100 << 20) + 1}, {50 << 20, (50 << 20) + 1}} {
		t.Run(fmt.Sprint(sizes), func(t *testing.T) {
			paths := make([]string, 0, len(sizes))
			var total int64
			for i, size := range sizes {
				path := filepath.Join(t.TempDir(), fmt.Sprint(i))
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(size); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, path)
				total += size
			}
			wantOK := total <= 100<<20
			_, inspectErr := Inspect(t.Context(), paths)
			files, openErr := Open(t.Context(), paths)
			Close(files)
			if (inspectErr == nil) != wantOK || (openErr == nil) != wantOK {
				t.Fatalf("content ceiling changed: inspect=%v open=%v total=%d", inspectErr, openErr, total)
			}
			if !wantOK && (!errors.Is(inspectErr, ErrInvalid) || !errors.Is(openErr, ErrInvalid)) {
				t.Fatal("file refusal lost its classification")
			}
		})
	}
}

func TestCanceledFileInspectionAndOpenPreserveCause(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, run := range []func() error{
		func() error { _, err := Inspect(ctx, []string{"nonexistent.bin"}); return err },
		func() error { files, err := Open(ctx, []string{"nonexistent.bin"}); Close(files); return err },
	} {
		if err := run(); !errors.Is(err, context.Canceled) {
			t.Fatalf("local input handling erased cancellation: %v", err)
		}
	}
}
