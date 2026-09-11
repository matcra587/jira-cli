package unit

// BoardService.ResolveOne does case-insensitive exact-only matching
// against the cache. Zero matches → ErrBoardNotFound. Two-or-more
// matches → *AmbiguousBoardError carrying every candidate so the cmd
// layer can render the disambiguation envelope.

import (
	"context"
	"encoding/json"
	stdlibErrors "errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/cache"
	"github.com/matcra587/jira-cli/internal/jira"
)

// resolverFixture writes a boards cache file with the supplied entries
// for the supplied profile and returns a BoardService (with a no-op
// httptest server, since ResolveOne shouldn't hit the wire).
func resolverFixture(t *testing.T, profile string, boards []jira.Board) (*jira.Client, func()) {
	t.Helper()
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	// Marshal the boards into the cache file format expected by the
	// service (items: [...] envelope).
	path, err := cache.Path(profile, "boards")
	if err != nil {
		t.Fatalf("cache.Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Use the cache.Write API so the file shape matches production.
	raw, err := json.Marshal(boards)
	if err != nil {
		t.Fatalf("marshal boards: %v", err)
	}
	if _, err := cache.Write(profile, "boards", raw); err != nil {
		t.Fatalf("cache.Write: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("resolver hit the network — should be cache-only")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	return client, srv.Close
}

func TestResolveOneExactMatchCaseInsensitive(t *testing.T) {
	client, cleanup := resolverFixture(t, "default", []jira.Board{
		{ID: new(42), Name: new("Engineering Sprint"), Type: new("scrum"), ProjectKeys: []string{"ENG"}},
		{ID: new(99), Name: new("Platform Roadmap"), Type: new("kanban"), ProjectKeys: []string{"PLAT"}},
	})
	defer cleanup()
	svc := jira.NewBoardService(client)

	scope, err := svc.ResolveOne(context.Background(), "default", "engineering sprint")
	if err != nil {
		t.Fatalf("ResolveOne: %v", err)
	}
	if scope.Board.ID == nil || *scope.Board.ID != 42 {
		t.Fatalf("ResolveOne returned id %v; want 42", scope.Board.ID)
	}
}

func TestResolveOneZeroMatchesReturnsErrBoardNotFound(t *testing.T) {
	client, cleanup := resolverFixture(t, "default", []jira.Board{
		{ID: new(42), Name: new("Engineering"), Type: new("scrum"), ProjectKeys: []string{"ENG"}},
	})
	defer cleanup()
	svc := jira.NewBoardService(client)

	_, err := svc.ResolveOne(context.Background(), "default", "Nonexistent")
	if !stdlibErrors.Is(err, jira.ErrBoardNotFound) {
		t.Fatalf("ResolveOne err = %v; want ErrBoardNotFound", err)
	}
}

func TestResolveOneTwoOrMoreMatchesReturnsAmbiguousError(t *testing.T) {
	client, cleanup := resolverFixture(t, "default", []jira.Board{
		{ID: new(42), Name: new("Engineering"), Type: new("scrum"), ProjectKeys: []string{"ENG"}},
		{ID: new(99), Name: new("engineering"), Type: new("kanban"), ProjectKeys: []string{"OPS"}},
	})
	defer cleanup()
	svc := jira.NewBoardService(client)

	_, err := svc.ResolveOne(context.Background(), "default", "Engineering")
	var ambig *jira.AmbiguousBoardError
	if !stdlibErrors.As(err, &ambig) {
		t.Fatalf("ResolveOne err = %v; want *AmbiguousBoardError", err)
	}
	if len(ambig.Candidates) != 2 {
		t.Fatalf("Candidates len = %d; want 2", len(ambig.Candidates))
	}
}

func TestResolveOneNoSubstringFallback(t *testing.T) {
	// Substring resolution is explicitly NOT shipped (consistent with
	// the link-type policy). Typing "eng" must NOT match "Engineering
	// Sprint" — zero matches → not-found.
	client, cleanup := resolverFixture(t, "default", []jira.Board{
		{ID: new(42), Name: new("Engineering Sprint"), Type: new("scrum"), ProjectKeys: []string{"ENG"}},
	})
	defer cleanup()
	svc := jira.NewBoardService(client)

	_, err := svc.ResolveOne(context.Background(), "default", "eng")
	if !stdlibErrors.Is(err, jira.ErrBoardNotFound) {
		t.Fatalf("ResolveOne err = %v; want ErrBoardNotFound (no substring fallback)", err)
	}
}

func TestResolveOneUnicodeNamePreserved(t *testing.T) {
	// Unicode in board names preserved verbatim through cache,
	// completion, and resolver.
	client, cleanup := resolverFixture(t, "default", []jira.Board{
		{ID: new(42), Name: new("Café & Croissant 🥐"), Type: new("scrum"), ProjectKeys: []string{"FOOD"}},
	})
	defer cleanup()
	svc := jira.NewBoardService(client)

	scope, err := svc.ResolveOne(context.Background(), "default", "Café & Croissant 🥐")
	if err != nil {
		t.Fatalf("ResolveOne with Unicode name: %v", err)
	}
	if scope.Board.Name == nil || *scope.Board.Name != "Café & Croissant 🥐" {
		t.Fatalf("name lost in resolution: %v", scope.Board.Name)
	}
}

// ptr helpers — small *T = &T constructors for nullable fields.
//
