package root

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

func TestLocalPathCompletionPreservesLiteralNamesAndExplicitDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	names := []string{"candidate space.txt", "candidate,comma.txt", "candidate$HOME;name.txt", "unrelated.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, `candidate"quote.txt`, "candidate\nunsupported.txt")
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("unread content"), 0o000); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "candidate directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	prefix := directory + string(filepath.Separator)
	want := []string{prefix + "candidate space.txt", prefix + "candidate,comma.txt", prefix + "candidate$HOME;name.txt", prefix + "candidate directory" + string(filepath.Separator)}
	if runtime.GOOS != "windows" {
		want = append(want, prefix+`candidate"quote.txt`)
	}
	slices.Sort(want)
	if got := localPathCandidates(t.Context(), prefix+"candidate"); !reflect.DeepEqual(got, want) {
		t.Fatal("filesystem completion changed literal names or read outside its prefix", got, want)
	}
	// A glob-shaped filename is a literal prefix, not an expression selecting
	// the ordinary candidate files. Windows cannot create these path bytes.
	if runtime.GOOS != "windows" {
		name := "candidate[?]*literal"
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := localPathCandidates(t.Context(), prefix+"candidate[?]*"); !reflect.DeepEqual(got, []string{prefix + name}) {
			t.Fatal("path completion interpreted glob syntax", got)
		}
	}
}

func TestLocalPathCompletionRefusesCanceledMissingAndNonDirectoryWork(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	file := filepath.Join(directory, "regular")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		canceled bool
		prefix   string
	}{
		{canceled: true, prefix: directory},
		{prefix: filepath.Join(directory, "missing", "prefix")},
		{prefix: file + string(filepath.Separator)},
		{prefix: "bad\nrecord"},
		{prefix: "bad\tcolumn"},
		{prefix: "bad\x1b[31mrecord"},
	} {
		lookupContext := t.Context()
		if tc.canceled {
			lookupContext = ctx
		}
		if got := localPathCandidates(lookupContext, tc.prefix); got != nil {
			t.Fatal("failed or canceled lookup supplied candidates", tc.prefix, got)
		}
	}
}

func TestLocalPathCompletionRefusesIncompleteOversizedInventory(t *testing.T) {
	directory := t.TempDir()
	for i := 0; i <= maxCompletionDirectoryEntries; i++ {
		path := filepath.Join(directory, fmt.Sprintf("candidate-%05d", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := localPathCandidates(t.Context(), filepath.Join(directory, "candidate")); got != nil {
		t.Fatal("oversized inventory supplied partial candidates", len(got))
	}
}
