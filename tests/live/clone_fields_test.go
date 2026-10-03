//go:build live

package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// findCloneDateField walks create metadata until a verified writable scalar
// date is found. An incomplete walk cannot establish that no field exists.
func findCloneDateField(read func(int) json.RawMessage) (string, error) {
	seen := make(map[string]bool)
	start := 0
	for range 100 {
		var page struct {
			StartAt    *int `json:"startAt"`
			MaxResults *int `json:"maxResults"`
			Total      *int `json:"total"`
			Fields     []struct {
				FieldID    string                        `json:"fieldId"`
				Operations []string                      `json:"operations"`
				Schema     struct{ Type, Custom string } `json:"schema"`
			} `json:"fields"`
		}
		if err := json.Unmarshal(read(start), &page); err != nil {
			return "", fmt.Errorf("invalid clone create metadata: %w", err)
		}
		if page.StartAt == nil || *page.StartAt != start || page.MaxResults == nil || *page.MaxResults <= 0 || page.Total == nil || *page.Total < start || page.Fields == nil || len(page.Fields) > *page.MaxResults || len(page.Fields) > *page.Total-start {
			return "", errors.New("clone create metadata has unusable pagination")
		}
		date := ""
		for _, field := range page.Fields {
			if strings.TrimSpace(field.FieldID) == "" || seen[field.FieldID] {
				return "", errors.New("clone create metadata has blank or repeated field identities")
			}
			seen[field.FieldID] = true
			if date == "" && strings.HasPrefix(field.FieldID, "customfield_") && field.Schema.Type == "date" && strings.HasSuffix(field.Schema.Custom, ":datepicker") && slices.Contains(field.Operations, "set") {
				date = field.FieldID
			}
		}
		if date != "" {
			return date, nil
		}
		start += len(page.Fields)
		if start == *page.Total {
			return "", errors.New("no writable native custom date field exists for clone acceptance")
		}
		if len(page.Fields) == 0 {
			return "", errors.New("clone create metadata ended before its total")
		}
	}
	return "", errors.New("clone create metadata exceeded the 100-page acceptance bound")
}

func TestLiveCloneDateMetadataWalk(t *testing.T) {
	t.Parallel()
	var starts []int
	field, err := findCloneDateField(func(start int) json.RawMessage {
		starts = append(starts, start)
		if start == 0 {
			return json.RawMessage(`{"startAt":0,"maxResults":1,"total":2,"fields":[{"fieldId":"summary","operations":["set"],"schema":{"type":"string"}}]}`)
		}
		return json.RawMessage(`{"startAt":1,"maxResults":1,"total":2,"fields":[{"fieldId":"customfield_10011","operations":["set"],"schema":{"type":"date","custom":"example:datepicker"}}]}`)
	})
	if err != nil || field != "customfield_10011" || !slices.Equal(starts, []int{0, 1}) {
		t.Fatalf("later-page writable custom date was lost: field=%q starts=%v err=%v", field, starts, err)
	}
	for name, body := range map[string]string{
		"missing pagination": `{"fields":[]}`,
		"empty nonfinal":     `{"startAt":0,"maxResults":1,"total":2,"fields":[]}`,
		"wrong offset":       `{"startAt":1,"maxResults":1,"total":2,"fields":[]}`,
		"missing operations": `{"startAt":0,"maxResults":1,"total":1,"fields":[{"fieldId":"customfield_10011","schema":{"type":"date","custom":"example:datepicker"}}]}`,
		"blank identity":     `{"startAt":0,"maxResults":1,"total":1,"fields":[{"fieldId":" "}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := findCloneDateField(func(int) json.RawMessage { return json.RawMessage(body) })
			if err == nil || got != "" {
				t.Fatal("incomplete or nonwritable create metadata supplied a clone stimulus")
			}
		})
	}
}
