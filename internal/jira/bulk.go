package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	xslices "github.com/gechr/x/slices"
)

const MaxBulkMoveIssues = 1000

// IssueMoveTarget binds one authored source identity to its native destination.
type IssueMoveTarget struct {
	Source      string
	Destination string
}

// ParseIssueMovePayload validates untrusted native authoring input before
// generated wire decoding, whose response-oriented field matching is lenient.
func ParseIssueMovePayload(data []byte) (IssueBulkMovePayload, error) {
	members, err := DecodeNativeObject(data, issueBulkMovePayloadKeys)
	if err != nil {
		return IssueBulkMovePayload{}, err
	}
	if mapping, exists := members["targetToSourcesMapping"]; exists {
		if _, err := DecodeNativeObject(mapping, nil); err != nil {
			return IssueBulkMovePayload{}, fmt.Errorf("targetToSourcesMapping: %w", err)
		}
	}
	var request IssueBulkMovePayload
	if err := json.Unmarshal(data, &request); err != nil {
		return IssueBulkMovePayload{}, err
	}
	if _, err := request.MoveBindings(); err != nil {
		return IssueBulkMovePayload{}, err
	}
	return request, nil
}

// MoveTargets returns the explicit source identities, in destination order.
// Native move options stay raw. The pin misspells two required properties;
// validation uses the actual targetToSourcesMapping and issueIdsOrKeys names.
func (v IssueBulkMovePayload) MoveTargets() ([]string, error) {
	bindings, err := v.MoveBindings()
	if err != nil {
		return nil, err
	}
	return xslices.Map(bindings, func(b IssueMoveTarget) string { return b.Source }), nil
}

// MoveBindings validates the complete native mapping before returning any
// targets. Source identities can be keys or IDs; neither is silently rewritten.
func (v IssueBulkMovePayload) MoveBindings() ([]IssueMoveTarget, error) {
	for key := range v.Extra {
		if xslices.ContainsFold(issueBulkMovePayloadKeys, key) {
			return nil, errors.New("bulk move input repeats or changes the spelling of a modeled member")
		}
	}
	if !v.TargetToSourcesMapping.Set || v.TargetToSourcesMapping.Null || len(v.TargetToSourcesMapping.Value) == 0 {
		return nil, errors.New("targetToSourcesMapping must be a nonempty object")
	}
	destinations := slices.Sorted(maps.Keys(v.TargetToSourcesMapping.Value))
	var targets []IssueMoveTarget
	seen := make(map[string]struct{})
	for _, destination := range destinations {
		parts := strings.Split(destination, ",")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, errors.New("move destination must be project,issueType or project,issueType,parent")
		}
		for _, part := range parts {
			if part == "" || strings.TrimSpace(part) != part {
				return nil, errors.New("move destination components must be nonblank and have no surrounding whitespace")
			}
		}
		members, err := DecodeNativeObject(v.TargetToSourcesMapping.Value[destination], []string{"issueIdsOrKeys"})
		if err != nil {
			return nil, fmt.Errorf("move destination %q: %w", destination, err)
		}
		raw, ok := members["issueIdsOrKeys"]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, errors.New("move destination requires an explicit issueIdsOrKeys array")
		}
		var sources []string
		if err := json.Unmarshal(raw, &sources); err != nil || len(sources) == 0 {
			return nil, errors.New("issueIdsOrKeys must be a nonempty string array")
		}
		for _, source := range sources {
			if source == "" || strings.TrimSpace(source) != source {
				return nil, errors.New("move sources must be nonblank and have no surrounding whitespace")
			}
			if _, exists := seen[source]; exists {
				return nil, fmt.Errorf("move source %q appears more than once", source)
			}
			seen[source] = struct{}{}
			targets = append(targets, IssueMoveTarget{Source: source, Destination: destination})
			if len(targets) > MaxBulkMoveIssues {
				return nil, errors.New("a bulk move can contain at most 1,000 explicit issues; Jira also counts subtasks")
			}
		}
	}
	return targets, nil
}

