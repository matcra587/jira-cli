package credentials_test

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type publicationStore struct {
	core.CredentialStore
	write func(context.Context) error
	calls int
}

func (s *publicationStore) Set(ctx context.Context, _, _ string) error {
	s.calls++
	return s.write(ctx)
}

func TestCredentialPublicationSeparatesRefusalAcknowledgmentAndUnknownEntry(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"canceled before", "invalid before", "known refusal", "unknown entered", "canceled unknown", "acknowledged cancellation", "success"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("synthetic private provider failure")
			store := &publicationStore{write: func(actual context.Context) error {
				if actual != ctx || actual.Err() != nil {
					t.Fatal("publication lost context or entered canceled")
				}
				switch outcome {
				case "known refusal":
					return &core.CredentialStoreError{Backend: core.CredentialBackendKeyring, Operation: "save", Cause: cause}
				case "unknown entered":
					return cause
				case "canceled unknown":
					cancel()
					return context.Canceled
				case "acknowledged cancellation":
					cancel()
				}
				return nil
			}}
			record := credentials.Record{Site: "https://example.invalid", Email: "public@example.invalid", Token: "synthetic-token"}
			if outcome == "canceled before" {
				cancel()
			}
			if outcome == "invalid before" {
				record.Token = ""
			}
			receipt, err := credentials.SaveWithPublication(ctx, store, record)
			wantSaved := outcome == "success" || outcome == "acknowledged cancellation"
			wantUnknown := outcome == "unknown entered" || outcome == "canceled unknown"
			if receipt.Saved != wantSaved || receipt.Attempted != (wantSaved || wantUnknown) || receipt.Status == "unknown" != wantUnknown {
				t.Fatalf("false credential publication: %+v %v", receipt, err)
			}
			if wantSaved && (err != nil || receipt.Status != "saved") {
				t.Fatal("acknowledgment erased by cancellation", err)
			}
			if !wantSaved && err == nil {
				t.Fatal("failed credential publication returned success")
			}
			wantCalls := 1
			if outcome == "canceled before" || outcome == "invalid before" {
				wantCalls = 0
			}
			if store.calls != wantCalls {
				t.Fatal("unstarted credential write entered or replayed")
			}
		})
	}
}
