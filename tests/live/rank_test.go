//go:build live

package live

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type ownedRankInput struct {
	issues           []string
	position, anchor string
}

func (r *liveRun) allowRank(issues []string, position, anchor string) {
	r.rankInputs = append(r.rankInputs, ownedRankInput{issues: slices.Clone(issues), position: position, anchor: anchor})
}

// Ranking binds the ordered sources and anchor to active ownership and one
// authored intent. It cannot select, resolve or change an indirect target.
func (r *liveRun) checkRankMutation(keys, args []string) error {
	if !r.verified || len(args) < 4 || args[0] != "issue" || args[1] != "rank" {
		return errors.New("ranking requires verified explicit owned targets")
	}
	i := 2
	seenIssues := map[string]bool{}
	for i < len(args) && !strings.HasPrefix(args[i], "--") {
		if seenIssues[args[i]] || !liveKey.MatchString(args[i]) || r.ownedLiveIssue(args[i]) == nil {
			return errors.New("ranking has repeated, inactive or foreign sources")
		}
		seenIssues[args[i]] = true
		i++
	}
	issues := args[2:i]
	if len(issues) == 0 || len(issues) > 50 {
		return errors.New("ranking requires between one and fifty authored sources")
	}
	var position, anchor string
	preview := false
	for ; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		if flag == "--dry-run" {
			if inline || preview {
				return errors.New("ranking has an unauthored preview option")
			}
			preview = true
			continue
		}
		if (flag != "--before" && flag != "--after") || position != "" {
			return errors.New("ranking has unauthored or repeated options")
		}
		if !inline {
			i++
			if i == len(args) {
				return errors.New("ranking omits its anchor value")
			}
			value = args[i]
		}
		position, anchor = strings.TrimPrefix(flag, "--"), value
	}
	if !liveKey.MatchString(anchor) || r.ownedLiveIssue(anchor) == nil || seenIssues[anchor] {
		return errors.New("ranking has an inactive, foreign or source anchor")
	}
	declared := append(slices.Clone(issues), anchor)
	if !slices.Equal(keys, declared) {
		return errors.New("ranking sources or anchor differ from their ownership declaration")
	}
	for _, input := range r.rankInputs {
		if input.position == position && input.anchor == anchor && slices.Equal(input.issues, issues) {
			return nil
		}
	}
	return errors.New("ranking differs from its registered authored intent")
}

func rankArgs(issues []string, position, anchor string) []string {
	args := append([]string{"issue", "rank"}, issues...)
	return append(args, "--"+position+"="+anchor)
}

// Compare complete native and domain search snapshots twice, repeating only
// reads to accommodate index visibility after issue creation or ranking.
func waitRankOrder(t *testing.T, r *liveRun, query string, owned, want []string) []string {
	t.Helper()
	stable := 0
	var previous []string
	for range 20 {
		native := rawEpicSearch(t, r, query, "summary")
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "list", "--jql", query, "--fields=summary", "--all")
		var domain struct {
			Data envelope.IssueListOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &domain) != nil || domain.Data.Truncated || !domain.Data.Pagination.IsLast {
			t.Fatal("ranking order read has no complete domain observation")
		}
		keys := make([]string, len(native))
		for i, row := range native {
			issue := r.ownedLiveIssue(row.Key)
			if issue == nil || !slices.Contains(owned, row.Key) || row.Bean.ID.Value != issue.ID {
				t.Fatal("ranking order observation contains foreign or changed identity")
			}
			keys[i] = row.Key
		}
		matched := len(keys) == len(owned) && len(domain.Data.Issues) == len(keys)
		for i, issue := range domain.Data.Issues {
			if i >= len(keys) || issue.Key != keys[i] || issue.ID != native[i].Bean.ID.Value {
				matched = false
			}
		}
		if want != nil && !slices.Equal(keys, want) {
			matched = false
		}
		if matched && slices.Equal(keys, previous) {
			stable++
		} else {
			stable = 0
		}
		previous = keys
		if stable == 1 {
			return keys
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("ranking order verification canceled")
		case <-timer.C:
		}
	}
	t.Fatal("ranking order did not reach two matching independent observations")
	return nil
}

