package jql

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestBuildDefaultsAndSharedFiltersNeedNoClient(t *testing.T) {
	t.Parallel()
	desc := true
	for _, test := range []struct {
		in   command.JQLInput
		want string
	}{
		{command.JQLInput{OrderBy: "updated", Descending: &desc}, "updated >= -365d ORDER BY updated DESC"},
		{command.JQLInput{OrderBy: "none", Descending: &desc, CustomOrder: true}, "updated >= -365d"},
		{command.JQLInput{Project: []string{"KEY"}, Assignee: []string{"me"}, OrderBy: "key", Descending: &desc}, `project in ("KEY") AND assignee = currentUser() ORDER BY key DESC`},
		{command.JQLInput{Key: []string{"KEY-1..KEY-2"}, Created: "2026-01-01..2026-02-01", OrderBy: "updated", Descending: &desc}, `key in ("KEY-1", "KEY-2") AND created >= "2026-01-01" AND created <= "2026-02-01" ORDER BY updated DESC`},
	} {
		deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			t.Error("local build constructed Jira client")
			return nil, errors.New("unexpected")
		}, CacheFor: func(string, string) (core.Cache, error) {
			t.Error("unscoped build opened cache")
			return nil, errors.New("unexpected")
		}}
		out, err := runBuild(t.Context(), deps, buildOptions{filters: test.in})
		if err != nil || out.JQL != test.want || out.URL != "" || out.Precedence != "none" || out.BoardScope.Applied {
			t.Fatalf("local defaults/filters changed: %+v %v", out, err)
		}
	}
}

func TestBuildInvalidFiltersAndCancellationPrecedePreferences(t *testing.T) {
	t.Parallel()
	deps := Deps{Preferences: func() (BuildPreferences, error) {
		t.Error("invalid/canceled build resolved preferences")
		return BuildPreferences{}, errors.New("unexpected")
	}}
	if _, err := runBuild(t.Context(), deps, buildOptions{filters: command.JQLInput{Key: []string{"KEY-0"}}}); cli.MapError(err).Code != cli.CodeFlagValueInvalid {
		t.Fatalf("invalid key lost local classification: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runBuild(ctx, deps, buildOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("local build lost cancellation: %v", err)
	}
}

func TestBuildURLIsBestEffortWithoutAuthentication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		site string
		err  error
		want bool
	}{
		{"https://example.atlassian.net", nil, true}, {"", nil, false}, {"https://user:password@example.com", nil, false}, {"javascript://example.com", nil, false}, {"https://example.atlassian.net", errors.New("unavailable preferences"), false},
	} {
		deps := Deps{Preferences: func() (BuildPreferences, error) { return BuildPreferences{BaseURL: test.site}, test.err }}
		out, err := runBuild(t.Context(), deps, buildOptions{})
		if err != nil || (out.URL != "") != test.want || out.JQL != "updated >= -365d" {
			t.Fatalf("optional URL blocked local composition: %+v %v", out, err)
		}
	}
}

func TestBuildCachedScopeKeepsSavedFilterAndOverridesOrdering(t *testing.T) {
	t.Parallel()
	store := &adapters.MemoryCache{}
	const saved = `project = KEY OR labels = scope ORDER BY Rank ASC`
	if err := store.Set(boardscope.CacheKey("https://example.atlassian.net", "id", "42"), []byte(`{"id":42,"name":"Sprint","jql":"`+saved+`"}`)); err != nil {
		t.Fatal(err)
	}
	_, written, ok := store.Get(boardscope.CacheKey("https://example.atlassian.net", "id", "42"))
	if !ok {
		t.Fatal("seeded board cache is missing")
	}
	calls := 0
	deps := Deps{Preferences: func() (BuildPreferences, error) {
		return BuildPreferences{BaseURL: "https://example.atlassian.net", Account: "user@example.com"}, nil
	}, CacheFor: func(site, account string) (core.Cache, error) {
		calls++
		if site != "https://example.atlassian.net" || account != "user@example.com" {
			t.Error("cache identity changed")
		}
		return store, nil
	}}
	desc := false
	out, err := runBuild(t.Context(), deps, buildOptions{filters: command.JQLInput{Status: []string{"Done"}, OrderBy: "key", Descending: &desc}, boardID: 42})
	if err != nil || out.JQL != `(project = KEY OR labels = scope) AND status in ("Done") ORDER BY key ASC` || out.Precedence != "flag" || !out.BoardScope.Applied || out.BoardScope.ID != 42 || out.BoardScope.JQL != saved || !out.BoardScope.CachedAt.Equal(written) || calls != 1 {
		t.Fatalf("cached filter or precedence lost: %+v %v", out, err)
	}
	if _, err := runBuild(t.Context(), deps, buildOptions{board: "Sprint"}); cli.MapError(err).Code != cli.CodeBoardCacheUnavailable {
		t.Fatalf("uncached selector silently became unscoped: %v", err)
	}
}

func TestBuildMissingOfflineIdentityNeverOpensCache(t *testing.T) {
	t.Parallel()
	for _, prefs := range []BuildPreferences{{}, {BaseURL: "https://example.atlassian.net"}, {BaseURL: "https://user:password@example.com", Account: "user@example.com"}} {
		deps := Deps{Preferences: func() (BuildPreferences, error) { return prefs, nil }, CacheFor: func(string, string) (core.Cache, error) {
			t.Error("missing identity opened cache")
			return nil, errors.New("unexpected")
		}}
		_, err := runBuild(t.Context(), deps, buildOptions{boardID: 42})
		input, ok := errors.AsType[*cli.InputError](err)
		if !ok || input.Code != cli.CodeConfigInvalid || input.Field != "board-id" {
			t.Fatalf("offline identity failure misclassified: %v", err)
		}
	}
}

func TestBuildCachedFilterWithNoConditionsIsNotAnUnscopedQuery(t *testing.T) {
	t.Parallel()
	store := &adapters.MemoryCache{}
	if err := store.Set(boardscope.CacheKey("https://example.atlassian.net", "id", "42"), []byte(`{"id":42,"name":"Sprint","jql":"ORDER BY Rank ASC"}`)); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Preferences: func() (BuildPreferences, error) {
		return BuildPreferences{BaseURL: "https://example.atlassian.net", Account: "user@example.com"}, nil
	}, CacheFor: func(string, string) (core.Cache, error) { return store, nil }}
	if out, err := runBuild(t.Context(), deps, buildOptions{boardID: 42}); cli.MapError(err).Code != cli.CodeBoardCacheUnavailable || out.JQL != "" || out.BoardScope.Applied {
		t.Fatalf("unusable cached filter became a query: %+v %v", out, err)
	}
}
