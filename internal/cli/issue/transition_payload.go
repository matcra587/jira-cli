package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"slices"
	"strings"

	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// transitionInput keeps the native request separate from the convenience
// comment. Only the convenience comment can use the separate POST fallback;
// native update operations retain their authored single-request semantics.
type transitionInput struct {
	payload     jira.IssueWriteRequest
	target      string
	targetExtra map[string]json.RawMessage
	body        command.Body
	native      bool
}

func readTransitionInput(ctx context.Context, flags *command.BodyFlags, stdin io.Reader, terminal bool) (transitionInput, error) {
	var input transitionInput
	if flags.JSONInput == "" {
		body, err := flags.Resolve(ctx, stdin, terminal)
		input.body = body
		return input, err
	}
	data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, flags.JSONInput, stdin, terminal)
	if err != nil {
		return input, err
	}
	if !jsontext.Value(data).IsValid() {
		return input, transitionInputError("must contain valid JSON with unique member names")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return input, transitionInputError("must contain one JSON object")
	}
	var kind string
	if json.Unmarshal(members["type"], &kind) == nil && kind == "doc" {
		for _, name := range []string{"transition", "fields", "update", "historyMetadata", "properties"} {
			if _, present := members[name]; present {
				return input, transitionInputError("cannot mix a bare ADF document with native request members")
			}
		}
		body, err := command.ResolveADF(command.FlagJSONInput, flags.JSONInput, data)
		input.body = body
		return input, err
	}
	input.native = true
	if raw, present := members["transition"]; present {
		input.target, err = transitionInputTarget(raw)
		if err != nil {
			return input, err
		}
		if json.Unmarshal(raw, &input.targetExtra) == nil {
			delete(input.targetExtra, "id")
			delete(input.targetExtra, "name")
			for _, name := range []string{"expand", "fields", "hasScreen", "isAvailable", "isConditional", "isGlobal", "isInitial", "to"} {
				if _, present := input.targetExtra[name]; present {
					return input, transitionInputError("transition." + name + " is read-only workflow metadata; supply an id or name and request extensions")
				}
			}
		}
		delete(members, "transition")
	}
	if raw, present := members["comment"]; present {
		input.body, err = command.ResolveADF(command.FlagJSONInput, "comment", raw)
		if err != nil {
			return input, err
		}
		delete(members, "comment")
	}
	// Remove target/comment convenience members before classifying. A remaining
	// object with no request sections is the baseline flat-field route, with a
	// target supplied either in the payload or on the command line.
	native := false
	for _, name := range []string{"fields", "update", "historyMetadata", "properties"} {
		if _, present := members[name]; present {
			native = true
			break
		}
	}
	if native {
		encoded, err := jira.MarshalNoEscape(members)
		if err != nil {
			return input, err
		}
		if err := json.Unmarshal(encoded, &input.payload); err != nil {
			return input, transitionInputError(err.Error())
		}
		delete(input.payload.Extra, "transition")
	} else {
		input.payload.Fields = members
	}
	if err := validateTransitionInput(input); err != nil {
		return input, err
	}
	return input, nil
}

func transitionInputError(message string) error {
	err := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+command.FlagJSONInput+": "+message)
	err.Field = command.FlagJSONInput
	return err
}

func transitionInputTarget(raw json.RawMessage) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	var target map[string]json.RawMessage
	if json.Unmarshal(raw, &target) != nil || target == nil {
		return "", transitionInputError("transition must name one id or name")
	}
	for key := range target {
		for _, known := range []string{"id", "name"} {
			if key != known && strings.EqualFold(key, known) {
				return "", transitionInputError(fmt.Sprintf("transition member %q must use %q; supply the target once", key, known))
			}
		}
	}
	id, hasID := target["id"]
	name, hasName := target["name"]
	if hasID == hasName {
		return "", transitionInputError("transition must name one id or name")
	}
	if hasName {
		if json.Unmarshal(name, &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
		return "", transitionInputError("transition.name must be a nonempty string")
	}
	if json.Unmarshal(id, &value) == nil && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	// Keep arbitrary-sized integer spelling. Floating-point conversion can
	// truncate fractions or round a copied native ID above 2^53.
	if isTransitionID(string(id)) {
		return string(id), nil
	}
	return "", transitionInputError("transition.id must be a nonempty string or decimal integer")
}

