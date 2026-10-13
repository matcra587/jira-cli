package credentials_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/credentials"
)

type contextStore struct {
	matches  func(context.Context) bool
	raw      string
	calls    int
	mismatch bool
}

func (s *contextStore) inspect(ctx context.Context) error {
	s.calls++
	if !s.matches(ctx) {
		s.mismatch = true
		return errors.New("credential operation lost caller context")
	}
	return ctx.Err()
}

func (s *contextStore) Get(ctx context.Context, _ string) (string, error) {
	return s.raw, s.inspect(ctx)
}

func (s *contextStore) Set(ctx context.Context, _, raw string) error {
	if err := s.inspect(ctx); err != nil {
		return err
	}
	s.raw = raw
	return nil
}

func (s *contextStore) Delete(ctx context.Context, _ string) error {
	if err := s.inspect(ctx); err != nil {
		return err
	}
	s.raw = ""
	return nil
}

func TestCredentialOperationsPreserveCallerContext(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "active"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			if canceled {
				cancel()
			}
			record := credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "public@example.invalid", Token: "fixture-token"}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			store := &contextStore{matches: func(candidate context.Context) bool { return candidate == ctx }, raw: string(raw)}
			operations := []func() error{
				func() error { _, err := credentials.Load(ctx, store); return err },
				func() error { return credentials.Save(ctx, store, record) },
				func() error { return credentials.Clear(ctx, store) },
			}
			for _, operation := range operations {
				err := operation()
				if canceled {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("store cancellation lost: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			wantCalls := 3
			if canceled {
				// Saving now refuses cancellation before entering the store;
				// the read/clear wrappers still propagate it to their callbacks.
				wantCalls = 2
			}
			if store.calls != wantCalls || store.mismatch {
				t.Fatalf("credential contexts differed: calls=%d mismatch=%t", store.calls, store.mismatch)
			}
		})
	}
}
