//go:build outputaudit

package audit

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestInventoryReconcilesCleanAndExact is the T032 gate on the live
// tree: the join of command tree × op metadata × schema registry ×
// recipes must reconcile with ZERO problems, and today's whole surface
// must match the explicit surface pin — an exact-leaf inventory, not a
// containment check, so any new leaf red-flags itself until it is
// declared, registered, and covered.
func TestInventoryReconcilesCleanAndExact(t *testing.T) {
	members, problems := liveMembers()
	for _, p := range problems {
		t.Errorf("inventory: %s", p)
	}
	byOp := map[string]Member{}
	keys := make([]string, 0, len(members))
	for _, m := range members {
		byOp[m.Meta.Op] = m
		keys = append(keys, m.Key())
	}
	// The count is pinned so a new surface cannot appear unnoticed: adding
	// a command means coming here and stating that it was intended, which
	// is the moment its scenarios, schema and metadata get checked.
	// Names, not just a count: a swap (one surface removed, another
	// added) keeps the count and is exactly the drift the pin exists to
	// catch.
	wantKeys := []string{
		"jira adf convert|", "jira adf edit apply|", "jira adf edit merge|", "jira adf edit prepare|", "jira adf explain-node|", "jira adf lint|", "jira adf render|", "jira adf validate|",
		"jira agent adf-matrix|", "jira agent export|", "jira agent fieldtypes|", "jira agent guide|", "jira agent schema|",
		"jira alias delete|", "jira alias import|", "jira alias list|", "jira alias set|",
		"jira api delete|", "jira api get|", "jira api head|", "jira api options|",
		"jira api patch|", "jira api post|", "jira api put|",
		"jira auth login|", "jira auth logout|", "jira auth status|", "jira auth token|", "jira auth whoami|", "jira auth whoami|--save",
		"jira boards list|",
		"jira cache boards|", "jira cache clear|", "jira cache epics|", "jira cache fields|",
		"jira cache inspect|", "jira cache issuekeys|", "jira cache issuetypes|", "jira cache labels|", "jira cache linktypes|", "jira cache priorities|",
		"jira cache projects|", "jira cache refresh|", "jira cache resolutions|", "jira cache statuses|",
		"jira completion|", "jira config edit|", "jira config get|", "jira config init|", "jira config list|", "jira config migrate|", "jira config profile add|", "jira config profile default|", "jira config profile list|", "jira config profile remove|", "jira config profile rename|", "jira config rollback|", "jira config set|", "jira config theme|",
		"jira epic add|", "jira epic board|", "jira epic list|", "jira epic remove|",
		"jira guide|",
		"jira issue attachment add|", "jira issue attachment delete|",
		"jira issue attachment download|", "jira issue attachment list|",
		"jira issue clone|",
		"jira issue comment add|", "jira issue comment delete|", "jira issue comment edit|",
		"jira issue comment list|",
		"jira issue create|", "jira issue create|--generate-json", "jira issue delete|", "jira issue edit draft|", "jira issue edit inspect|", "jira issue edit prepare|", "jira issue edit resume|", "jira issue edit|", "jira issue edit|--generate-json",
		"jira issue input prepare|",
		"jira issue link create|", "jira issue link delete|", "jira issue link list|",
		"jira issue link types|",
		"jira issue list|", "jira issue mine|", "jira issue move fields|", "jira issue move status|", "jira issue move|", "jira issue rank|", "jira issue transition list|", "jira issue transition|", "jira issue view|",
		"jira issue watchers add|", "jira issue watchers list|", "jira issue watchers remove|",
		"jira issue weblink add|", "jira issue weblink delete|", "jira issue weblink list|",
		"jira jql build|", "jira jql reference|", "jira jql validate|",
		"jira me|",
		"jira open|",
		"jira release-notes|",
		"jira update|",
		"jira user search|", "jira version|", "jira worklog add|", "jira worklog list|", "jira|",
	}
	if gotKeys := slices.Sorted(slices.Values(keys)); !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("inventory members = %v, want %v", gotKeys, wantKeys)
	}
	if len(members) != len(wantKeys) {
		t.Fatalf("inventory has %d members [%s], want the %d declared surfaces; extend the exact pin deliberately for new commands", len(members), strings.Join(keys, ", "), len(wantKeys))
	}
	if m, ok := byOp["root"]; !ok || m.Meta.NoSuccessEnvelope != "" || !m.Meta.Passthrough || m.Meta.Class() != command.ClassRead || m.Meta.Args == nil || m.Meta.Args.Min != 0 || m.Meta.Args.Max != 0 {
		t.Errorf("root member = %+v, want a typed read with schema passthrough and no positional arguments", m.Meta)
	}
	if m, ok := byOp["completion"]; !ok || m.Meta.NoSuccessEnvelope == "" {
		t.Errorf("completion member = %+v, want declared envelope-free (script surface)", m.Meta)
	}
	if m, ok := byOp["version"]; !ok || m.Meta.NoSuccessEnvelope != "" {
		t.Errorf("version member = %+v, want the envelope-bearing read", m.Meta)
	}
}

// TestInventoryDerivesOpsFromMetadataNotShape pins the mechanism the
// design demands: the member's op comes from the declaration, not from
// the command path — the bare root's path is "jira" while its op is
// "root", which no tree walk could invent.
func TestInventoryDerivesOpsFromMetadataNotShape(t *testing.T) {
	members, _ := liveMembers()
	if len(members) == 0 {
		t.Fatal("no members")
	}
	if members[0].Path == members[0].Meta.Op {
		t.Errorf("member path %q equals op %q — ops must come from metadata, and this pin loses meaning", members[0].Path, members[0].Meta.Op)
	}
}

// TestHasRefSlotContainers pins the container coverage the doc claims:
// a ref inside a slice, array, or map counts as an identity slot.
func TestHasRefSlotContainers(t *testing.T) {
	t.Parallel()
	type inSlice struct {
		Issues []envelope.IssueRef `json:"issues"`
	}
	type inMap struct {
		ByKey map[string]envelope.IssueRef `json:"by_key"`
	}
	type inArray struct {
		Pair [2]envelope.IssueRef `json:"pair"`
	}
	for name, sample := range map[string]any{
		"slice": inSlice{}, "map": inMap{}, "array": inArray{},
	} {
		if !hasRefSlot(reflect.TypeOf(sample)) {
			t.Errorf("%s container: ref not found", name)
		}
	}
}
