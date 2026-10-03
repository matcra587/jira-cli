//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// TestLiveBoardPages checks the typed adapter against independent raw reads
// through the frozen binary. Both board and associated-project endpoints are
// read-only. No board, project, filter or issue is created or changed.
func TestLiveBoardPages(t *testing.T) {
	r := newLiveRun(t)
	boardID, err := strconv.ParseInt(requiredEnv(t, "JIRA_LIVE_BOARD_ID"), 10, 64)
	if err != nil || boardID <= 0 {
		t.Fatal("live board fixture must have a positive identity")
	}
	resolved, err := config.Load(config.Options{Credentials: keyring.New(), Environ: func() []string {
		return append(os.Environ(), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "JIRA_READ_ONLY=true")
	}})
	// Check the configured host before constructing the adapter or sending any
	// credentials. The harness independently preflights this existing keyring.
	if err != nil || resolved.Config.BaseURL != r.manifest.BaseURL || resolved.Source("api_token") != config.LayerKeyring {
		t.Fatal("typed live reads require the preflighted keyring and configured host")
	}
	if resolved.Config.Validate() != nil {
		t.Fatal("typed live configuration is incomplete")
	}
	client, err := adapterjira.New(adapterjira.Config{
		BaseURL: resolved.Config.BaseURL, Email: resolved.Config.Email, APIToken: resolved.Config.APIToken,
		ReadOnly: true, Timeout: 30 * time.Second, Retry: core.DefaultRetryPolicy(),
	})
	if err != nil {
		t.Fatal("cannot construct the read-only live adapter")
	}
	foundBoard, foundProject := false, false
	for start := 0; start < 10000; {
		page, err := client.GetBoards(t.Context(), "", start, 50)
		if err != nil || page.Page == nil {
			t.Fatalf("typed board read failed: %v", err)
		}
		raw := rawRead(t, r, "api", "get", "/rest/agile/1.0/board", "--query=startAt="+strconv.Itoa(start), "--query=maxResults=50")
		assertLiveAgilePageEqual(t, raw.Response.JSON, page.Page)
		for _, board := range page.Page.Values.Value {
			foundBoard = foundBoard || board.ID.Value == boardID
		}
		if page.Page.IsLast.Value {
			break
		}
		start += len(page.Page.Values.Value)
		if start >= 10000 {
			t.Fatal("board read exceeded acceptance bound without completion")
		}
	}
	if !foundBoard {
		t.Fatal("configured board fixture not visible; board acceptance is unproven")
	}
	path := "/rest/agile/1.0/board/" + strconv.FormatInt(boardID, 10) + "/project"
	for start := 0; start < 10000; {
		page, err := client.GetBoardProjects(t.Context(), boardID, start, 50)
		if err != nil || page.Page == nil {
			t.Fatalf("typed associated-project read failed: %v", err)
		}
		raw := rawRead(t, r, "api", "get", path, "--query=startAt="+strconv.Itoa(start), "--query=maxResults=50")
		assertLiveAgilePageEqual(t, raw.Response.JSON, page.Page)
		for _, project := range page.Page.Values.Value {
			foundProject = foundProject || project.Key.Value == r.manifest.Project
		}
		if page.Page.IsLast.Value {
			break
		}
		start += len(page.Page.Values.Value)
		if start >= 10000 {
			t.Fatal("project read exceeded acceptance bound without completion")
		}
	}
	if !foundProject {
		t.Fatal("configured project not associated with the board; project acceptance is unproven")
	}
	for _, start := range []int{0, 1} {
		page, err := client.GetBoards(t.Context(), "", start, 1)
		if err != nil || page.Page == nil {
			t.Fatalf("bounded board read failed: %v", err)
		}
		raw := rawRead(t, r, "api", "get", "/rest/agile/1.0/board", "--query=startAt="+strconv.Itoa(start), "--query=maxResults=1")
		assertLiveAgilePageEqual(t, raw.Response.JSON, page.Page)
		projects, err := client.GetBoardProjects(t.Context(), boardID, start, 1)
		if err != nil || projects.Page == nil {
			t.Fatalf("bounded project read failed: %v", err)
		}
		raw = rawRead(t, r, "api", "get", path, "--query=startAt="+strconv.Itoa(start), "--query=maxResults=1")
		assertLiveAgilePageEqual(t, raw.Response.JSON, projects.Page)
	}
}

func assertLiveAgilePageEqual(t *testing.T, raw []byte, typed any) {
	t.Helper()
	encoded, err := jira.MarshalNoEscape(typed)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	for i, body := range [][]byte{raw, encoded} {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			before = value
		} else {
			after = value
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("typed Agile page lost or changed raw members, presence or values")
	}
}
