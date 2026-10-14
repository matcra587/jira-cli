package core

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strings"

	xmaps "github.com/gechr/x/maps"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jira"
)

// PreparedIssueField reports local handling, never Jira permission or acceptance.
type PreparedIssueField struct {
	Field, Type, Handling string
}

// PreparedIssueInput is a candidate produced from an explicit metadata snapshot.
// Native mutation requests do not call this opt-in authoring owner.
type PreparedIssueInput struct {
	Payload  jira.IssueWriteRequest
	Fields   []PreparedIssueField
	Warnings []adf.Warning
}

// IssueInputPreparationError refuses an authored value without dropping it.
type IssueInputPreparationError struct {
	Field, Reason string
}

func (e *IssueInputPreparationError) Error() string {
	return fmt.Sprintf("issue input preparation: field %q: %s", e.Field, e.Reason)
}

// PrepareIssueInput explicitly authors legacy scalar shapes into native values.
// It is pure and all-or-nothing; it neither fetches metadata nor submits a write.
func PrepareIssueInput(ctx context.Context, request jira.IssueWriteRequest, fields map[string]json.RawMessage, create bool) (PreparedIssueInput, error) {
	if err := ctx.Err(); err != nil {
		return PreparedIssueInput{}, err
	}
	candidate, err := flattenPreparationFields(request)
	if err != nil {
		return PreparedIssueInput{}, err
	}
	encoded, err := jira.MarshalNoEscape(candidate)
	if err != nil || !jsontext.Value(encoded).IsValid() {
		return PreparedIssueInput{}, &IssueInputPreparationError{Reason: "payload must contain valid native JSON with unique member names"}
	}
	if _, err := PreflightIssueFields(ctx, candidate, fields, create); err != nil {
		return PreparedIssueInput{}, err
	}
	out := PreparedIssueInput{
		Payload: candidate, Fields: []PreparedIssueField{}, Warnings: []adf.Warning{},
	}
	for _, id := range candidate.Keys() {
		if err := ctx.Err(); err != nil {
			return PreparedIssueInput{}, err
		}
		codec, kind, err := preparationFieldCodec(id, fields[id], create)
		if err != nil {
			return PreparedIssueInput{}, err
		}
		row := PreparedIssueField{Field: id, Type: kind, Handling: "unverified"}
		raw, set := candidate.Fields[id]
		if _, updated := candidate.Update[id]; updated {
			if set {
				return PreparedIssueInput{}, &IssueInputPreparationError{Field: id, Reason: "field appears in both fields and update; supply one route"}
			}
			// Screen and verb checks ran above. No baseline evidence supplies
			// coercion rules for native add/remove/set operation values.
			out.Fields = append(out.Fields, row)
			continue
		}
		if codec == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			out.Fields = append(out.Fields, row)
			continue
		}
		value, err := codec(raw)
		if err != nil {
			return PreparedIssueInput{}, &IssueInputPreparationError{Field: id, Reason: err.Error()}
		}
		if value.Validated {
			row.Handling = "validated"
		}
		if !bytes.Equal(value.Value, raw) {
			row.Handling = "encoded"
		}
		out.Payload.Fields[id] = value.Value
		out.Fields = append(out.Fields, row)
		for _, warning := range value.Warnings {
			warning.Field = id
			out.Warnings = append(out.Warnings, warning)
		}
	}
	if err := ctx.Err(); err != nil {
		return PreparedIssueInput{}, err
	}
	return out, nil
}

func flattenPreparationFields(request jira.IssueWriteRequest) (jira.IssueWriteRequest, error) {
	candidate := request.Clone()
	raw, present := candidate.Fields["custom_fields"]
	if !present {
		return candidate, nil
	}
	fields, err := metadataObject(raw)
	if err != nil {
		return jira.IssueWriteRequest{}, &IssueInputPreparationError{Field: "custom_fields", Reason: "legacy wrapper must be an object of field IDs and values"}
	}
	for id, value := range xmaps.Sorted(fields) {
		if _, collision := candidate.Fields[id]; collision {
			return jira.IssueWriteRequest{}, &IssueInputPreparationError{Field: id, Reason: "legacy wrapper collides with a field already authored outside it"}
		}
		candidate.Fields[id] = value
	}
	delete(candidate.Fields, "custom_fields")
	return candidate, nil
}

type preparedIssueFieldValue struct {
	Value     json.RawMessage
	Warnings  []adf.Warning
	Validated bool
}

type issueInputCodec func(json.RawMessage) (preparedIssueFieldValue, error)

func validatedPreparationValue(raw json.RawMessage, warnings []adf.Warning) preparedIssueFieldValue {
	return preparedIssueFieldValue{Value: raw, Warnings: warnings, Validated: true}
}

// These identity keys are fixed authoring meanings, never digit-based guesses.
var preparationSystemCodecs = map[string]issueInputCodec{
	"project": prepareIdentity("key", "id"), "parent": prepareParent,
	"issuetype": prepareIdentity("name", "id"), "priority": prepareIdentity("name", "id"),
	"assignee": prepareIdentity("accountId", "id"), "reporter": prepareIdentity("accountId", "id"),
	"components":  prepareArray(prepareIdentity("name", "id")),
	"versions":    prepareArray(prepareIdentity("name", "id")),
	"fixVersions": prepareArray(prepareIdentity("name", "id")),
	"summary":     prepareString, "labels": prepareArray(prepareString), "duedate": prepareDate,
}

