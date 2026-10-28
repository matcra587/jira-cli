package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrIssueHierarchyInvalid refuses malformed identities or cyclic parent data.
var ErrIssueHierarchyInvalid = errors.New("invalid native issue hierarchy")

// MaxIssueHierarchyDepth bounds one explicit ancestor observation.
const MaxIssueHierarchyDepth = 32

// IssueHierarchyClient reads through the invocation's existing Jira client.
type IssueHierarchyClient interface {
	GetIssue(context.Context, string, []string, ...string) (IssueRead, error)
}

// IssueHierarchyReference prefers a native immutable ID over a display key.
type IssueHierarchyReference struct{ ID, Key string }

// IssueParentState preserves omitted, null and identified native parent values.
type IssueParentState uint8

const (
	// IssueParentUnknown means Jira did not return the requested parent field.
	IssueParentUnknown IssueParentState = iota
	// IssueParentNone means Jira explicitly returned a null parent.
	IssueParentNone
	// IssueParentKnown carries a native reference, independently read on ascent.
	IssueParentKnown
)

// IssueHierarchyNode is a read-only projection of one freshly observed issue.
type IssueHierarchyNode struct {
	ID, Key, Summary, Type string
	Parent                 IssueHierarchyReference
	ParentState            IssueParentState
}

// IssueHierarchyRead orders the selected issue before its observed ancestors.
// Incomplete observations never assert that an inaccessible parent is absent.
type IssueHierarchyRead struct {
	Nodes    []IssueHierarchyNode
	Complete bool
	Reason   string
}

// ReadIssueHierarchy follows native parents without guessing type names, custom
// fields or hierarchy levels. A later transport failure retains observed nodes;
// callers must disclose that incompleteness. Invalid data remains a refusal.
func ReadIssueHierarchy(
	ctx context.Context,
	client IssueHierarchyClient,
	reference IssueHierarchyReference,
) (IssueHierarchyRead, error) {
	out := IssueHierarchyRead{Nodes: []IssueHierarchyNode{}}
	seen := make(map[string]bool)
	for len(out.Nodes) < MaxIssueHierarchyDepth {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		selector := reference.ID
		if selector == "" {
			selector = reference.Key
		}
		if !validHierarchyIdentity(selector) {
			return out, fmt.Errorf("%w: missing issue reference", ErrIssueHierarchyInvalid)
		}
		read, err := client.GetIssue(ctx, selector, []string{"summary", "issuetype", "parent"})
		if err != nil {
			out.Reason = "ancestor_unavailable"
			return out, err
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		node, err := decodeHierarchyNode(read.Issue)
		if err != nil {
			return out, err
		}
		if reference.ID != "" && node.ID != reference.ID {
			return out, fmt.Errorf("%w: issue identity changed", ErrIssueHierarchyInvalid)
		}
		if seen[node.ID] {
			return out, fmt.Errorf("%w: parent cycle", ErrIssueHierarchyInvalid)
		}
		seen[node.ID] = true
		out.Nodes = append(out.Nodes, node)
		switch node.ParentState {
		case IssueParentNone:
			out.Complete = true
			return out, nil
		case IssueParentUnknown:
			out.Reason = "parent_not_returned"
			return out, nil
		}
		if node.Parent.ID != "" && seen[node.Parent.ID] {
			return out, fmt.Errorf("%w: parent cycle", ErrIssueHierarchyInvalid)
		}
		reference = node.Parent
	}
	out.Reason = "depth_limit"
	return out, nil
}

func validHierarchyIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func decodeHierarchyNode(bean *jira.IssueBean) (IssueHierarchyNode, error) {
	var zero IssueHierarchyNode
	if bean == nil {
		return zero, fmt.Errorf("%w: missing native issue", ErrIssueHierarchyInvalid)
	}
	validID := bean.ID.Set && !bean.ID.Null && validHierarchyIdentity(bean.ID.Value)
	validKey := bean.Key.Set && !bean.Key.Null && validHierarchyIdentity(bean.Key.Value)
	validFields := bean.Fields.Set && !bean.Fields.Null
	if !validID || !validKey || !validFields {
		return zero, fmt.Errorf("%w: missing native issue identity or fields", ErrIssueHierarchyInvalid)
	}
	fields := bean.Fields.Value
	node := IssueHierarchyNode{
		ID: bean.ID.Value, Key: bean.Key.Value,
		Summary: fields.Summary.Value, Type: fields.Issuetype.Value.Name.Value,
	}
	state, parent, err := DecodeIssueParent(fields)
	node.ParentState, node.Parent = state, parent
	return node, err
}

// DecodeIssueParent preserves absent/null distinctions in the native field.
func DecodeIssueParent(fields jira.IssueFields) (IssueParentState, IssueHierarchyReference, error) {
	var reference IssueHierarchyReference
	raw, present := fields.Extra["parent"]
	if !present {
		return IssueParentUnknown, reference, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return IssueParentNone, reference, nil
	}
	var parent jira.IssueBean
	if err := json.Unmarshal(raw, &parent); err != nil {
		return IssueParentUnknown, reference, fmt.Errorf("%w: malformed parent", ErrIssueHierarchyInvalid)
	}
	if parent.ID.Set && !parent.ID.Null {
		if !validHierarchyIdentity(parent.ID.Value) {
			return IssueParentUnknown, reference, fmt.Errorf("%w: malformed parent ID", ErrIssueHierarchyInvalid)
		}
		reference.ID = parent.ID.Value
	}
	if parent.Key.Set && !parent.Key.Null {
		if !validHierarchyIdentity(parent.Key.Value) {
			return IssueParentUnknown, reference, fmt.Errorf("%w: malformed parent key", ErrIssueHierarchyInvalid)
		}
		reference.Key = parent.Key.Value
	}
	if xstrings.AllEmpty(reference.ID, reference.Key) {
		return IssueParentUnknown, reference, fmt.Errorf("%w: parent has no identity", ErrIssueHierarchyInvalid)
	}
	return IssueParentKnown, reference, nil
}
