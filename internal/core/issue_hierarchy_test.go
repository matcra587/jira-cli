package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

type hierarchyReader struct {
	read  func(context.Context, string) (*jira.IssueBean, error)
	calls []string
}

func (c *hierarchyReader) GetIssue(ctx context.Context, key string, fields []string, _ ...string) (IssueRead, error) {
	if !slices.Equal(fields, []string{"summary", "issuetype", "parent"}) {
		return IssueRead{}, errors.New("unexpected hierarchy field selection")
	}
	c.calls = append(c.calls, key)
	bean, err := c.read(ctx, key)
	return IssueRead{Issue: bean}, err
}

func hierarchyBean(t *testing.T, id, key, parent string) *jira.IssueBean {
	t.Helper()
	var bean jira.IssueBean
	data := fmt.Sprintf(`{"id":%q,"key":%q,"fields":{"summary":"Native issue","issuetype":{"name":"Renamed type"}%s}}`, id, key, parent)
	if err := json.Unmarshal([]byte(data), &bean); err != nil {
		t.Fatal(err)
	}
	return &bean
}

func TestReadHierarchyPrefersIDsAndPreservesParentKnowledge(t *testing.T) {
	for _, terminal := range []struct {
		parent   string
		complete bool
		reason   string
	}{
		{parent: `,"parent":null`, complete: true},
		{parent: "", reason: "parent_not_returned"},
	} {
		t.Run(terminal.reason+terminal.parent, func(t *testing.T) {
			beans := map[string]*jira.IssueBean{
				"STORY-1": hierarchyBean(t, "10", "STORY-1", `,"parent":{"id":"20","key":"OLD-2"}`),
				"20":      hierarchyBean(t, "20", "EPIC-2", `,"parent":{"id":"30","key":"PLAN-3"}`),
				"30":      hierarchyBean(t, "30", "PLAN-3", terminal.parent),
			}
			client := &hierarchyReader{read: func(_ context.Context, key string) (*jira.IssueBean, error) { return beans[key], nil }}
			read, err := ReadIssueHierarchy(t.Context(), client, IssueHierarchyReference{Key: "STORY-1"})
			if err != nil || len(read.Nodes) != 3 || read.Complete != terminal.complete || read.Reason != terminal.reason || !slices.Equal(client.calls, []string{"STORY-1", "20", "30"}) {
				t.Fatalf("native ancestors were lost or guessed: %+v %v %v", read, err, client.calls)
			}
			if read.Nodes[1].Key != "EPIC-2" || read.Nodes[2].Type != "Renamed type" {
				t.Fatal("renamed native identity or type lost")
			}
		})
	}
}

func TestHierarchyRefusesCyclesIdentityChangesAndMalformedParents(t *testing.T) {
	for _, parent := range []string{`{"id":"1"}`, `{}`, `[]`, `"key"`, `{"id":42}`, `{"id":" "}`, `{"key":"bad\nkey"}`} {
		t.Run(parent, func(t *testing.T) {
			client := &hierarchyReader{read: func(_ context.Context, _ string) (*jira.IssueBean, error) {
				return hierarchyBean(t, "1", "DEMO-1", `,"parent":`+parent), nil
			}}
			_, err := ReadIssueHierarchy(t.Context(), client, IssueHierarchyReference{ID: "1"})
			if !errors.Is(err, ErrIssueHierarchyInvalid) || len(client.calls) != 1 {
				t.Fatalf("invalid parent accepted: %v %v", err, client.calls)
			}
		})
	}
	client := &hierarchyReader{read: func(_ context.Context, _ string) (*jira.IssueBean, error) {
		return hierarchyBean(t, "2", "DEMO-2", `,"parent":null`), nil
	}}
	if _, err := ReadIssueHierarchy(t.Context(), client, IssueHierarchyReference{ID: "1"}); !errors.Is(err, ErrIssueHierarchyInvalid) {
		t.Fatal("changed immutable identity accepted", err)
	}
	client = &hierarchyReader{read: func(_ context.Context, key string) (*jira.IssueBean, error) {
		if key == "1" {
			return hierarchyBean(t, "1", "DEMO-1", `,"parent":{"key":"DEMO-2"}`), nil
		}
		return hierarchyBean(t, "1", "DEMO-1", `,"parent":null`), nil
	}}
	if _, err := ReadIssueHierarchy(t.Context(), client, IssueHierarchyReference{ID: "1"}); !errors.Is(err, ErrIssueHierarchyInvalid) || len(client.calls) != 2 {
		t.Fatal("key-only cycle accepted", err)
	}
}

func TestHierarchyBoundsReadsAndRetainsDeniedObservations(t *testing.T) {
	denied := errors.New("ancestor permission denied")
	client := &hierarchyReader{read: func(_ context.Context, key string) (*jira.IssueBean, error) {
		if key == "2" {
			return nil, denied
		}
		return hierarchyBean(t, "1", "DEMO-1", `,"parent":{"id":"2"}`), nil
	}}
	read, err := ReadIssueHierarchy(t.Context(), client, IssueHierarchyReference{ID: "1"})
	if !errors.Is(err, denied) || read.Complete || len(read.Nodes) != 1 || read.Reason != "ancestor_unavailable" {
		t.Fatalf("denial became absent parent: %+v %v", read, err)
	}
	n := 0
	client = &hierarchyReader{read: func(_ context.Context, key string) (*jira.IssueBean, error) {
		n++
		return hierarchyBean(t, key, "DEMO-"+key, fmt.Sprintf(`,"parent":{"id":"%d"}`, n+1)), nil
	}}
	read, err = ReadIssueHierarchy(t.Context(), client, IssueHierarchyReference{ID: "1"})
	if err != nil || read.Complete || read.Reason != "depth_limit" || n != MaxIssueHierarchyDepth {
		t.Fatalf("ancestor bound not enforced: %+v %v %d", read, err, n)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client.calls = nil
	if _, err = ReadIssueHierarchy(ctx, client, IssueHierarchyReference{ID: "1"}); !errors.Is(err, context.Canceled) || len(client.calls) != 0 {
		t.Fatal("canceled hierarchy made a read", err)
	}
}
