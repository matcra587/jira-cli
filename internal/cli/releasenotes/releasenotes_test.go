package releasenotes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	changelog "github.com/matcra587/jira-cli"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestReleaseNotesSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  string
		latest bool
		tag    string
	}{
		{name: "history"},
		{name: "latest", latest: true, tag: "v0.15.2"},
		{name: "version", query: "0.7.7", tag: "v0.7.7"},
		{name: "tag", query: "v0.7.7", tag: "v0.7.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildOutput(tc.query, tc.latest)
			if err != nil {
				t.Fatal(err)
			}
			if tc.tag == "" {
				if !reflect.DeepEqual(got.Releases, changelog.Releases()) || got.Markdown != changelog.Full() {
					t.Fatal("history differs from embedded catalog")
				}
			} else {
				want, ok := changelog.Find(tc.tag)
				if !ok || len(got.Releases) != 1 || !reflect.DeepEqual(got.Releases[0], want) || got.Markdown != want.Markdown {
					t.Fatal("selected release differs from embedded artifact")
				}
			}
		})
	}
}

func TestReleaseNotesRefusalAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		query   string
		latest  bool
		message string
	}{
		{query: "missing", message: "available: 0.15.2"},
		{query: "0.7.7", latest: true, message: "pass a version or --latest, not both"},
	} {
		got, err := buildOutput(tc.query, tc.latest)
		classification := cli.MapError(err)
		if classification.Exit != 3 || classification.Code != cli.CodeArgValueInvalid || classification.Field != "version" || !strings.Contains(classification.Message, tc.message) || !reflect.DeepEqual(got, envelope.ReleaseNotesOutput{}) {
			t.Fatalf("invalid selection lacks actionable refusal: %+v", classification)
		}
	}
}

func TestReleaseNotesContextAndRendererBoundary(t *testing.T) {
	cmd := New()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cmd.SetContext(cli.WithFormat(ctx, output.FormatJSON))
	if err := cmd.RunE(cmd, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled local read: %v", err)
	}
	if err := renderHuman(output.NewView(&strings.Builder{}, cli.ResolvedColorMode()), envelope.Envelope{Data: "wrong payload"}); err == nil {
		t.Fatal("human renderer silently accepted wrong payload")
	}
}
