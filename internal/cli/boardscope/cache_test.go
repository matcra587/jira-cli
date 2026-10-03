package boardscope

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/core"
)

type snapshotCache struct {
	core.Cache
	raw       []byte
	written   time.Time
	key       string
	hit       bool
	mutations int
}

func (c *snapshotCache) Get(key string) ([]byte, time.Time, bool) {
	c.key = key
	return c.raw, c.written, c.hit
}

func (c *snapshotCache) Set(string, []byte) error {
	c.mutations++
	return errors.New("offline lookup must never save")
}

func (c *snapshotCache) Forget(string) error {
	c.mutations++
	return errors.New("offline lookup must never forget")
}

func TestCachedBoardRetainsStaleSnapshotAndExactSelectorNamespace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		id   int
		key  string
	}{
		{" sprint ", 0, "v2:board:https://example.atlassian.net:name: sprint "},
		{"", 42, "v2:board:https://example.atlassian.net:id:42"},
	} {
		written := time.Now().Add(-30 * 24 * time.Hour)
		store := &snapshotCache{raw: []byte(`{"id":42,"name":"Sprint","jql":"project = KEY OR labels = scope ORDER BY Rank ASC"}`), written: written, hit: true}
		board, stamp, err := ReadCached(store, "https://example.atlassian.net", test.name, test.id)
		if err != nil || board.ID != 42 || board.Name != "Sprint" || board.JQL != "project = KEY OR labels = scope ORDER BY Rank ASC" || stamp != written || store.key != test.key || store.mutations != 0 {
			t.Fatalf("snapshot or selector changed: %+v %v %s %v", board, stamp, store.key, err)
		}
	}
}

func TestCachedBoardRefusesIncompleteOrMismatchedSnapshot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw   string
		stamp time.Time
		hit   bool
		name  string
		id    int
	}{
		{`{"id":42,"name":"Sprint","jql":"project = KEY"}`, time.Now(), false, "", 42},
		{`null`, time.Now(), true, "", 42},
		{`{}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":"project = KEY"} {}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":"project = KEY"}`, time.Time{}, true, "", 42},
		{`{"id":42,"jql":"project = KEY"}`, time.Now().Add(time.Hour), true, "", 42},
		{`{"id":0,"jql":"project = KEY"}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":""}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":" \t "}`, time.Now(), true, "", 42},
		{`{"id":43,"jql":"project = KEY"}`, time.Now(), true, "", 42},
		{`{"id":42,"name":"Different","jql":"project = KEY"}`, time.Now(), true, "Sprint", 0},
	} {
		store := &snapshotCache{raw: []byte(test.raw), written: test.stamp, hit: test.hit}
		if board, stamp, err := ReadCached(store, "https://example.atlassian.net", test.name, test.id); err == nil || board.ID != 0 || !stamp.IsZero() || store.mutations != 0 {
			t.Fatalf("bad snapshot accepted: %+v %v %v", board, stamp, err)
		}
	}
	if _, _, err := ReadCached(nil, "site", "Sprint", 0); err == nil {
		t.Fatal("missing cache was accepted")
	}
}

func TestCachedBoardCannotCrossAccountSiteOrSelector(t *testing.T) {
	t.Parallel()
	disk := &adapters.MemoryCache{}
	first, err := adapters.NewScopedCache(disk, "https://one.example", "one@example.com")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Resolution{ID: 42, Name: "Sprint", JQL: "project = KEY"})
	if err != nil || first.Set(CacheKey("https://one.example", "id", "42"), raw) != nil {
		t.Fatal("cannot seed scoped cache")
	}
	for _, test := range []struct{ site, account string }{{"https://two.example", "one@example.com"}, {"https://one.example", "two@example.com"}} {
		other, err := adapters.NewScopedCache(disk, test.site, test.account)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadCached(other, test.site, "", 42); err == nil {
			t.Fatal("offline board crossed site/account namespace")
		}
	}
	if _, _, err := ReadCached(first, "https://one.example", "Sprint", 0); err == nil {
		t.Fatal("ID record silently satisfied a name lookup")
	}
	if err := disk.Set(CacheKey("https://two.example", "id", "42"), raw); err != nil {
		t.Fatal(err)
	}
	other, err := adapters.NewScopedCache(disk, "https://two.example", "one@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadCached(other, "https://two.example", "", 42); err == nil {
		t.Fatal("unscoped legacy cache satisfied a local lookup")
	}
}
