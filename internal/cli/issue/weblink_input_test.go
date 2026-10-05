package issue

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestWeblinkNativeInputFailuresPrecedeClientConstruction(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"canceled", "deadline", "terminal", "read failure"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inv := invocation(false, "KEY-1")
			inv.Stdin = strings.NewReader(`{"object":{"url":"https://example.com","title":"Example"}}`)
			var want error
			switch name {
			case "canceled":
				cancel()
				want = context.Canceled
			case "deadline":
				reader, writer := io.Pipe()
				t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
				inv.Stdin = reader
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer deadlineCancel()
				want = context.DeadlineExceeded
			case "terminal":
				inv.StdinIsTerminal = true
			case "read failure":
				inv.Stdin = errReader{err: errors.New("fixture input failed")}
			}
			constructed := false
			deps := Deps{JiraClient: func() (core.JiraClient, error) {
				constructed = true
				return nil, errors.New("unexpected client construction")
			}}
			_, err := runWeblinkAdd(ctx, deps, inv, weblinkFlags{jsonInput: "-"}, 1)
			if err == nil || constructed || want != nil && !errors.Is(err, want) {
				t.Fatalf("native input failure lost cause or constructed client: %v, constructed=%t", err, constructed)
			}
			if want == nil {
				var input *cli.InputError
				if !errors.As(err, &input) || input.Field != "json-input" {
					t.Fatal("native input failure lost its canonical field")
				}
			}
		})
	}
}
