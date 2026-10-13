package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type cancellationStore struct {
	read func(context.Context) (string, error)
}

func (s cancellationStore) Get(ctx context.Context, _ string) (string, error) { return s.read(ctx) }
func (cancellationStore) Set(context.Context, string, string) error {
	panic("unexpected credential write")
}

func (cancellationStore) Delete(context.Context, string) error {
	panic("unexpected credential removal")
}

func TestCredentialResolutionRefusesCancellationDespiteExplicitToken(t *testing.T) {
	for _, tc := range []struct {
		name     string
		before   bool
		deadline bool
		during   bool
		missing  bool
		joined   bool
	}{
		{name: "canceled before", before: true},
		{name: "deadline before", before: true, deadline: true},
		{name: "wrapped backend cancellation"},
		{name: "wrapped backend deadline", deadline: true},
		{name: "joined missing and canceled", joined: true},
		{name: "joined missing and deadline", joined: true, deadline: true},
		{name: "canceled after successful store read", during: true},
		{name: "canceled with missing record", during: true, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := context.Canceled
			if tc.deadline {
				want = context.DeadlineExceeded
			}
			if tc.before {
				if tc.deadline {
					var done context.CancelFunc
					ctx, done = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
					defer done()
				} else {
					cancel()
				}
			}
			raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "public@example.invalid", Token: "synthetic-stored-token"})
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			store := cancellationStore{read: func(context.Context) (string, error) {
				reads++
				if tc.during {
					cancel()
					if tc.missing {
						return "", credentials.ErrNotStored
					}
					return string(raw), nil
				}
				if tc.joined {
					return "", errors.Join(core.ErrNoCredential, fmt.Errorf("fixture backend: %w", want))
				}
				return "", fmt.Errorf("fixture backend: %w", want)
			}}
			resolved, err := config.Resolve(ctx, config.Options{Path: filepath.Join(t.TempDir(), "missing.toml"), Environ: func() []string {
				return []string{"JIRA_BASE_URL=https://example.invalid", "JIRA_EMAIL=public@example.invalid", "JIRA_API_TOKEN=synthetic-environment-token"}
			}}, store)
			expectedReads := 1
			if tc.before {
				expectedReads = 0
			}
			if resolved != nil || !errors.Is(err, want) || reads != expectedReads {
				t.Fatalf("cancellation fell through to explicit token: resolved=%t error=%v reads=%d want=%d", resolved != nil, err, reads, expectedReads)
			}
		})
	}
}