var preparationCustomCodecs = map[string]issueInputCodec{
	"textfield": prepareString, "url": prepareString, "string": prepareString,
	"float": prepareNumber, "number": prepareNumber,
	"datepicker": prepareDate, "date": prepareDate, "datetime": prepareDatetime,
	"labels": prepareArray(prepareString),
	"select": prepareIdentity("value", "id"), "radiobuttons": prepareIdentity("value", "id"),
	"multiselect":     prepareArray(prepareIdentity("value", "id")),
	"multicheckboxes": prepareArray(prepareIdentity("value", "id")),
	"userpicker":      prepareIdentity("accountId", "id"), "user": prepareIdentity("accountId", "id"),
	"multiuserpicker": prepareArray(prepareIdentity("accountId", "id")),
	"grouppicker":     prepareIdentity("name"), "group": prepareIdentity("name"),
	"multigrouppicker": prepareArray(prepareIdentity("name")),
	"version":          prepareIdentity("name", "id"), "multiversion": prepareArray(prepareIdentity("name", "id")),
	"project": prepareIdentity("key", "id"), "cascadingselect": prepareCascading,
	"textarea": prepareTextarea,
}

func preparationFieldCodec(id string, definition json.RawMessage, create bool) (issueInputCodec, string, error) {
	if create && (id == "project" || id == "issuetype") {
		return preparationSystemCodecs[id], id, nil
	}
	members, err := metadataObject(definition)
	if err != nil {
		return nil, "", err
	}
	schema, err := metadataObject(members["schema"])
	if err != nil {
		return nil, "", err
	}
	if raw, present := schema["custom"]; present {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, "", nil
		}
		var custom string
		if json.Unmarshal(raw, &custom) != nil {
			return nil, "", fmt.Errorf("%w: field %q custom schema identity must be a string", ErrIssueMetadataInvalid, id)
		}
		// Only the built-in namespace and historical bare names are known.
		// A marketplace plugin ending in :select remains an unknown plugin.
		token := strings.TrimPrefix(custom, "com.atlassian.jira.plugin.system.customfieldtypes:")
		return preparationCustomCodecs[token], custom, nil
	}
	if raw, present := schema["system"]; present {
		var system string
		if json.Unmarshal(raw, &system) != nil || system != id {
			return nil, "", fmt.Errorf("%w: field %q system identity contradicts its map key", ErrIssueMetadataInvalid, id)
		}
		return preparationSystemCodecs[system], system, nil
	}
	return nil, "", nil
}

func preparationJSON(raw json.RawMessage) error {
	if !jsontext.Value(raw).IsValid() {
		return fmt.Errorf("value must be valid JSON with unique member names")
	}
	return nil
}

func prepareString(raw json.RawMessage) (preparedIssueFieldValue, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return preparedIssueFieldValue{}, fmt.Errorf("expected a nonempty string")
	}
	return validatedPreparationValue(raw, nil), nil
}

func prepareIdentity(keys ...string) issueInputCodec {
	return func(raw json.RawMessage) (preparedIssueFieldValue, error) {
		if err := preparationJSON(raw); err != nil {
			return preparedIssueFieldValue{}, err
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			if value == "" {
				return preparedIssueFieldValue{}, fmt.Errorf("expected a nonempty identity string")
			}
			encoded, err := jira.MarshalNoEscape(map[string]json.RawMessage{keys[0]: raw})
			if err != nil {
				return preparedIssueFieldValue{}, err
			}
			return validatedPreparationValue(encoded, nil), nil
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return preparedIssueFieldValue{}, fmt.Errorf("expected a string or native identity object")
		}
		found := false
		for _, key := range keys {
			if identity, exists := object[key]; exists {
				if _, err := prepareString(identity); err != nil {
					return preparedIssueFieldValue{}, fmt.Errorf("identity %q: %w", key, err)
				}
				found = true
			}
		}
		if !found {
			return preparedIssueFieldValue{}, fmt.Errorf("native object requires one of %v", keys)
		}
		return validatedPreparationValue(raw, nil), nil
	}
}

func prepareArray(element issueInputCodec) issueInputCodec {
	return func(raw json.RawMessage) (preparedIssueFieldValue, error) {
		if err := preparationJSON(raw); err != nil {
			return preparedIssueFieldValue{}, err
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return preparedIssueFieldValue{}, fmt.Errorf("expected an array")
		}
		changed := false
		for i, value := range values {
			encoded, err := element(value)
			if err != nil {
				return preparedIssueFieldValue{}, fmt.Errorf("element %d: %w", i, err)
			}
			changed = changed || !bytes.Equal(value, encoded.Value)
			values[i] = encoded.Value
		}
		if !changed {
			return validatedPreparationValue(raw, nil), nil
		}
		var buffer bytes.Buffer
		buffer.WriteByte('[')
		for i, value := range values {
			if i > 0 {
				buffer.WriteByte(',')
			}
			buffer.Write(value)
		}
		buffer.WriteByte(']')
		return validatedPreparationValue(buffer.Bytes(), nil), nil
	}
}
