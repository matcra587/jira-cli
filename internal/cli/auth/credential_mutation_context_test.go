package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

type interruptedStore struct {
	core.CredentialStore
	readError error
	writes    int
	deletes   int
}

func (s *interruptedStore) Get(context.Context, string) (string, error) { return "", s.readError }
func (s *interruptedStore) Set(context.Context, string, string) error   { s.writes++; return nil }
func (s *interruptedStore) Delete(context.Context, string) error        { s.deletes++; return nil }

func TestCredentialMutationDoesNotIgnoreInterruptedPriorRead(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			for _, operation := range []string{"login", "logout"} {
				t.Run(operation, func(t *testing.T) {
					h := newHarness(t, configured())
					store := &interruptedStore{readError: fmt.Errorf("fixture backend: %w", cause)}
					h.deps.Credentials = func() (core.CredentialStore, error) { return store, nil }
					var err error
					if operation == "login" {
						_, err = login(t.Context(), h.deps, identOf(configured()), "synthetic-candidate-token", false)
					} else {
						_, err = runLogout(t.Context(), h.deps, command.Invocation{}, true)
					}
					if !errors.Is(err, cause) || store.writes != 0 || store.deletes != 0 {
						t.Fatalf("interrupted prior read reached mutation: error=%v writes=%d deletes=%d", err, store.writes, store.deletes)
					}
				})
			}
		})
	}
}
