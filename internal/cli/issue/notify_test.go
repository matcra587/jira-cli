package issue

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// --notify's flag mechanics are pinned in internal/cli/command
// (notify_test.go). What is pinned HERE is that the resolved value
// actually reaches the two places it has to: the request, and the
// envelope. A flag that resolves correctly and is then dropped on the
// floor is the failure these commands are most likely to have, because
// notifyUsers rides the QUERY rather than the request body — so it is
// invisible in the published payload and nothing else would notice.

// TestEditSendsNotifyToTheWire. The value must reach EditIssue, or
// --notify=false is a flag that parses and does nothing — which is worse
// than an absent one, because an agent reading the schema will pass it
// and believe the emails stopped.
func TestEditSendsNotifyToTheWire(t *testing.T) {
	for _, tt := range []struct {
		name   string
		notify bool
	}{
		{name: "default notifies", notify: true},
		{name: "suppressed", notify: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeWriteClient{}
			_, err := runEdit(t.Context(), writeDeps(fake), invocation(false, "KEY-1"),
				writeFlagsFor(t, command.EditIssueVerb, "--summary", "New"),
				editOptions{parallelism: 1, notify: tt.notify})
			if err != nil {
				t.Fatalf("runEdit: %v", err)
			}
			if len(fake.edited) != 1 {
				t.Fatalf("edits = %d, want 1", len(fake.edited))
			}
			if got := fake.edited[0].notify; got != tt.notify {
				t.Errorf("EditIssue notifyUsers = %v, want %v — the flag resolved but never reached the request", got, tt.notify)
			}
		})
	}
}

// TestEditPublishesWhatWasAskedFor. The envelope's `notify` is the
// REQUEST, and it rides both paths for the reason delete_subtasks does:
// a caller auditing what a script did has to see which request was made.
//
// It matters more here than for most fields, because Jira IGNORES a
// suppression request from a caller without administer permission and
// says nothing about having done so. This field is therefore the only
// record that silence was ever asked for — it can never be a record that
// silence happened, which is what the field's own doc and the command's
// help both say in those terms.
func TestEditPublishesWhatWasAskedFor(t *testing.T) {
	fake := &fakeWriteClient{}
	out, err := runEdit(t.Context(), writeDeps(fake), invocation(false, "KEY-1"),
		writeFlagsFor(t, command.EditIssueVerb, "--summary", "New"),
		editOptions{parallelism: 1, notify: false})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	if out.Notify {
		t.Error("envelope notify = true after --notify=false")
	}

	preview, err := runEdit(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true, "KEY-1"),
		writeFlagsFor(t, command.EditIssueVerb, "--summary", "New"),
		editOptions{parallelism: 1, notify: false})
	if err != nil {
		t.Fatalf("runEdit --dry-run: %v", err)
	}
	if preview.Notify {
		t.Error("preview published notify = true after --notify=false; the preview must describe the request a live run would send")
	}
}

// TestCommentEditSendsNotifyToTheWire is the same guard on the other
// command. Its endpoint carries no permission caveat, so here a false
// really does mean the watchers are not emailed.
func TestCommentEditSendsNotifyToTheWire(t *testing.T) {
	fake := &fakeCommentEditClient{}
	out, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(false, "KEY-123", "10042"),
		commentEditOptions{body: bodyFromADF(t, adfBody), notify: false})
	if err != nil {
		t.Fatalf("runCommentEdit: %v", err)
	}
	if len(fake.edits) != 1 {
		t.Fatalf("edits = %d, want 1", len(fake.edits))
	}
	if fake.edits[0].notify {
		t.Error("UpdateComment notifyUsers = true after --notify=false")
	}
	if out.Notify {
		t.Error("envelope notify = true after --notify=false")
	}
}

// TestNotifyDefaultsTrueEndToEnd guards the direction that would be
// silent. A wrong default here does not fail anything — it just quietly
// stops notifying everyone, which nobody would report as a bug against
// this CLI and which would be found weeks later as "Jira stopped sending
// us email".
func TestNotifyDefaultsTrueEndToEnd(t *testing.T) {
	cmd := newEdit(Deps{})
	notify, err := cmd.Flags().GetBool(command.FlagNotify)
	if err != nil {
		t.Fatalf("issue edit has no --%s: %v", command.FlagNotify, err)
	}
	if !notify {
		t.Errorf("issue edit --%s defaults false; every edit would silently stop notifying watchers", command.FlagNotify)
	}

	cmd = newCommentEdit(Deps{})
	notify, err = cmd.Flags().GetBool(command.FlagNotify)
	if err != nil {
		t.Fatalf("issue comment edit has no --%s: %v", command.FlagNotify, err)
	}
	if !notify {
		t.Errorf("issue comment edit --%s defaults false", command.FlagNotify)
	}
}
