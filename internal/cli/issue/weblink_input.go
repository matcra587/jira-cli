package issue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// compose resolves one immutable request before fan-out. Native members retain
// raw bytes; explicitly authored flags fill only absent members. No target URL
// is fetched, and validation completes before a Jira client is constructed.
func (f weblinkFlags) compose(ctx context.Context, inv command.Invocation) (jira.RemoteLinkRequest, jira.RemoteIssueLinkRequest, error) {
	var native jira.RemoteIssueLinkRequest
	if err := ctx.Err(); err != nil {
		return nil, native, err
	}
	request := make(jira.RemoteLinkRequest)
	if f.jsonInput != "" {
		data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, f.jsonInput, inv.Stdin, inv.StdinIsTerminal)
		if err != nil {
			return nil, native, err
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, native, weblinkInputError(err.Error())
		}
	}
	object := make(map[string]json.RawMessage)
	if raw, present := request["object"]; present {
		var err error
		object, err = jira.DecodeNativeObject(raw, []string{"url", "title", "summary", "icon", "status"})
		if err != nil {
			return nil, native, weblinkInputError(err.Error())
		}
	}
	objectChanged := false
	for _, field := range []struct {
		flag, member, value string
		object              bool
	}{
		{flag: flagURL, member: "url", value: f.url, object: true},
		{flag: flagTitle, member: "title", value: f.title, object: true},
		{flag: flagSummary, member: "summary", value: f.summary, object: true},
		{flag: flagRelationship, member: "relationship", value: f.relationship, object: false},
		{flag: flagGlobalID, member: "globalId", value: f.globalID, object: false},
	} {
		given := f.cmd != nil && f.cmd.Flags().Changed(field.flag)
		if f.jsonInput != "" && !given {
			continue
		}
		value := field.value
		if f.jsonInput == "" {
			if field.flag == flagTitle && value == "" {
				value = f.url
			}
			if value == "" && field.flag != flagURL && field.flag != flagTitle {
				continue
			}
		}
		destination := map[string]json.RawMessage(request)
		if field.object {
			destination = object
			objectChanged = true
		}
		if _, present := destination[field.member]; present {
			return nil, native, weblinkInputError(fmt.Sprintf("%s is set in the payload and by --%s; supply it once", field.member, field.flag))
		}
		raw, err := jira.MarshalNoEscape(value)
		if err != nil {
			return nil, native, err
		}
		destination[field.member] = raw
	}
	if objectChanged || request["object"] == nil {
		raw, err := jira.MarshalNoEscape(object)
		if err != nil {
			return nil, native, err
		}
		request["object"] = raw
	}
	native, err := core.ValidateRemoteLinkRequest(request)
	if err != nil {
		return nil, native, weblinkInputError(err.Error())
	}
	if err := ctx.Err(); err != nil {
		return nil, native, err
	}
	return request, native, nil
}

func weblinkInputError(message string) error {
	err := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+command.FlagJSONInput+": "+message)
	err.Field = command.FlagJSONInput
	return err
}