func TestLiveRank(t *testing.T) {
	r := newLiveRun(t)
	boardID, err := strconv.ParseInt(requiredEnv(t, "JIRA_LIVE_BOARD_ID"), 10, 64)
	if err != nil || boardID <= 0 {
		t.Fatal("live board fixture requires a positive ID")
	}
	config := rawRead(t, r, "api", "get", "/rest/agile/1.0/board/"+strconv.FormatInt(boardID, 10)+"/configuration")
	var board struct {
		Filter struct {
			ID string `json:"id"`
		} `json:"filter"`
		Ranking struct {
			Field int64 `json:"rankCustomFieldId"`
		} `json:"ranking"`
	}
	if json.Unmarshal(config.Response.JSON, &board) != nil || board.Ranking.Field <= 0 {
		t.Fatal("the designated board has no visible filter or rank field")
	}
	filterID, err := strconv.ParseInt(board.Filter.ID, 10, 64)
	if err != nil || filterID <= 0 {
		t.Fatal("the designated board has no positive saved filter identity")
	}
	keys := []string{r.create(t, "rank first"), r.create(t, "rank second"), r.create(t, "rank anchor")}
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = strconv.Quote(key)
	}
	query := "filter = " + strconv.FormatInt(filterID, 10) + " AND key in (" + strings.Join(quoted, ",") + ") ORDER BY cf[" + strconv.FormatInt(board.Ranking.Field, 10) + "] ASC"
	baseline := waitRankOrder(t, r, query, keys, nil)
	anchor := baseline[0]
	moving := slices.Clone(baseline[1:])
	for _, position := range []string{"before", "after"} {
		if position == "after" {
			slices.Reverse(moving)
		}
		r.allowRank(moving, position, anchor)
		declared := append(slices.Clone(moving), anchor)
		args := rankArgs(moving, position, anchor)
		if err := r.checkMutation(declared, args); err != nil {
			t.Fatal(err)
		}
		preview, stdout, exit := r.invoke(t, t.Context(), nil, append(slices.Clone(args), "--dry-run")...)
		var result struct {
			Data envelope.IssueRankOutput `json:"data"`
		}
		if exit != 0 || !preview.OK || json.Unmarshal(stdout, &result) != nil || !result.Data.DryRun || result.Data.Anchor != anchor || result.Data.Position != position || len(result.Data.Results) != len(moving) {
			t.Fatal("ranking preview lost the authored move")
		}
		for i, row := range result.Data.Results {
			if row.Issue.Key != moving[i] || !row.OK || row.Data == nil || row.Err != nil {
				t.Fatal("ranking preview changed the authored source order or acknowledgement")
			}
		}
		waitRankOrder(t, r, query, keys, baseline)
		refused, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
		if exit != 3 || refused.OK || len(refused.Errors) != 1 || refused.Errors[0].Code != "read_only_blocked" {
			t.Fatal("ranking read-only policy did not refuse the write")
		}
		waitRankOrder(t, r, query, keys, baseline)
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.DryRun || result.Data.Anchor != anchor || result.Data.Position != position || len(result.Data.Results) != len(moving) {
			t.Fatal("ranking lost the live ordered request")
		}
		for i, row := range result.Data.Results {
			if row.Issue.Key != moving[i] || !row.OK || row.Data == nil || row.Err != nil {
				t.Fatal("ranking failed to acknowledge each authored move in input order")
			}
		}
		want := append(slices.Clone(moving), anchor)
		if position == "after" {
			want = append([]string{anchor}, moving...)
		}
		baseline = waitRankOrder(t, r, query, keys, want)
	}
}