func (v SubmittedBulkOperation) ValidateIdentity() error {
	if !v.TaskID.Set || v.TaskID.Null || v.TaskID.Value == "" || strings.TrimSpace(v.TaskID.Value) != v.TaskID.Value {
		return errors.New("bulk operation taskId is missing, null or blank")
	}
	return nil
}

// Terminal describes the job state, never whether every issue moved.
func (v BulkOperationProgress) Terminal() bool {
	switch v.Status.Value {
	case "COMPLETE", "FAILED", "CANCELLED", "DEAD": //nolint:misspell // Jira's native status uses this exact spelling.
		return v.Status.Set && !v.Status.Null
	default:
		return false
	}
}

// ValidateTask refuses wrong identities, unknown states and contradictory
// result facts. COMPLETE requires an accounted outcome for every reported
// issue; inaccessible and failed issues are outcomes, not successful moves.
func (v BulkOperationProgress) ValidateTask(taskID string) error {
	if taskID == "" || strings.TrimSpace(taskID) != taskID || !v.TaskID.Set || v.TaskID.Null || v.TaskID.Value != taskID {
		return errors.New("bulk progress taskId does not match the requested task")
	}
	if !v.Status.Set || v.Status.Null {
		return errors.New("bulk progress status is missing or null")
	}
	switch v.Status.Value {
	case "ENQUEUED", "RUNNING", "COMPLETE", "FAILED", "CANCEL_REQUESTED", "CANCELLED", "DEAD": //nolint:misspell // Jira's native status uses this exact spelling.
	default:
		return errors.New("bulk progress has an unsupported status")
	}
	if v.ProgressPercent.Set && !v.ProgressPercent.Null && (v.ProgressPercent.Value < 0 || v.ProgressPercent.Value > 100) {
		return errors.New("bulk progress percent must be between zero and 100")
	}
	if (v.TotalIssueCount.Set && !v.TotalIssueCount.Null && v.TotalIssueCount.Value < 0) ||
		(v.InvalidOrInaccessibleIssueCount.Set && !v.InvalidOrInaccessibleIssueCount.Null && v.InvalidOrInaccessibleIssueCount.Value < 0) {
		return errors.New("bulk progress issue counts must be nonnegative")
	}
	seen := make(map[int64]struct{})
	for _, id := range v.ProcessedAccessibleIssues.Value {
		if id <= 0 {
			return errors.New("bulk progress processed issue IDs must be positive")
		}
		if _, exists := seen[id]; exists {
			return errors.New("bulk progress repeats a processed issue ID")
		}
		seen[id] = struct{}{}
	}
	for id := range v.FailedAccessibleIssues.Value {
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil || number <= 0 || strconv.FormatInt(number, 10) != id {
			return errors.New("bulk progress failed issue IDs must be canonical positive integers")
		}
		if _, exists := seen[number]; exists {
			return errors.New("bulk progress lists an issue as both processed and failed")
		}
	}
	accounted := int64(len(v.ProcessedAccessibleIssues.Value)) + int64(len(v.FailedAccessibleIssues.Value))
	if v.InvalidOrInaccessibleIssueCount.Set && !v.InvalidOrInaccessibleIssueCount.Null {
		if v.InvalidOrInaccessibleIssueCount.Value > math.MaxInt64-accounted {
			return errors.New("bulk progress issue counts overflow")
		}
		accounted += v.InvalidOrInaccessibleIssueCount.Value
	}
	if v.TotalIssueCount.Set && !v.TotalIssueCount.Null && accounted > v.TotalIssueCount.Value {
		return errors.New("bulk progress outcomes exceed totalIssueCount")
	}
	if v.Status.Value == "COMPLETE" {
		if !v.TotalIssueCount.Set || v.TotalIssueCount.Null || !v.InvalidOrInaccessibleIssueCount.Set || v.InvalidOrInaccessibleIssueCount.Null || !v.ProcessedAccessibleIssues.Set || v.ProcessedAccessibleIssues.Null {
			return errors.New("completed bulk progress requires explicit issue outcome facts")
		}
		if accounted != v.TotalIssueCount.Value || (v.ProgressPercent.Set && (v.ProgressPercent.Null || v.ProgressPercent.Value != 100)) {
			return errors.New("completed bulk progress does not account for every reported issue")
		}
	}
	return nil
}
