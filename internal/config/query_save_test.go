package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedQueryCreationUsesNoClobberOwnerAndRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "queries")
	const query = `project = DEMO AND summary ~ "quotes" ORDER BY updated DESC`
	created, err := SaveQuery(t.Context(), dir, "My view", query)
	if err != nil || !created {
		t.Fatalf("saved query was not published: %t %v", created, err)
	}
	loaded, err := LoadQueries(t.Context(), dir)
	if err != nil || loaded["My view"].JQL != query {
		t.Fatalf("saved JQL changed: %v %v", loaded, err)
	}
	if created, err := SaveQuery(t.Context(), dir, "My view", "different"); err == nil || created {
		t.Fatal("saving replaced an existing search")
	}
	bytes, err := os.ReadFile(filepath.Join(dir, "My view.jql"))
	if err != nil || string(bytes) != query+"\n" {
		t.Fatal("collision changed existing query bytes")
	}
}

func TestSavedQueryRefusesUnsafeNameInvalidBodyAndCanceledPublication(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	for _, test := range []struct{ name, query string }{{"../escape", "project = DEMO"}, {"blank", " "}, {"controls", "project = DEMO\x1b"}} {
		if changed, err := SaveQuery(t.Context(), dir, test.name, test.query); err == nil || changed {
			t.Fatal("invalid query was published")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if changed, err := SaveQuery(ctx, dir, "canceled", "project = DEMO"); changed || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled query was published")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid input created a directory")
	}
}
