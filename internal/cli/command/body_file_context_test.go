package command

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
)

func TestInputCancellationPreservesBorrowedFile(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "input.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // test owns the file
	if _, err := file.WriteString("fixture input"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		data, err := readInputContext(ctx, "input", "--input", "standard input", file, func(input io.Reader) ([]byte, error) {
			data, err := io.ReadAll(input)
			cancel()
			synctest.Wait()
			return data, err
		})
		if !errors.Is(err, context.Canceled) || data != nil {
			t.Fatalf("expired file input: error=%v, accepted data=%t", err, data != nil)
		}
		if _, err := file.Stat(); err != nil {
			t.Fatal("cancellation closed the borrowed file")
		}
	})
}
