package root

import (
	"context"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

// reloadTUI reopens the invocation's pinned profile and captured environment,
// without invalidating the once-only factories used by ordinary commands.
func (s *invocationSettings) reloadTUI(ctx context.Context) (*config.Resolved, core.JiraClient, error) {
	opts, err := s.options(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	opts.Snapshot = nil
	opts, err = config.CaptureOptions(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	public, err := config.Load(opts)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := config.Resolve(ctx, opts, credentialStoreFromResolved(public))
	if err != nil {
		return nil, nil, err
	}
	client, err := jiraClientFromResolved(resolved)
	return resolved, client, err
}
