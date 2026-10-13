package issue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestMoveFieldsConfirmationBeforeClient(t *testing.T) {
	for _, tt := range []struct {
		name                                string
		preview, interactive, accept, force bool
		wantCode                            string
		wantWrites, wantPrompts             int
	}{
		{name: "preview", preview: true},
		{name: "headless", wantCode: cli.CodeFlagRequired},
		{name: "declined", interactive: true, wantCode: cli.CodeCanceled, wantPrompts: 1},
		{name: "accepted", interactive: true, accept: true, wantWrites: 1, wantPrompts: 1},
		{name: "forced", force: true, wantWrites: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeWriteClient{}
			clients, prompts := 0, 0
			deps := Deps{
				JiraClient: func(ctx context.Context) (core.JiraClient, error) { clients++; return client, nil },
				Confirm:    func(_ context.Context, q prompt.Confirm) (bool, error) { prompts++; return tt.accept, nil },
			}
			inv := command.Invocation{Args: []string{"KEY-1"}, DryRun: tt.preview, Interactive: tt.interactive, Stdin: strings.NewReader(`{"fields":{"issuetype":{"id":"10001"}}}`)}
			out, err := runEdit(t.Context(), deps, inv, &issueWriteFlags{jsonInput: "-"}, editOptions{fieldMove: true, force: tt.force, parallelism: 1, notify: true})
			if (err == nil) != (tt.wantCode == "") || (err != nil && cli.MapError(err).Code != tt.wantCode) {
				t.Fatalf("wrong confirmation outcome: %v", err)
			}
			if clients != tt.wantWrites || len(client.edited) != tt.wantWrites || prompts != tt.wantPrompts {
				t.Fatalf("confirmation reached client/write prematurely: clients=%d writes=%d prompts=%d", clients, len(client.edited), prompts)
			}
			if err == nil && (len(out.Results) != 1 || !out.Results[0].OK || out.DryRun != tt.preview) {
				t.Fatalf("lost shared edit receipt: %+v", out)
			}
		})
	}
}

func TestMoveFieldsCanceledContextNeverWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runEdit(ctx, Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		t.Error("canceled operation constructed client")
		return nil, errors.New("unexpected client")
	}}, command.Invocation{Args: []string{"KEY-1"}, Stdin: strings.NewReader(`{"fields":{"summary":"Change"}}`)}, &issueWriteFlags{jsonInput: "-"}, editOptions{fieldMove: true, force: true, parallelism: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
