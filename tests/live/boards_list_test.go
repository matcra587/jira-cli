//go:build live

package live

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLiveBoardsList(t *testing.T) {
	r := newLiveRun(t)
	boardID, err := strconv.ParseInt(requiredEnv(t, "JIRA_LIVE_BOARD_ID"), 10, 64)
	if err != nil || boardID <= 0 {
		t.Fatal("live board fixture requires a positive ID")
	}
	var expected []envelope.BoardRow
	for start := 0; start < 10000; {
		raw := rawRead(t, r, "api", "get", "/rest/agile/1.0/board", "--query=startAt="+strconv.Itoa(start), "--query=maxResults=50")
		var page struct {
			IsLast  *bool `json:"isLast"`
			StartAt int   `json:"startAt"`
			Values  []struct {
				ID   int64                 `json:"id"`
				Name string                `json:"name"`
				Type jira.Nullable[string] `json:"type"`
			} `json:"values"`
		}
		if json.Unmarshal(raw.Response.JSON, &page) != nil || page.IsLast == nil || page.Values == nil || page.StartAt != start {
			t.Fatal("raw board page has no explicit progress/completion")
		}
		for _, row := range page.Values {
			expected = append(expected, envelope.BoardRow{ID: row.ID, Name: strings.TrimSpace(row.Name), Type: row.Type, ProjectKeys: liveAssociatedKeys(t, r, row.ID), ProjectsComplete: true})
		}
		if *page.IsLast {
			break
		}
		if len(page.Values) == 0 {
			t.Fatal("raw board walk cannot advance")
		}
		start += len(page.Values)
		if start >= 10000 {
			t.Fatal("raw board acceptance reached its bound without completion")
		}
	}
	if len(expected) == 0 {
		t.Fatal("board visibility is empty; listing acceptance is unproven")
	}
	slices.SortFunc(expected, func(a, b envelope.BoardRow) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	found := false
	for _, board := range expected {
		if board.ID == boardID && slices.Contains(board.ProjectKeys, r.manifest.Project) {
			found = true
		}
	}
	if !found {
		t.Fatal("designated board/project relation is absent from raw listing")
	}
	cacheDir := t.TempDir()
	invoke := func(flags ...string) envelope.BoardsListOutput {
		args := append([]string{"boards", "list"}, flags...)
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "XDG_CACHE_HOME=" + cacheDir}, args...)
		var result struct {
			Data envelope.BoardsListOutput `json:"data"`
			Meta struct {
				Command string `json:"command"`
			} `json:"meta"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Meta.Command != "boards.list" {
			t.Fatal("live boards listing failed")
		}
		return result.Data
	}
	fresh := invoke("--refresh", "--all")
	if fresh.FromCache || !fresh.CacheSaved || fresh.Truncated || !fresh.Pagination.Complete || fresh.Count != len(expected) || fresh.Pagination.Total != len(expected) || len(fresh.ProjectFailures) != 0 || !reflect.DeepEqual(fresh.Boards, expected) {
		t.Fatal("fresh listing differs from independently drained boards/projects")
	}
	cached := invoke("--all")
	if !cached.FromCache || cached.FetchedAt != fresh.FetchedAt || !reflect.DeepEqual(cached.Boards, expected) {
		t.Fatal("fresh cache did not retain the full enriched snapshot")
	}
	window := invoke("--limit=1")
	if !window.FromCache || window.Count != 1 || window.Pagination.Total != len(expected) || window.Pagination.IsLast != (len(expected) == 1) || !reflect.DeepEqual(window.Boards, expected[:1]) {
		t.Fatal("display limit changed cache scope or fabricated a site-wide total")
	}
	frame, stdout, exit := r.invoke(t, t.Context(), nil, "auth", "status")
	var identity struct {
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &identity) != nil || strings.TrimSpace(identity.Data.Email) == "" {
		t.Fatal("verified CLI identity cannot select the owned cache namespace")
	}
	for _, preview := range []bool{true, false} {
		args := []string{"cache", "clear", "boards", "--no-input"}
		if preview {
			args = append(args, "--dry-run")
		} else {
			args = append(args, "--force")
		}
		// This mutation owns only this test's temporary cache. Disable Jira
		// credentials while keeping read-only mode to prove local isolation.
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "XDG_CACHE_HOME=" + cacheDir, "JIRA_EMAIL=" + identity.Data.Email, "JIRA_API_TOKEN=", "JIRA_KEYRING=false"}, args...)
		var result struct {
			Data envelope.CacheClearOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.DryRun != preview || result.Data.WouldRemove != 1 || len(result.Data.Entries) != 1 {
			t.Fatal("local clear did not inspect the native board snapshot without credentials")
		}
		if result.Data.Removed != 0 && preview || result.Data.Removed != 1 && !preview {
			t.Fatal("local clear fabricated a preview or removal receipt")
		}
		observed := invoke("--all")
		if observed.FromCache != preview || !reflect.DeepEqual(observed.Boards, expected) || !preview && !observed.CacheSaved {
			t.Fatal("local clear did not control subsequent native snapshot reuse")
		}
	}
}

func liveAssociatedKeys(t *testing.T, r *liveRun, id int64) []string {
	t.Helper()
	keys := []string{}
	path := "/rest/agile/1.0/board/" + strconv.FormatInt(id, 10) + "/project"
	for start := 0; start < 10000; {
		raw := rawRead(t, r, "api", "get", path, "--query=startAt="+strconv.Itoa(start), "--query=maxResults=50")
		var page struct {
			IsLast  *bool `json:"isLast"`
			StartAt int   `json:"startAt"`
			Values  []struct {
				Key string `json:"key"`
			} `json:"values"`
		}
		if json.Unmarshal(raw.Response.JSON, &page) != nil || page.IsLast == nil || page.Values == nil || page.StartAt != start {
			t.Fatal("raw project relation lacks pagination facts")
		}
		for _, project := range page.Values {
			if project.Key == "" {
				t.Fatal("raw project relation lacks a key")
			}
			keys = append(keys, project.Key)
		}
		if *page.IsLast {
			break
		}
		if len(page.Values) == 0 {
			t.Fatal("raw project walk cannot advance")
		}
		start += len(page.Values)
		if start >= 10000 {
			t.Fatal("project acceptance reached its bound without completion")
		}
	}
	xslices.SortNatural(keys)
	return slices.Compact(keys)
}
