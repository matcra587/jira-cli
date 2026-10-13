package issue

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type invalidIdentityConsumerClient struct {
	*fakeWriteClient
	user          *jira.UserDetails
	identityCalls int
	watcherWrites int
}

func (c *invalidIdentityConsumerClient) CurrentUser(context.Context) (core.UserRead, error) {
	c.identityCalls++
	return core.UserRead{User: c.user}, nil
}

func (c *invalidIdentityConsumerClient) AddWatcher(context.Context, string, string) (core.WatcherWrite, error) {
	c.watcherWrites++
	return core.WatcherWrite{}, nil
}

func (c *invalidIdentityConsumerClient) RemoveWatcher(context.Context, string, string) (core.WatcherWrite, error) {
	c.watcherWrites++
	return core.WatcherWrite{}, nil
}

func TestAccountConsumersRefuseInvalidIdentityBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	commands := []struct {
		name  string
		build func(Deps) *cobra.Command
		args  []string
	}{
		{name: "create", build: newCreate, args: []string{"--project", "KEY", "--type", "Task", "--summary", "x", "--assignee", "@me", "--reporter", "@me"}},
		{name: "edit", build: newEdit, args: []string{"KEY-1", "--assignee", "@me", "--reporter", "@me"}},
		{name: "clone", build: newClone, args: []string{"KEY-1", "--assignee", "@me", "--reporter", "@me"}},
		{name: "watchers add", build: newWatchersAdd, args: []string{"KEY-1", "--user", "@me"}},
		{name: "watchers remove", build: newWatchersRemove, args: []string{"KEY-1", "--user", "@me"}},
	}
	for _, consumer := range commands {
		t.Run(consumer.name, func(t *testing.T) {
			t.Parallel()
			for _, reply := range []struct {
				name string
				id   jira.Nullable[string]
			}{
				{name: "absent populated", id: jira.Nullable[string]{Value: "discarded-account"}},
				{name: "null populated", id: jira.Nullable[string]{Set: true, Null: true, Value: "discarded-account"}},
				{name: "blank", id: jira.NullableOf(" \t\n")},
			} {
				t.Run(reply.name, func(t *testing.T) {
					t.Parallel()
					client := &invalidIdentityConsumerClient{fakeWriteClient: &fakeWriteClient{}, user: &jira.UserDetails{AccountID: reply.id}}
					constructors := 0
					cmd := consumer.build(Deps{JiraClient: func(context.Context) (core.JiraClient, error) {
						constructors++
						return client, nil
					}})
					cmd.SetArgs(consumer.args)
					cmd.SetOut(io.Discard)
					cmd.SetErr(io.Discard)
					cmd.SilenceErrors, cmd.SilenceUsage = true, true
					err := cmd.ExecuteContext(t.Context())
					if !errors.Is(err, core.ErrCurrentUserResponseInvalid) || cli.MapError(err).Code != cli.CodeCurrentUserResponseInvalid {
						t.Fatalf("registered consumer lost malformed-account refusal: %v", err)
					}
					if constructors != 1 || client.identityCalls != 1 || len(client.created) != 0 || len(client.edited) != 0 || client.watcherWrites != 0 {
						t.Fatal("invalid identity reached a write or repeated identity resolution")
					}
				})
			}
		})
	}
}
