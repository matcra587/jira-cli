package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestResolveTargetsExpandsExplicitKeysBeforeClientEntry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{"repeated", []string{"KEY-2", "KEY-1", "KEY-2"}, []string{"KEY-2", "KEY-1"}},
		{"csv", []string{"KEY-2,KEY-1,KEY-2"}, []string{"KEY-2", "KEY-1"}},
		{"overlapping-ranges", []string{"KEY-2:3", "KEY-1..KEY-2", "KEY-3"}, []string{"KEY-2", "KEY-3", "KEY-1"}},
		{"canonical-number", []string{"KEY-0002", "KEY-2"}, []string{"KEY-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.args)
			client := func(context.Context) (core.JiraClient, error) {
				t.Fatal("local targets constructed a client")
				return nil, nil
			}
			selected, targets, err := ResolveTargets(t.Context(), client, SelectorFlags{}, tc.args, false, false, nil, false)
			if err != nil || selected != nil || targets.Summary != nil || !reflect.DeepEqual(targets.Keys, tc.want) || !reflect.DeepEqual(tc.args, original) {
				t.Fatalf("explicit targets lost canonical first-occurrence order: targets=%+v err=%v", targets, err)
			}
			tc.args[0] = "changed-after-resolution"
			if !reflect.DeepEqual(targets.Keys, tc.want) {
				t.Fatal("resolved targets retained the caller's mutable argument storage")
			}
		})
	}
}

func TestResolveTargetsRefusesCompleteInvalidExpressionBeforeClientEntry(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"KEY-2,", "KEY-2:1", "KEY-2..OTHER-3", "KEY-1:1001", "KEY-0", "not a key"} {
		t.Run(expression, func(t *testing.T) {
			client := func(context.Context) (core.JiraClient, error) {
				t.Fatal("invalid targets constructed a client")
				return nil, nil
			}
			selected, targets, err := ResolveTargets(t.Context(), client, SelectorFlags{}, []string{"KEY-1", expression}, true, false, nil, false)
			if err == nil || selected != nil || len(targets.Keys) != 0 || targets.Summary != nil || cli.MapError(err).Code != cli.CodeArgValueInvalid {
				t.Fatalf("malformed late expression retained partial targets: targets=%+v err=%v", targets, err)
			}
		})
	}
}

func TestResolveTargetsCancellationPrecedesExplicitPublication(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	selected, targets, err := ResolveTargets(ctx, func(context.Context) (core.JiraClient, error) {
		t.Fatal("canceled targets constructed a client")
		return nil, nil
	}, SelectorFlags{}, []string{"KEY-1", "KEY-1"}, true, false, nil, false)
	if !errors.Is(err, context.Canceled) || selected != nil || len(targets.Keys) != 0 || targets.Summary != nil {
		t.Fatalf("canceled resolution published targets: targets=%+v err=%v", targets, err)
	}
}

func TestResolveTargetsFileKeepsLiteralUniqueLines(t *testing.T) {
	t.Parallel()
	const text = "KEY-2\nKEY-1\nKEY-2 # repeat\n\n# note\nKEY-1,KEY-3\nopaque target\nKEY-1:3\nopaque target\n"
	path := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	selected, targets, err := ResolveTargets(t.Context(), func(context.Context) (core.JiraClient, error) {
		t.Fatal("file targets constructed a client")
		return nil, nil
	}, SelectorFlags{FromFile: path}, nil, true, false, nil, false)
	want := []string{"KEY-2", "KEY-1", "KEY-1,KEY-3", "opaque target", "KEY-1:3"}
	if err != nil || selected != nil || targets.Summary == nil || !targets.Summary.Resolved || targets.Summary.Kind != "from-file" || targets.Summary.Query != path || !reflect.DeepEqual(targets.Keys, want) {
		t.Fatalf("file lines lost literal first-occurrence identity: targets=%+v err=%v", targets, err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != text {
		t.Fatal("file resolution changed its authored source")
	}
}

func TestResolveTargetsFileCapCountsUniqueLiteralTargets(t *testing.T) {
	t.Parallel()
	for _, count := range []int{100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var text strings.Builder
			want := make([]string, count)
			for i := range count {
				want[i] = fmt.Sprintf("KEY-%d", i+1)
				fmt.Fprintf(&text, "%s\n%s\n", want[i], want[i])
			}
			path := filepath.Join(t.TempDir(), "keys.txt")
			if err := os.WriteFile(path, []byte(text.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			selected, targets, err := ResolveTargets(t.Context(), func(context.Context) (core.JiraClient, error) {
				t.Fatal("file cap constructed a client")
				return nil, nil
			}, SelectorFlags{FromFile: path}, nil, true, false, nil, false)
			if count == 100 {
				if err != nil || selected != nil || !reflect.DeepEqual(targets.Keys, want) {
					t.Fatalf("duplicate lines consumed the unique-target cap: %+v %v", targets, err)
				}
			} else if err == nil || cli.MapError(err).Code != cli.CodeFlagValueInvalid || selected != nil || len(targets.Keys) != 0 {
				t.Fatalf("overflow published partial file targets: %+v %v", targets, err)
			}
		})
	}
}
