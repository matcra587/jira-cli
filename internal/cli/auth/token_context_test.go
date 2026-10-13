package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestTokenRefusesCanceledResolutionWithoutPrintingSecret(t *testing.T) {
	for _, before := range []bool{false, true} {
		name := "during resolution"
		if before {
			name = "before resolution"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if before {
				cancel()
			}
			calls := 0
			cmd := newToken(Deps{})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetContext(ctx)
			err := runToken(cmd, Deps{Site: func(context.Context) (SiteInfo, error) {
				calls++
				cancel()
				return SiteInfo{Token: "synthetic-token-must-not-print"}, nil
			}})
			expectedCalls := 1
			if before {
				expectedCalls = 0
			}
			if !errors.Is(err, context.Canceled) || output.Len() != 0 || calls != expectedCalls {
				t.Fatalf("canceled token read succeeded or printed: error=%v bytes=%d calls=%d", err, output.Len(), calls)
			}
		})
	}
}
