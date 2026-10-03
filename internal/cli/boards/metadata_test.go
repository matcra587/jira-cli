package boards

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestBoardMetadataRequiresExplicitCompletionFacts(t *testing.T) {
	t.Parallel()
	complete := `{"boards":[],"fetched_at":"STAMP","pages_fetched":1,"complete":true,"truncated":false,"project_failures":[]}`
	capped := `{"boards":[],"fetched_at":"STAMP","pages_fetched":100,"complete":false,"truncated":true,"reason":"max_pages","next_start":0,"project_failures":[]}`
	partialProjects := `{"boards":[{"id":42,"name":"Example board","project_keys":[],"projects_complete":false,"projects_next_start":0}],"fetched_at":"STAMP","pages_fetched":1,"complete":true,"truncated":false,"project_failures":[{"board_id":42,"error":{"code":"jira_api_error"}}]}`
	for _, tc := range []struct {
		name, raw string
	}{
		{name: "missing completion", raw: strings.Replace(capped, `"complete":false,`, "", 1)},
		{name: "null completion", raw: strings.Replace(capped, `"complete":false`, `"complete":null`, 1)},
		{name: "missing truncation", raw: strings.Replace(complete, `"truncated":false,`, "", 1)},
		{name: "null truncation", raw: strings.Replace(complete, `"truncated":false`, `"truncated":null`, 1)},
		{name: "missing project completion", raw: strings.Replace(partialProjects, `"projects_complete":false,`, "", 1)},
		{name: "null project completion", raw: strings.Replace(partialProjects, `"projects_complete":false`, `"projects_complete":null`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := []byte(strings.ReplaceAll(tc.raw, "STAMP", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)))
			if value, err := DecodeMetadata(raw); err == nil {
				t.Errorf("metadata inspection accepted missing facts: %+v", value)
			}
			var store adapters.MemoryCache
			if err := store.Set(metacache.BoardListKey, raw); err != nil {
				t.Fatal(err)
			}
			calls := 0
			out, err := runList(t.Context(), Deps{
				Cache: func() (core.Cache, error) { return &store, nil },
				JiraClient: func() (core.JiraClient, error) {
					calls++
					return boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) {
						return boardPage(0, true), nil
					}}, nil
				},
			}, options{all: true})
			if err != nil || out.FromCache || calls != 1 || !out.Pagination.Complete {
				t.Fatalf("board consumer trusted missing facts: %+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestBoardMetadataPayloadIsReadableByExistingListConsumer(t *testing.T) {
	t.Parallel()
	fake := boardFake{
		boards: func(ctx context.Context, start, size int) (core.BoardRead, error) {
			if ctx != t.Context() || start != 0 || size != pageSize {
				t.Fatal("board primer bypassed existing collector policy")
			}
			return boardPage(0, true, board(42, "Example board")), nil
		},
		projects: func(ctx context.Context, id int64, start, size int) (core.BoardProjectRead, error) {
			if ctx != t.Context() || id != 42 || start != 0 || size != pageSize {
				t.Fatal("board primer bypassed existing project enrichment")
			}
			return projectPage(0, true, "EX", "OPS"), nil
		},
	}
	value, err := FetchMetadata(t.Context(), fake, false)
	if err != nil || !value.Complete || value.Count != 1 || value.Pages != 1 || value.NextStart != nil || value.Payload == nil {
		t.Fatalf("board primer receipt = %+v, %v", value, err)
	}
	var native snapshot
	if err := json.Unmarshal(value.Payload, &native); err != nil || !validSnapshot(native) {
		t.Fatalf("board primer produced incompatible snapshot: %v", err)
	}
	var store adapters.MemoryCache
	if err := store.Set(metacache.BoardListKey, value.Payload); err != nil {
		t.Fatal(err)
	}
	out, err := runList(t.Context(), Deps{
		Cache: func() (core.Cache, error) { return &store, nil },
		JiraClient: func() (core.JiraClient, error) {
			t.Fatal("primed board payload caused an unnecessary Jira read")
			return nil, errors.New("unexpected Jira read")
		},
	}, options{all: true})
	if err != nil || !out.FromCache || !out.CacheSaved || !out.Pagination.Complete || out.Count != 1 || out.Boards[0].ID != 42 || !out.Boards[0].ProjectsComplete || !slices.Equal(out.Boards[0].ProjectKeys, []string{"EX", "OPS"}) {
		t.Fatalf("primed metadata lost consumer behavior: %+v, %v", out, err)
	}
}

func TestBoardMetadataRetainsProjectDiagnosticsAndPartialReadCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("board continuation unavailable")
	for _, tt := range []struct {
		name         string
		boardFailure bool
	}{
		{name: "project failure", boardFailure: false},
		{name: "later board failure", boardFailure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := boardFake{
				boards: func(_ context.Context, start, _ int) (core.BoardRead, error) {
					if start != 0 {
						return core.BoardRead{}, cause
					}
					return boardPage(0, !tt.boardFailure, board(42, "Example board")), nil
				},
				projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
					return core.BoardProjectRead{}, cause
				},
			}
			value, err := FetchMetadata(t.Context(), fake, false)
			var native snapshot
			if decodeErr := json.Unmarshal(value.Payload, &native); decodeErr != nil || value.Count != 1 || value.Pages != 1 || len(native.Boards) != 1 {
				t.Fatalf("partial board metadata lost native payload: %+v, %v", value, decodeErr)
			}
			if tt.boardFailure {
				if !errors.Is(err, cause) || value.Complete || value.NextStart == nil || *value.NextStart != 1 {
					t.Fatalf("later board failure lost cause or resume: %+v, %v", value, err)
				}
			} else if err != nil || !value.Complete || len(native.Failures) != 1 || native.Failures[0].BoardID != 42 || native.Boards[0].ProjectsComplete {
				t.Fatalf("project enrichment diagnostics lost: %+v, %v", native, err)
			}
		})
	}
}
