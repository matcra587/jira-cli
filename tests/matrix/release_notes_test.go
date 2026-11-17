//go:build flagmatrix

package matrix

import (
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestReleaseNotesLatestSelectsOneEmbeddedRelease(t *testing.T) {
	t.Parallel()
	history := mx4Run(t, mx4Opts{}, "release-notes", "--output=json", "--no-input")
	latest := mx4Run(t, mx4Opts{}, "release-notes", "--latest", "--output=json", "--no-input")
	var all, selected envelope.ReleaseNotesOutput
	mx4Data(t, mx4OK(t, history), &all)
	mx4Data(t, mx4OK(t, latest), &selected)
	if len(all.Releases) <= 1 || len(selected.Releases) != 1 || !reflect.DeepEqual(selected.Releases[0], all.Releases[0]) {
		t.Fatal("--latest did not select exactly the first embedded release")
	}
	Prove(t, "jira release-notes", "latest", "competing full-history and latest runs prove selection of exactly the newest embedded release")
}
