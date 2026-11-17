package issue

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestBulkCommentProgressPreservesRequestsAndPartialResults(t *testing.T) {
	keys := []string{"DEMO-1", "DEMO-2", "DEMO-3"}
	newClient := func() *fakeCommentAddClient {
		return &fakeCommentAddClient{failures: map[string]error{
			"DEMO-2": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Fixture missing"}},
		}}
	}
	options := commentAddOptions{body: bodyFromADF(t, adfBody), parallelism: 3}
	baseline := newClient()
	want, err := runCommentAdd(t.Context(), commentAddDeps(baseline), invocation(false, keys...), options)
	if err != nil {
		t.Fatal(err)
	}
	var observed []core.Progress
	ctx := core.WithProgressObserver(t.Context(), func(progress core.Progress) { observed = append(observed, progress) })
	client := newClient()
	got, err := runCommentAdd(ctx, commentAddDeps(client), invocation(false, keys...), options)
	wantJSON, marshalErr := jira.MarshalNoEscape(want)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	gotJSON, marshalErr := jira.MarshalNoEscape(got)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err != nil || !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("observation changed requests or receipts: %v\ngot %s\nwant %s", err, gotJSON, wantJSON)
	}
	gotKeys, wantKeys := client.postedKeys(), baseline.postedKeys()
	slices.Sort(gotKeys)
	slices.Sort(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("observation changed submitted targets: got %v, want %v", gotKeys, wantKeys)
	}
	if len(observed) != 4 {
		t.Fatalf("progress has %d observations, want initial plus three completed results", len(observed))
	}
	for i, progress := range observed {
		if progress.Phase != core.ProgressBulkResults || progress.Total != 3 || progress.Completed != int64(i) || progress.Complete != (i == 3) {
			t.Fatalf("progress did not follow completed result rows: %+v", observed)
		}
	}
	if observed[3].Failed != 1 {
		t.Fatalf("partial failure disappeared from progress: %+v", observed[3])
	}
}
