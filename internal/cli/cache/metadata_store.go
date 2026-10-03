package cache

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"slices"
	"strings"
	"time"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type metadataSnapshot struct {
	Version int `json:"version"`
	MetadataValue
}

// InspectMetadata reads the producer's actual key and payload without fetching
// or modifying storage. Valid stale data remains inspectable; only fresh data
// can suppress collection. Missing, malformed and future-dated entries cannot.
func InspectMetadata(store core.Cache, site, name string, ttl time.Duration, boards func([]byte) (MetadataValue, error)) (MetadataValue, time.Time, string) {
	empty := MetadataValue{Items: []json.RawMessage{}}
	resource, ok := metadataResource(name)
	if !ok || store == nil {
		return empty, time.Time{}, "missing"
	}
	raw, written, ok := store.Get(resource.Key(site))
	if !ok {
		return empty, time.Time{}, "missing"
	}
	var value MetadataValue
	var err error
	switch name {
	case "boards":
		if boards == nil {
			return empty, time.Time{}, "malformed"
		}
		value, err = boards(raw)
	case "linktypes":
		value.Items, err = decodeMetadataItems(raw, name)
		value.Count, value.Pages, value.Complete = len(value.Items), 1, true
	default:
		// Presence matters: zero counts and incomplete walks are valid only
		// when the producer explicitly recorded those collection facts.
		var facts struct {
			Version  *int  `json:"version"`
			Count    *int  `json:"count"`
			Pages    *int  `json:"pages"`
			Complete *bool `json:"complete"`
		}
		err = jsonv2.Unmarshal(raw, &facts)
		if err == nil && (facts.Version == nil || facts.Count == nil || facts.Pages == nil || facts.Complete == nil) {
			err = metadataInvalid("cached metadata lacks explicit collection facts")
		}
		if err != nil {
			break
		}
		var snapshot metadataSnapshot
		err = jsonv2.Unmarshal(raw, &snapshot)
		if err == nil && snapshot.Version != 1 {
			err = metadataInvalid("unsupported metadata snapshot version")
		}
		value = snapshot.MetadataValue
	}
	if err != nil || validateStoredMetadata(name, value, boards) != nil {
		return empty, time.Time{}, "malformed"
	}
	age := time.Since(written)
	if written.IsZero() || age < 0 || age > ttl {
		return value, written, "stale"
	}
	return value, written, "fresh"
}

func validateStoredMetadata(name string, value MetadataValue, boards func([]byte) (MetadataValue, error)) error {
	if value.Items == nil || value.Count < 0 || value.Pages < 1 {
		return metadataInvalid("cached metadata lacks collection facts")
	}
	if name == "boards" {
		if boards == nil || len(value.Items) != 0 || value.Payload == nil || value.NextPageToken != "" {
			return metadataInvalid("cached board metadata lacks its owning snapshot")
		}
		decoded, err := boards(value.Payload)
		if err != nil {
			return err
		}
		if value.Count != decoded.Count || value.Pages != decoded.Pages || value.Complete != decoded.Complete || value.TruncatedReason != decoded.TruncatedReason ||
			(value.NextStart == nil) != (decoded.NextStart == nil) || value.NextStart != nil && *value.NextStart != *decoded.NextStart {
			return metadataInvalid("cached board facts disagree with their owning snapshot")
		}
		return nil
	}
	if value.Payload != nil || value.Count != len(value.Items) {
		return metadataInvalid("cached metadata count or payload is inconsistent")
	}
	if value.Complete {
		if value.TruncatedReason != "" || value.NextStart != nil || value.NextPageToken != "" {
			return metadataInvalid("complete cached metadata has a resume cursor")
		}
	} else if name == "epics" {
		if !slices.Contains([]string{envelope.TruncatedReasonMaxPages, envelope.TruncatedReasonMaxIssues, envelope.TruncatedReasonMaxEmptyPages, envelope.TruncatedReasonMissingCursor, envelope.TruncatedReasonRepeatedCursor}, value.TruncatedReason) || value.NextStart != nil || value.TruncatedReason != envelope.TruncatedReasonMissingCursor && value.NextPageToken == "" {
			return metadataInvalid("incomplete epic metadata lacks search provenance")
		}
	} else if value.NextStart == nil || *value.NextStart != value.Count || value.NextPageToken != "" || (value.TruncatedReason != "max_pages" && value.TruncatedReason != "max_results") {
		return metadataInvalid("incomplete cached metadata lacks an offset or bound")
	}
	if name != "epics" {
		if (name == "fields" || name == "issuetypes" || name == "linktypes" || name == "statuses") && (!value.Complete || value.Pages != 1) {
			return metadataInvalid("array metadata has impossible page facts")
		}
		raw, err := jira.MarshalNoEscape(value.Items)
		if err != nil {
			return err
		}
		_, err = decodeMetadataItems(raw, name)
		return err
	}
	seen := make(map[string]bool, len(value.Items))
	for _, raw := range value.Items {
		var issue struct {
			ID     string          `json:"id"`
			Key    string          `json:"key"`
			Fields json.RawMessage `json:"fields"`
		}
		if jsonv2.Unmarshal(raw, &issue) != nil || strings.TrimSpace(issue.ID) == "" || len(issue.Fields) == 0 || issue.Fields[0] != '{' {
			return metadataInvalid("cached epic lacks native issue identity or fields")
		}
		if _, err := command.ParseIssueKey(issue.Key); err != nil || seen[issue.Key] {
			return metadataInvalid("cached epic has an invalid or repeated key")
		}
		seen[issue.Key] = true
	}
	return nil
}
