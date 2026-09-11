package completion

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/startup"
)

type completionErrorWriter struct {
	err    error
	writes int
}

func (w *completionErrorWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, w.err
}

type completionShortWriter struct{}

func (completionShortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestUniqueCachedNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []namedCacheValue
		want []string
	}{
		{
			name: "collapses per-workflow duplicates in first-seen order",
			in: []namedCacheValue{
				{Name: "To Do"},
				{Name: "In Progress"},
				{Name: "Done"},
				{Name: "To Do"},
				{Name: "In Progress"},
				{Name: "Done"},
				{Name: "To Do"},
			},
			want: []string{"To Do", "In Progress", "Done"},
		},
		{
			name: "drops blank names",
			in:   []namedCacheValue{{Name: "High"}, {Name: ""}, {Name: "Low"}},
			want: []string{"High", "Low"},
		},
		{
			name: "passes a unique list through unchanged",
			in:   []namedCacheValue{{Name: "Highest"}, {Name: "High"}, {Name: "Medium"}},
			want: []string{"Highest", "High", "Medium"},
		},
		{
			name: "empty input yields an empty, non-nil slice",
			in:   nil,
			want: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := uniqueCachedNames(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("uniqueCachedNames = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandlerWritesDynamicCandidatesToInjectedWriter(t *testing.T) {
	var stdout bytes.Buffer
	handler := NewHandler(&stdout, startup.Globals{})

	handler.Complete("fish", "cacheresource", nil)
	if err := handler.Err(); err != nil {
		t.Fatalf("handler.Err() = %v", err)
	}
	got := stdout.String()
	if got == "" {
		t.Fatal("dynamic completion emitted no candidates")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(got), "\n") {
		if strings.ContainsAny(line, "\r\t") {
			t.Fatalf("dynamic completion emitted non-candidate text %q", line)
		}
	}
}

func TestHandlerReturnsFirstDynamicCandidateWriteFailure(t *testing.T) {
	writeErr := errors.New("stdout closed")
	stdout := &completionErrorWriter{err: writeErr}
	handler := NewHandler(stdout, startup.Globals{})

	handler.Complete("fish", "cacheresource", nil)
	if !errors.Is(handler.Err(), writeErr) {
		t.Fatalf("handler.Err() = %v, want writer failure", handler.Err())
	}
	if _, ok := errors.AsType[*cli.OutputError](handler.Err()); !ok {
		t.Fatalf("handler.Err() type = %T, want *cli.OutputError", handler.Err())
	}
	if stdout.writes != 1 {
		t.Fatalf("stdout writes = %d, want one attempt after the first failure", stdout.writes)
	}
}

func TestHandlerReturnsDynamicCandidateShortWrite(t *testing.T) {
	handler := NewHandler(completionShortWriter{}, startup.Globals{})

	handler.Complete("fish", "cacheresource", nil)
	if !errors.Is(handler.Err(), io.ErrShortWrite) {
		t.Fatalf("handler.Err() = %v, want io.ErrShortWrite", handler.Err())
	}
	if _, ok := errors.AsType[*cli.OutputError](handler.Err()); !ok {
		t.Fatalf("handler.Err() type = %T, want *cli.OutputError", handler.Err())
	}
}

func TestConfigKeyCandidatesFallBackToTemplatesWhenConfigCannotLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("profiles: ["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	var stdout bytes.Buffer

	if err := emitConfigKeys(&stdout, startup.Globals{ConfigPath: path}); err != nil {
		t.Fatalf("emitConfigKeys: %v", err)
	}
	if !strings.Contains(stdout.String(), "profiles.<profile>.base_url\t") {
		t.Fatalf("template config keys missing from fallback output:\n%s", stdout.String())
	}
}
