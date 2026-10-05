package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// IssueCreateProjectClient resolves the native identity used by a create
// skeleton. A project selector can be a key or an ID; guessing which would
// produce an invalid fields.project object for one of those routes.
type IssueCreateProjectClient interface {
	GetCreateIssueProject(context.Context, string) (IssueCreateProjectRead, error)
}

// IssueCreateProjectRead retains the whole project response and its established
// identities. Native remains the source of truth at consumer boundaries.
type IssueCreateProjectRead struct {
	ID     string
	Key    string
	Native json.RawMessage
	Retry  RetryInfo
}

// DecodeIssueCreateProject establishes a usable native project identity without
// dropping optional or unmodeled project members.
func DecodeIssueCreateProject(data []byte) (IssueCreateProjectRead, error) {
	fields, err := metadataObject(data)
	if err != nil {
		return IssueCreateProjectRead{}, err
	}
	out := IssueCreateProjectRead{}
	for _, fact := range []struct {
		name  string
		value *string
	}{
		{name: "id", value: &out.ID},
		{name: "key", value: &out.Key},
	} {
		if json.Unmarshal(fields[fact.name], fact.value) != nil || strings.TrimSpace(*fact.value) == "" || strings.TrimSpace(*fact.value) != *fact.value {
			return IssueCreateProjectRead{}, fmt.Errorf("%w: project lacks a usable %s", ErrIssueMetadataInvalid, fact.name)
		}
	}
	out.Native = bytes.Clone(data)
	return out, nil
}
