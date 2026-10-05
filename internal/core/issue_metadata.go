package core

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
)

// ErrIssueMetadataInvalid marks an incomplete or contradictory metadata read.
// Such a read cannot establish a complete create/edit input contract.
var ErrIssueMetadataInvalid = errors.New("invalid issue field metadata response")

// ErrIssueMetadataUnavailable identifies an unconfigured input-discovery seam.
// It is a configuration failure, not an HTTP or malformed-response outcome.
var ErrIssueMetadataUnavailable = errors.New("issue input metadata client is not configured")

// IssueMetadataClient is the authenticated read boundary for input discovery.
// Each create method fetches exactly one page; the discovery consumer owns the
// complete walk and name resolution. Edit metadata has no pagination protocol.
type IssueMetadataClient interface {
	GetCreateIssueTypes(context.Context, string, int, int) (IssueMetadataRead, error)
	GetCreateIssueFields(context.Context, string, string, int, int) (IssueMetadataRead, error)
	GetEditIssueFields(context.Context, string) (IssueEditMetadataRead, error)
}

// IssueMetadataPage records only established paging facts. Entries and Native
// retain the complete JSON values, including schemas, defaults, allowed values
// and future members. No field value or compatibility is inferred here.
type IssueMetadataPage struct {
	StartAt    int
	MaxResults int
	Total      int
	Entries    []json.RawMessage
	Native     json.RawMessage
}

// IssueMetadataRead is one native create-metadata page and its retry facts.
// A nil Page means no complete, validated page was established.
type IssueMetadataRead struct {
	Page  *IssueMetadataPage
	Retry RetryInfo
}

// IssueEditMetadataRead retains the context-bound editable field map and the
// whole native response. A nil Fields means no validated map was established;
// an explicit empty map is a valid answer with no discoverable editable fields.
type IssueEditMetadataRead struct {
	Fields map[string]json.RawMessage
	Native json.RawMessage
	Retry  RetryInfo
}

// DecodeCreateIssueTypes accepts one complete page from the project-specific
// create issue-types endpoint in the pinned platform spec.
func DecodeCreateIssueTypes(data []byte, startAt, maxResults int) (*IssueMetadataPage, error) {
	return decodeIssueMetadataPage(data, "issueTypes", "id", startAt, maxResults)
}

// DecodeCreateIssueFields accepts one complete page from a project's create
// fields endpoint for one issue-type ID. Native field definitions stay raw.
func DecodeCreateIssueFields(data []byte, startAt, maxResults int) (*IssueMetadataPage, error) {
	return decodeIssueMetadataPage(data, "fields", "fieldId", startAt, maxResults)
}

func decodeIssueMetadataPage(data []byte, member, identity string, startAt, maxResults int) (*IssueMetadataPage, error) {
	if startAt < 0 || maxResults < 1 || maxResults > 200 {
		return nil, fmt.Errorf("%w: page offset must be nonnegative and size between 1 and 200", ErrIssueMetadataInvalid)
	}
	members, err := metadataObject(data)
	if err != nil {
		return nil, err
	}
	page := &IssueMetadataPage{}
	for _, fact := range []struct {
		name  string
		value *int
	}{
		{name: "startAt", value: &page.StartAt},
		{name: "maxResults", value: &page.MaxResults},
		{name: "total", value: &page.Total},
	} {
		raw, exists := members[fact.name]
		if !exists || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, fact.value) != nil {
			return nil, fmt.Errorf("%w: page lacks an explicit integer %s", ErrIssueMetadataInvalid, fact.name)
		}
	}
	raw, exists := members[member]
	if !exists || json.Unmarshal(raw, &page.Entries) != nil || page.Entries == nil {
		return nil, fmt.Errorf("%w: page lacks an explicit %s array", ErrIssueMetadataInvalid, member)
	}
	if page.StartAt != startAt || page.MaxResults < 1 || len(page.Entries) > maxResults || len(page.Entries) > page.MaxResults {
		return nil, fmt.Errorf("%w: page offset or size contradicts the request", ErrIssueMetadataInvalid)
	}
	if page.Total < startAt || len(page.Entries) > page.Total-startAt {
		return nil, fmt.Errorf("%w: page total contradicts returned entries", ErrIssueMetadataInvalid)
	}
	if len(page.Entries) == 0 && startAt < page.Total {
		return nil, fmt.Errorf("%w: non-final page makes no progress", ErrIssueMetadataInvalid)
	}
	seen := make(map[string]struct{}, len(page.Entries))
	for _, entry := range page.Entries {
		fields, err := metadataObject(entry)
		if err != nil {
			return nil, err
		}
		var id string
		if json.Unmarshal(fields[identity], &id) != nil || strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("%w: metadata entry lacks a usable %s", ErrIssueMetadataInvalid, identity)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: page repeats an identity", ErrIssueMetadataInvalid)
		}
		seen[id] = struct{}{}
		if identity == "fieldId" {
			if err := validateIssueFieldMetadata(fields); err != nil {
				return nil, err
			}
		}
	}
	page.Native = bytes.Clone(data)
	return page, nil
}

