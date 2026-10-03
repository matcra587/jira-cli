package boards

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// UnmarshalJSON keeps required completion facts explicit for both consumers
// of this snapshot. Omitted or null flags cannot establish cache freshness.
func (s *snapshot) UnmarshalJSON(raw []byte) error {
	var facts struct {
		Complete  *bool `json:"complete"`
		Truncated *bool `json:"truncated"`
		Boards    []struct {
			ProjectsComplete *bool `json:"projects_complete"`
		} `json:"boards"`
	}
	if err := jsonv2.Unmarshal(raw, &facts); err != nil {
		return err
	}
	if facts.Complete == nil || facts.Truncated == nil {
		return protocolError("cached board snapshot lacks completion facts")
	}
	for _, board := range facts.Boards {
		if board.ProjectsComplete == nil {
			return protocolError("cached board snapshot lacks project completion facts")
		}
	}
	type nativeSnapshot snapshot
	var decoded nativeSnapshot
	if err := jsonv2.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*s = snapshot(decoded)
	return nil
}

// FetchMetadata collects the same enriched snapshot that boards list reads.
// It does not persist anything; primers own storage and previews. Keeping the
// native payload prevents a primer from silently dropping project diagnostics.
func FetchMetadata(ctx context.Context, client core.JiraClient, unbounded bool) (metacache.MetadataValue, error) {
	snapshot, err := fetchSnapshot(ctx, client, unbounded, maxPages, maxBoards)
	raw, marshalErr := jira.MarshalNoEscape(snapshot)
	if marshalErr != nil {
		return metacache.MetadataValue{}, errors.Join(err, fmt.Errorf("encode board metadata: %w", marshalErr))
	}
	return metacache.MetadataValue{
		Items: []json.RawMessage{}, Payload: raw,
		Count: len(snapshot.Boards), Pages: snapshot.PagesFetched,
		Complete: snapshot.Complete, TruncatedReason: snapshot.Reason,
		NextStart: snapshot.NextStart,
	}, errors.Join(err, ctx.Err())
}

// DecodeMetadata validates the existing board snapshot for offline inspection
// and priming. The exact owning payload and project diagnostics are retained.
func DecodeMetadata(raw []byte) (metacache.MetadataValue, error) {
	var snapshot snapshot
	if err := jsonv2.Unmarshal(raw, &snapshot); err != nil || !validSnapshot(snapshot) {
		return metacache.MetadataValue{}, protocolError("cached board snapshot is malformed")
	}
	return metacache.MetadataValue{
		Items: []json.RawMessage{}, Payload: raw,
		Count: len(snapshot.Boards), Pages: snapshot.PagesFetched,
		Complete: snapshot.Complete, TruncatedReason: snapshot.Reason,
		NextStart: snapshot.NextStart,
	}, nil
}
