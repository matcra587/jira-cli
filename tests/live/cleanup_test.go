//go:build live

package live

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The test executable serves as a hermetic CLI fixture. TestMain handles this
// mode before Go parses the appended Jira arguments; it never contacts Jira.
func TestMain(m *testing.M) {
	if os.Getenv("JIRA_LIVE_CLEANUP_HELPER") == "1" {
		args := os.Args[1:]
		if len(args) >= 5 && args[0] == "issue" && args[1] == "move" && args[2] == "status" {
			end := slices.Index(args, "--")
			if end < 0 || end+2 != len(args) || !slices.Contains(args[:end], "--output=json") || !slices.Contains(args[:end], "--no-input") || !slices.Contains(args[:end], "--timeout=30s") {
				os.Exit(3)
			}
			body, err := json.Marshal(map[string]any{"ok": true, "data": map[string]any{"task": map[string]any{"id": args[end+1]}, "terminal": true, "progress": map[string]any{"taskId": args[end+1], "status": "COMPLETE"}}})
			if err != nil {
				os.Exit(8)
			}
			if _, err := os.Stdout.Write(body); err != nil {
				os.Exit(8)
			}
			os.Exit(0)
		}
		if len(os.Args) < 4 || os.Args[1] != "issue" || os.Args[3] != "KEY-123" {
			os.Exit(3)
		}
		var response string
		exit := 0
		switch os.Args[2] {
		case "delete":
			response = `{"ok":true,"data":{"results":[{"issue":{"key":"KEY-123"},"ok":true}]}}`
		case "view":
			response = `{"ok":false,"data":null,"errors":[{"code":"jira_not_found"}]}`
			exit = 2
		default:
			os.Exit(3)
		}
		if _, err := os.Stdout.WriteString(response); err != nil {
			os.Exit(8)
		}
		os.Exit(exit)
	}
	os.Exit(m.Run())
}

func TestLiveInvokeKeepsPolicyFlagsBeforePositionalTerminator(t *testing.T) {
	t.Setenv("JIRA_LIVE_CLEANUP_HELPER", "1")
	r := &liveRun{binary: os.Args[0], dir: t.TempDir()}
	frame, raw, exit := r.invoke(t, t.Context(), nil, "issue", "move", "status", "--wait", "--", "--help")
	var out struct {
		Data struct {
			Task struct{ ID string }
		}
	}
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &out) != nil || out.Data.Task.ID != "--help" {
		t.Fatal("opaque task or harness policy flags were parsed as positional arguments")
	}
}

func TestLiveCleanupSettlesMoveBeforeDeleting(t *testing.T) {
	t.Setenv("JIRA_LIVE_CLEANUP_HELPER", "1")
	r := &liveRun{
		binary: os.Args[0], dir: t.TempDir(),
		manifest: liveManifest{
			Issues:    []ownedIssue{{Key: "KEY-123", ID: "10001"}},
			BulkTasks: []ownedBulkTask{{ID: "100", Keys: []string{"KEY-123"}}},
		},
	}
	t.Run("owned-fixture", func(t *testing.T) {
		t.Cleanup(func() { r.cleanup(t) })
	})
	if !r.manifest.BulkTasks[0].Terminal || !r.manifest.Issues[0].Deleted || r.sequence != 3 {
		t.Fatal("cleanup did not settle the move before deleting and independently verifying its owned fixture")
	}
}

func TestLiveCleanupOutlivesTestContext(t *testing.T) {
	t.Setenv("JIRA_LIVE_CLEANUP_HELPER", "1")
	r := &liveRun{
		binary: os.Args[0], dir: t.TempDir(),
		manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-123", ID: "10001", Comments: []ownedComment{{ID: "20001"}}, Attachments: []ownedAttachment{{ID: "30001"}}}}},
	}
	t.Run("owned-fixture", func(t *testing.T) {
		t.Cleanup(func() { r.cleanup(t) })
	})
	if !r.manifest.Issues[0].Deleted || r.sequence != 2 {
		t.Fatal("cleanup did not delete and verify its owned fixture after the test context was canceled")
	}
	if !r.manifest.Issues[0].Comments[0].Deleted {
		t.Fatal("confirmed issue removal did not close its owned comment resources")
	}
	if !r.manifest.Issues[0].Attachments[0].Deleted {
		t.Fatal("confirmed issue removal did not close its owned attachment resources")
	}
}
