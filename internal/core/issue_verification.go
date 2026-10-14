package core

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jira"
)

// IssueVerificationReader admits complete bounded authenticated readbacks.
// Alternate JiraClient implementations remain responsible for valid IssueRead.
type IssueVerificationReader interface {
	GetIssueVerification(context.Context, string, []string) (IssueVerificationRead, error)
}

// IssueVerificationRead keeps comparison bytes independently of the typed
// presentation model, which can normalize numbers and native member spelling.
type IssueVerificationRead struct {
	Read   IssueRead
	Fields map[string]json.RawMessage
}

// IssueFieldVerification compares one requested field with its later observed state.
// Differences do not establish why the value differs or authorize replay.
type IssueFieldVerification struct {
	Field    string
	Outcome  string
	Reason   string
	Observed jira.Nullable[json.RawMessage]
}

// CompareIssueWrite compares authored native values with one field readback.
// JSON structural comparison reuses adf.StructuralEqual, whose generic JSON
// contract preserves number spelling and refuses ambiguous member names.
func CompareIssueWrite(request jira.IssueWriteRequest, stored jira.IssueFields) ([]IssueFieldVerification, error) {
	raw, err := jira.MarshalNoEscape(stored)
	if err != nil {
		return nil, fmt.Errorf("encode observed issue fields: %w", err)
	}
	var observed map[string]json.RawMessage
	if err := jsonv2.Unmarshal(raw, &observed); err != nil {
		return nil, fmt.Errorf("decode observed issue fields: %w", err)
	}
	return CompareNativeIssueWrite(request, observed)
}

