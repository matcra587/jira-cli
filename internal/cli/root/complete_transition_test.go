package root

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestTransitionCompletionUsesEligibleIssueHintsAndExplicitRoles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("JIRA_BASE_URL", "https://example.com")
	t.Setenv("JIRA_EMAIL", "person@example.com")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_KEYRING", "false")
	store, err := newScopedDiskCache("https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordIssueKeys(store, []string{"DEMO-1", "DEMO-2"}); err != nil {
		t.Fatal(err)
	}
	var list jira.Transitions
	if err := jsonv2.Unmarshal([]byte(`{"transitions":[{"id":"31","name":"Finish","hasScreen":false,"to":{"id":"10020","name":"Done"}},{"id":"11","name":"Start","hasScreen":false,"isAvailable":false}]}`), &list); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DEMO-1", "DEMO-2"} {
		if err := cachecmd.RecordTransitionHints(t.Context(), store, key, &list, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, kind string
		args       []string
		want       []string
	}{
		{name: "first issue key", kind: "transitionissuekey", want: []string{"DEMO-1", "DEMO-2"}},
		{name: "positional target", kind: "transitionissuekey", args: []string{"DEMO-1"}, want: []string{"31\tFinish (Done)"}},
		{name: "target callback", kind: "cachetransition", args: []string{"DEMO-1"}, want: []string{"31\tFinish (Done)"}},
		{name: "shared target", kind: "cachetransition", args: []string{"DEMO-1", "DEMO-2"}, want: []string{"31\tFinish (Done)"}},
		{name: "explicit target leaves issue slot", kind: "transitionissuekey", args: []string{"DEMO-1", "--transition=31"}, want: []string{"DEMO-1", "DEMO-2"}},
		{name: "unknown issue", kind: "cachetransition", args: []string{"DEMO-9"}},
		{name: "too many issue contexts", kind: "cachetransition", args: slices.Repeat([]string{"DEMO-1"}, 51)},
		{name: "selector has no explicit workflow", kind: "cachetransition", args: []string{"--jql=project = DEMO"}},
		{name: "native input leaves target unknown", kind: "transitionissuekey", args: []string{"DEMO-1", "--json-input=payload.json"}},
		{name: "prior target is not another key", kind: "transitionissuekey", args: []string{"DEMO-1", "Done"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := completionKinds[tc.kind]
			if fn == nil {
				t.Fatalf("missing eligible transition predictor %s", tc.kind)
			}
			if got := fn(tc.args); !slices.Equal(got, tc.want) {
				t.Fatalf("contextual transition hints=%q want=%q", got, tc.want)
			}
		})
	}
	t.Setenv("JIRA_EMAIL", "other@example.com")
	if got := completionKinds["transitionissuekey"]([]string{"DEMO-1"}); len(got) != 0 {
		t.Fatal("another account reused stored transition hints")
	}
}

func TestTransitionCompletionRejectsUnsafeOrConflictingObservations(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("JIRA_BASE_URL", "https://example.com")
	t.Setenv("JIRA_EMAIL", "person@example.com")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_KEYRING", "false")
	store, err := newScopedDiskCache("https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	const good = `{"id":"31","name":"Finish","status_id":"10020","status":"Done"}`
	for _, tc := range []struct {
		name, hints, key, observed string
		multiple                   bool
	}{
		{name: "stale", hints: good, observed: time.Now().Add(-time.Hour).Format(time.RFC3339Nano)},
		{name: "future", hints: good, observed: time.Now().Add(time.Hour).Format(time.RFC3339Nano)},
		{name: "cross issue", hints: good, key: "DEMO-9"},
		{name: "malformed", hints: `{`},
		{name: "different identity", hints: strings.ReplaceAll(good, `"31"`, `"32"`), multiple: true},
		{name: "different destination", hints: strings.ReplaceAll(good, `"10020"`, `"10021"`), multiple: true},
		{name: "different name", hints: strings.ReplaceAll(good, `"Finish"`, `"Close"`), multiple: true},
		{name: "different destination name", hints: strings.ReplaceAll(good, `"Done"`, `"Closed"`), multiple: true},
		{name: "unknown destination", hints: strings.ReplaceAll(good, `"10020"`, `""`), multiple: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			write := func(key, binding, observed, hints string) {
				raw := fmt.Sprintf(`{"version":1,"issue_key":%q,"observed_at":%q,"hints":[%s]}`, binding, observed, hints)
				if err := store.Set(cachecmd.TransitionHintsPrefix+key, []byte(raw)); err != nil {
					t.Fatal(err)
				}
			}
			write("DEMO-1", "DEMO-1", now, good)
			key, binding := "DEMO-1", "DEMO-1"
			args := []string{"DEMO-1"}
			if tc.multiple {
				key, binding = "DEMO-2", "DEMO-2"
				args = append(args, key)
			}
			if tc.key != "" {
				binding = tc.key
			}
			if tc.observed != "" {
				now = tc.observed
			}
			write(key, binding, now, tc.hints)
			if got := completionKinds["cachetransition"](args); len(got) != 0 {
				t.Fatalf("unsafe completion accepted: %q", got)
			}
		})
	}
}