func validateTransitionInput(input transitionInput) error {
	if raw, present := input.payload.Extra["properties"]; present {
		var properties []json.RawMessage
		if json.Unmarshal(raw, &properties) != nil || properties == nil {
			return transitionInputError("properties must be a JSON array")
		}
	}
	if raw, present := input.payload.Extra["historyMetadata"]; present {
		var history map[string]json.RawMessage
		if json.Unmarshal(raw, &history) != nil || history == nil {
			return transitionInputError("historyMetadata must be a JSON object")
		}
	}
	for name := range input.payload.Fields {
		if _, present := input.payload.Update[name]; present {
			return transitionInputError(fmt.Sprintf("field %q occurs in both fields and update; supply it once", name))
		}
	}
	if _, present := input.payload.Update["comment"]; present && len(input.body.ADF) > 0 {
		return transitionInputError("comment occurs in both update.comment and the comment input; supply it once")
	}
	for name, raw := range input.payload.Update {
		var operations []map[string]json.RawMessage
		if json.Unmarshal(raw, &operations) != nil || operations == nil {
			return transitionInputError(fmt.Sprintf("update.%s must be an array of operation objects", name))
		}
		for _, operation := range operations {
			if len(operation) == 0 {
				return transitionInputError(fmt.Sprintf("update.%s contains an empty operation", name))
			}
			if name == "comment" {
				for _, verb := range []string{"add", "edit"} {
					if value, present := operation[verb]; present {
						var comment map[string]json.RawMessage
						if json.Unmarshal(value, &comment) != nil || comment == nil {
							return transitionInputError("update.comment must contain native comment objects")
						}
						if body, present := comment["body"]; present {
							if _, err := command.ResolveADF(command.FlagJSONInput, "update.comment.body", body); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}
	return nil
}

// transitionInputArgs resolves payload target grammar before selector I/O.
// An explicit payload target makes every positional an issue key, as --transition
// does. Conflicting explicit targets are refused rather than resolved by Jira.
func transitionInputArgs(args []string, flagTarget, payloadTarget string, selector bool) ([]string, string, error) {
	if payloadTarget == "" {
		return splitPositionals(args, flagTarget, selector)
	}
	if flagTarget != "" && !strings.EqualFold(flagTarget, payloadTarget) {
		return nil, "", transitionInputError(fmt.Sprintf("transition target conflicts with --%s; supply one target or align the values", flagTransition))
	}
	return splitPositionals(args, payloadTarget, selector)
}

func transitionFieldConflicts(payload jira.IssueWriteRequest, resolution, assignee string) error {
	if xstrings.AllEmpty(resolution, assignee) {
		return nil
	}
	keys := payload.Keys()
	for _, field := range []struct{ name, value string }{{"resolution", resolution}, {"assignee", assignee}} {
		if field.value == "" {
			continue
		}
		for _, name := range keys {
			if field.name == name {
				return transitionInputError(fmt.Sprintf("%s is set in the payload and by --%s; supply it once", field.name, field.name))
			}
		}
	}
	return nil
}

func transitionRequest(plan transitionPlan, id string, atomic bool) jira.TransitionRequest {
	req := jira.TransitionRequest{Transition: jira.TransitionRef{ID: id, Extra: plan.targetExtra}, Payload: &plan.payload}
	if plan.resolution != "" || plan.assignee != "" {
		req.Fields = transitionFields(plan)
	}
	if atomic && plan.wantsComment() {
		req.Update = &jira.TransitionUpdate{Comment: []jira.CommentOp{{Add: jira.CommentAdd{Body: json.RawMessage(plan.comment)}}}}
	}
	return req
}

func localTransitionPayload(plan transitionPlan) (json.RawMessage, error) {
	raw, err := transitionRequest(plan, plan.want, true).MarshalJSON()
	if err != nil || isTransitionID(plan.want) {
		return raw, err
	}
	var payload jira.IssueWriteRequest
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	var target map[string]json.RawMessage
	if err := json.Unmarshal(payload.Extra["transition"], &target); err != nil {
		return nil, err
	}
	delete(target, "id")
	target["name"], err = jira.MarshalNoEscape(plan.want)
	if err != nil {
		return nil, err
	}
	payload.Extra["transition"], err = jira.MarshalNoEscape(target)
	if err != nil {
		return nil, err
	}
	return payload.MarshalJSON()
}

// validateTransitionFields checks only returned workflow metadata. Jira owns
// allowed values and field-specific validators; a missing required input or an
// out-of-screen field is refused before any transition or fallback comment.
func validateTransitionFields(key string, selected jira.IssueTransition, plan transitionPlan) error {
	if !selected.HasScreen.Value || !selected.Fields.Set {
		return nil
	}
	request := transitionRequest(plan, selected.ID.Value, true)
	raw, err := request.MarshalJSON()
	if err != nil {
		return err
	}
	var payload jira.IssueWriteRequest
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	for _, name := range payload.Keys() {
		if _, allowed := selected.Fields.Value[name]; !allowed {
			return transitionInputError(fmt.Sprintf("field %q is not on transition %s's screen; inspect `jira issue transition list %s`", name, selected.ID.Value, key))
		}
	}
	var missing []string
	for name, metadata := range selected.Fields.Value {
		var field struct {
			Required        bool            `json:"required"`
			HasDefaultValue *bool           `json:"hasDefaultValue"`
			DefaultValue    json.RawMessage `json:"defaultValue"`
		}
		if err := json.Unmarshal(metadata, &field); err != nil {
			return fmt.Errorf("%w: invalid transition field default metadata", core.ErrTransitionReadInvalid)
		}
		if !field.Required {
			continue
		}
		if value, present := payload.Fields[name]; present {
			if transitionValuePresent(value) {
				continue
			}
		} else if value, present := payload.Update[name]; present {
			if transitionValuePresent(value) {
				continue
			}
		} else if (field.HasDefaultValue != nil && *field.HasDefaultValue) ||
			(field.HasDefaultValue == nil && transitionValuePresent(field.DefaultValue)) {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return transitionInputError("required transition fields have no supplied value or returned default: " + strings.Join(missing, ", ") + "; supply native fields/update through --json-input and inspect `jira issue transition list " + key + "`")
	}
	return nil
}

func transitionValuePresent(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || string(value) == "null" || string(value) == `""` {
		return false
	}
	if value[0] == '[' {
		var values []json.RawMessage
		return json.Unmarshal(value, &values) == nil && len(values) > 0
	}
	return true
}