// DecodeEditIssueFields accepts the complete native editmeta response. Field
// IDs come from its map keys, never display names or the local type catalog.
func DecodeEditIssueFields(data []byte) (IssueEditMetadataRead, error) {
	members, err := metadataObject(data)
	if err != nil {
		return IssueEditMetadataRead{}, err
	}
	fields, err := metadataObject(members["fields"])
	if err != nil {
		return IssueEditMetadataRead{}, err
	}
	for id, raw := range fields {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id {
			return IssueEditMetadataRead{}, fmt.Errorf("%w: edit field lacks a usable identity", ErrIssueMetadataInvalid)
		}
		field, err := metadataObject(raw)
		if err != nil {
			return IssueEditMetadataRead{}, err
		}
		if err := validateIssueFieldMetadata(field); err != nil {
			return IssueEditMetadataRead{}, err
		}
		if value, exists := field["fieldId"]; exists {
			var declared string
			if json.Unmarshal(value, &declared) != nil || declared != id {
				return IssueEditMetadataRead{}, fmt.Errorf("%w: edit field identity contradicts its map key", ErrIssueMetadataInvalid)
			}
		}
	}
	return IssueEditMetadataRead{Fields: fields, Native: bytes.Clone(data)}, nil
}

// The pinned FieldCreateMetadata/FieldMetadata schemas require these facts.
// Validate their shape without imposing a local registry of schema types,
// operation names or plugin values. Optional values and extensions stay raw.
func validateIssueFieldMetadata(field map[string]json.RawMessage) error {
	for _, name := range []string{"key", "name"} {
		if !metadataString(field[name]) {
			return fmt.Errorf("%w: field lacks an explicit string %s", ErrIssueMetadataInvalid, name)
		}
	}
	var required bool
	if raw := field["required"]; bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &required) != nil {
		return fmt.Errorf("%w: field lacks explicit boolean requiredness", ErrIssueMetadataInvalid)
	}
	var operations []json.RawMessage
	if json.Unmarshal(field["operations"], &operations) != nil || operations == nil {
		return fmt.Errorf("%w: field lacks an explicit operations array", ErrIssueMetadataInvalid)
	}
	for _, operation := range operations {
		if !metadataString(operation) {
			return fmt.Errorf("%w: field operation is not a string", ErrIssueMetadataInvalid)
		}
	}
	schema, err := metadataObject(field["schema"])
	if err != nil {
		return err
	}
	if !metadataString(schema["type"]) {
		return fmt.Errorf("%w: field schema lacks an explicit string type", ErrIssueMetadataInvalid)
	}
	return nil
}

func metadataString(raw json.RawMessage) bool {
	var value string
	return !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, &value) == nil
}

func metadataObject(data []byte) (map[string]json.RawMessage, error) {
	// The native input owner uses the same validator: unlike v1 Unmarshal,
	// IsValid rejects duplicate member names, including nested definitions.
	if !jsontext.Value(data).IsValid() {
		return nil, fmt.Errorf("%w: response must contain one valid JSON document with unique member names", ErrIssueMetadataInvalid)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return nil, fmt.Errorf("%w: metadata must be a JSON object", ErrIssueMetadataInvalid)
	}
	return object, nil
}
