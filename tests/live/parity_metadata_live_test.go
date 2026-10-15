//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"strconv"
	"testing"
	"time"
)

func parityNativeMetadata(t *testing.T, p *parityRun, lane string, fixture parityMetadataFixture) ([]json.RawMessage, int) {
	t.Helper()
	endpoint, size := fixture.Endpoint, 100
	if lane == "before" {
		if fixture.BeforeEndpoint != "" {
			endpoint = fixture.BeforeEndpoint
		}
		if fixture.BeforePageSize > 0 {
			size = fixture.BeforePageSize
		}
	}
	paged := fixture.Paged && (lane != "before" || fixture.BeforeEndpoint == "")
	items := []json.RawMessage{}
	start := 0
	for page := 1; page <= 100; page++ {
		args := []string{"api", "get", endpoint}
		if paged {
			args = append(args, "--query=startAt="+strconv.Itoa(start), "--query=maxResults="+strconv.Itoa(size))
		}
		read := rawRead(t, p.owner, args...)
		if !jsontext.Value(read.Response.JSON).IsValid() {
			t.Fatal("independent native metadata contains duplicate names or invalid JSON")
		}
		raw := read.Response.JSON
		last, next := true, 0
		if paged {
			var native struct {
				StartAt, MaxResults, Total *int
				IsLast                     *bool
				Values                     json.RawMessage
			}
			var rows []json.RawMessage
			if json.Unmarshal(raw, &native) != nil || native.StartAt == nil || *native.StartAt != start || native.MaxResults == nil || *native.MaxResults < 1 || native.Total == nil || native.IsLast == nil || json.Unmarshal(native.Values, &rows) != nil || rows == nil || len(rows) > size || len(rows) > *native.MaxResults || *native.Total < start+len(rows) || *native.IsLast != (start+len(rows) == *native.Total) {
				t.Fatal("independent metadata walk lacks complete bounded page facts")
			}
			raw, last, next = native.Values, *native.IsLast, start+len(rows)
			if !last && len(rows) == 0 {
				t.Fatal("independent metadata walk made no progress")
			}
			if lane == "before" {
				next = start + *native.MaxResults
			}
		} else if fixture.Name == "linktypes" {
			var native struct {
				Types json.RawMessage `json:"issueLinkTypes"`
			}
			if json.Unmarshal(raw, &native) != nil {
				t.Fatal("independent link type response is not an object")
			}
			raw = native.Types
		}
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows)+len(items) > 10000 {
			t.Fatal("independent metadata values are missing or exceed the fixture bound")
		}
		items = append(items, rows...)
		if last {
			return items, page
		}
		start = next
	}
	t.Fatal("independent metadata walk exceeded its page bound")
	return nil, 0
}

func TestLiveParityMetadataPrimers(t *testing.T) {
	p := newParityRun(t)
	for _, fixture := range parityMetadataFixtures(t) {
		for _, lane := range p.evidence.Lanes {
			t.Run(fixture.Name+"/"+lane.Name, func(t *testing.T) {
				items, pages := parityNativeMetadata(t, p, lane.Name, fixture)
				if lane.Name == "before" {
					var ok bool
					items, ok = parityMetadataLegacyItems(items, fixture.Name)
					if !ok {
						t.Fatal("independent legacy native metadata projection is unavailable")
					}
				}
				force := "--force"
				if lane.Name == "before" {
					force = "--refresh"
				}
				began := time.Now()
				frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "cache", fixture.Name, force)
				if exit != 0 || !frame.OK {
					t.Fatal("paired live metadata collection failed; inspect retained receipts")
				}
				data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache."+fixture.Name))
				state := "missing"
				if lane.Name == "before" {
					state = "refresh"
				}
				want := parityMetadataExpected{Resource: fixture.Name, Profile: lane.Profile, State: state, Items: items, TTL: fixture.TTL, Pages: pages, Force: true, Earliest: began, Latest: time.Now()}
				if fixture.Name == "fields" {
					// Independent Jira field reads can reorder equivalent rows.
					// Bind complete native membership to the stored order, then
					// require the public primer to retain that complete snapshot.
					written, stored := parityLiveMetadataStored(t, p, lane, fixture, items, pages)
					if written.Before(began) || written.After(time.Now()) {
						t.Fatal("native field primer has an impossible publication time")
					}
					want.Items, want.Earliest = stored, written
					if lane.Name == "before" {
						want.Stamp = written.UTC().Format(time.RFC3339)
					}
				}
				if err != nil || !parityMetadataProjection(data, lane.Name, want) {
					t.Fatal("metadata primer differs from independent native values and collection/publication receipt")
				}
				p.check(t, lane, fixture.ID+".metadata.native")
			})
		}
	}
}