// CompareNativeIssueWrite uses original observed field values without a typed
// decode/encode round trip. It retains only fields explicitly requested.
func CompareNativeIssueWrite(request jira.IssueWriteRequest, observed map[string]json.RawMessage) ([]IssueFieldVerification, error) {
	for _, raw := range observed {
		if !validVerificationJSON(raw) {
			return nil, fmt.Errorf("invalid native observed field value")
		}
	}
	rows := make([]IssueFieldVerification, 0, len(request.Keys()))
	for _, field := range request.Keys() {
		row := IssueFieldVerification{Field: field, Outcome: "unverified", Reason: "field-not-returned"}
		value, present := observed[field]
		if present {
			row.Observed = jira.NullableOf(json.RawMessage(bytes.Clone(value)))
			if isVerificationNull(value) {
				row.Observed = jira.NullableNull[json.RawMessage]()
			}
		}
		want, replacement := request.Fields[field]
		operations, updated := request.Update[field]
		switch {
		case replacement && updated:
			row.Reason = "conflicting-request-containers"
		case !present:
		case updated:
			row.Outcome, row.Reason = compareIssueOperations(field, operations, value)
		default:
			row.Outcome, row.Reason = compareIssueValue(field, want, value)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func compareIssueValue(field string, want, got json.RawMessage) (string, string) {
	equal, err := adf.StructuralEqual(want, got)
	if err != nil {
		return "unverified", "invalid-native-value"
	}
	if equal {
		return "matches", "native-value"
	}
	if field == "description" || field == "environment" || verificationCustomADF(field, want) {
		return "unverified", "adf-normalization"
	}
	if (field == "assignee" || field == "reporter") && isVerificationNull(got) {
		var object map[string]json.RawMessage
		if jsonv2.Unmarshal(want, &object) == nil && len(object) == 1 && isVerificationNull(object["accountId"]) {
			return "matches", "account-clear"
		}
	}
	if verificationCollection(field) {
		var expected, actual []json.RawMessage
		if jsonv2.Unmarshal(want, &expected) != nil || jsonv2.Unmarshal(got, &actual) != nil || expected == nil || actual == nil {
			return "differs", "native-value"
		}
		if len(expected) == 0 {
			return "differs", "collection-clear"
		}
		for _, member := range expected {
			if !verificationContains(field, actual, member) {
				return "differs", "collection-membership"
			}
		}
		if !verificationReplacementCovered(field, expected, actual) {
			return "differs", "collection-membership"
		}
		return "matches", "collection-membership"
	}
	if verificationIdentity(field, want, got) {
		return "matches", "object-identity"
	}
	if strings.HasPrefix(field, "customfield_") {
		var expected, actual []json.RawMessage
		if jsonv2.Unmarshal(want, &expected) == nil && jsonv2.Unmarshal(got, &actual) == nil && len(expected) != 0 && actual != nil {
			identified := slices.ContainsFunc(expected, func(value json.RawMessage) bool { return !verificationIdentity(field, value, value) })
			if !identified {
				for _, value := range expected {
					if !verificationContains(field, actual, value) {
						return "differs", "identity-membership"
					}
				}
				if !verificationReplacementCovered(field, expected, actual) {
					return "differs", "identity-membership"
				}
				return "matches", "identity-membership"
			}
		}
	}
	return "differs", "native-value"
}

// Custom rich-text fields use ADF too. Reuse its preservation parser rather than
// schema-validating future native nodes or guessing from arbitrary object keys.
func verificationCustomADF(field string, value json.RawMessage) bool {
	if !strings.HasPrefix(field, "customfield_") {
		return false
	}
	_, err := adf.ParseDocument(value)
	return err == nil
}

// Known unordered system collections admit membership proofs. Custom arrays
// admit identity membership only when every requested element has a usable
// option/user identity; other arrays retain structural order.
func verificationCollection(field string) bool {
	return slices.Contains([]string{"labels", "components", "fixVersions", "versions"}, field)
}

// Replacement membership must account for every observed member. Keep each
// authored object in the expected position so server-expanded metadata is allowed.
func verificationReplacementCovered(field string, expected, actual []json.RawMessage) bool {
	return !slices.ContainsFunc(actual, func(observed json.RawMessage) bool {
		return !slices.ContainsFunc(expected, func(authored json.RawMessage) bool {
			return verificationContains(field, []json.RawMessage{observed}, authored)
		})
	})
}

func verificationContains(field string, values []json.RawMessage, want json.RawMessage) bool {
	return slices.ContainsFunc(values, func(got json.RawMessage) bool {
		equal, err := adf.StructuralEqual(want, got)
		return err == nil && (equal || verificationIdentity(field, want, got))
	})
}

// A single authored identity can match a server-expanded object. Other authored
// members must match structurally, except identified cascading-select children,
// whose authored members are checked against their expanded native objects.
func verificationIdentity(field string, want, got json.RawMessage) bool {
	if !strings.HasPrefix(field, "customfield_") && !slices.Contains([]string{"assignee", "reporter", "priority", "issuetype", "project", "parent", "components", "fixVersions", "versions"}, field) {
		return false
	}
	var expected, actual map[string]json.RawMessage
	if jsonv2.Unmarshal(want, &expected) != nil || jsonv2.Unmarshal(got, &actual) != nil || expected == nil || actual == nil {
		return false
	}
	identity := false
	for key, value := range expected {
		if verificationIdentityKey(field, key) {
			var text string
			if isVerificationNull(value) || jsonv2.Unmarshal(value, &text) != nil || strings.TrimSpace(text) == "" {
				return false
			}
			identity = true
		}
		observed, present := actual[key]
		if !present {
			return false
		}
		equal, err := adf.StructuralEqual(value, observed)
		if err != nil || !equal {
			// A cascading-select child is an option identity that Jira expands.
			if strings.HasPrefix(field, "customfield_") && key == "child" && verificationIdentity(field, value, observed) {
				continue
			}
			// Jira's named system values and issue/project keys are matched
			// without case; numeric IDs and option values remain exact.
			var a, b string
			if !slices.Contains([]string{"priority", "issuetype", "project", "parent", "components", "fixVersions", "versions"}, field) || (key != "name" && key != "key") || jsonv2.Unmarshal(value, &a) != nil || jsonv2.Unmarshal(observed, &b) != nil || !strings.EqualFold(a, b) {
				return false
			}
		}
	}
	return identity
}

func verificationIdentityKey(field, key string) bool {
	switch field {
	case "assignee", "reporter":
		return key == "accountId"
	case "parent", "project":
		return key == "id" || key == "key"
	case "priority", "issuetype", "components", "fixVersions", "versions":
		return key == "id" || key == "name"
	default:
		return strings.HasPrefix(field, "customfield_") && slices.Contains([]string{"id", "accountId", "key", "name", "value"}, key)
	}
}

func compareIssueOperations(field string, raw, got json.RawMessage) (string, string) {
	var operations []map[string]json.RawMessage
	if !validVerificationJSON(raw) || jsonv2.Unmarshal(raw, &operations) != nil || len(operations) == 0 {
		return "unverified", "unsupported-update-operation"
	}
	// A final set establishes an observable expected value without reading a
	// preimage. Later unsupported operations still prevent a success claim.
	lastSet := -1
	for i, operation := range operations {
		if len(operation) != 1 {
			return "unverified", "unsupported-update-operation"
		}
		if _, ok := operation["set"]; ok {
			lastSet = i
			continue
		}
		value, add := operation["add"]
		if !add {
			var remove bool
			value, remove = operation["remove"]
			if !remove {
				return "unverified", "unsupported-update-operation"
			}
		}
		if !verificationCollection(field) || verificationMemberKind(field, value) == "" {
			return "unverified", "unsupported-update-operation"
		}
	}
	if lastSet == len(operations)-1 {
		return compareIssueValue(field, operations[lastSet]["set"], got)
	}
	if !verificationCollection(field) {
		return "unverified", "unsupported-update-operation"
	}
	var actual []json.RawMessage
	if jsonv2.Unmarshal(got, &actual) != nil || actual == nil {
		return "unverified", "collection-not-returned"
	}
	var expected []json.RawMessage
	if lastSet >= 0 {
		if jsonv2.Unmarshal(operations[lastSet]["set"], &expected) != nil || expected == nil {
			return "unverified", "unsupported-update-operation"
		}
	}
	type constraint struct {
		value   json.RawMessage
		present bool
	}
	var constraints []constraint
	identityKind := ""
	acceptIdentity := func(value json.RawMessage) bool {
		kind := verificationMemberKind(field, value)
		if kind == "" || (identityKind != "" && identityKind != kind) {
			return false
		}
		identityKind = kind
		return true
	}
	for _, value := range expected {
		if !acceptIdentity(value) {
			return "unverified", "ambiguous-update-identity"
		}
	}
	for _, operation := range operations[lastSet+1:] {
		value, add := operation["add"]
		if !add {
			var remove bool
			value, remove = operation["remove"]
			if !remove {
				return "unverified", "unsupported-update-operation"
			}
		}
		if !acceptIdentity(value) {
			return "unverified", "ambiguous-update-identity"
		}
		constraints = slices.DeleteFunc(constraints, func(old constraint) bool { return verificationContains(field, []json.RawMessage{old.value}, value) })
		constraints = append(constraints, constraint{value, add})
		if lastSet >= 0 {
			expected = slices.DeleteFunc(expected, func(old json.RawMessage) bool { return verificationContains(field, []json.RawMessage{old}, value) })
			if add {
				expected = append(expected, value)
			}
		}
	}
	if lastSet >= 0 {
		if len(expected) == 0 && len(actual) != 0 {
			return "differs", "collection-clear"
		}
		for _, value := range expected {
			if !verificationContains(field, actual, value) {
				return "differs", "update-membership"
			}
		}
		if !verificationReplacementCovered(field, expected, actual) {
			return "differs", "update-membership"
		}
		// A set followed by remove must also prove the removal, including
		// members that were originally absent from the requested set.
	}
	for _, member := range actual {
		if !verificationObservedIdentity(field, identityKind, member) {
			return "unverified", "collection-identity-unavailable"
		}
	}
	for _, check := range constraints {
		if verificationContains(field, actual, check.value) != check.present {
			return "differs", "update-membership"
		}
	}
	return "matches", "update-membership"
}

func verificationObservedIdentity(field, identityKind string, value json.RawMessage) bool {
	if field == "labels" {
		return verificationMemberKind(field, value) == "string"
	}
	var object map[string]json.RawMessage
	if jsonv2.Unmarshal(value, &object) != nil {
		return false
	}
	var text string
	raw, present := object[identityKind]
	return present && !isVerificationNull(raw) && jsonv2.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != ""
}

// Updates using id and name together cannot prove whether two operands name
// the same member. Retain that uncertainty rather than inventing an alias map.
func verificationMemberKind(field string, value json.RawMessage) string {
	var text string
	if field == "labels" {
		if !isVerificationNull(value) && jsonv2.Unmarshal(value, &text) == nil {
			return "string"
		}
		return ""
	}
	var object map[string]json.RawMessage
	if jsonv2.Unmarshal(value, &object) != nil || len(object) != 1 {
		return ""
	}
	for _, key := range []string{"id", "name"} {
		if raw, ok := object[key]; ok && !isVerificationNull(raw) && jsonv2.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != "" {
			return key
		}
	}
	return ""
}

func validVerificationJSON(raw json.RawMessage) bool {
	_, err := adf.StructuralEqual(raw, raw)
	return err == nil
}

func isVerificationNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.Trim(raw, " \t\r\n"), []byte("null"))
}
