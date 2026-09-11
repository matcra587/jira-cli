package adfmode_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/adfmode"
)

// Mode selection precedence: flag > env > profile > default.
// Default is best-effort for read/render, strict for mutation submit.
//
// Each table case sets all four inputs and asserts the resolved mode matches
// the highest-priority *explicit* signal. Defaults are exercised by leaving
// every input zero/empty.
func TestResolveHonoursPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		flag     adfmode.FlagChoice
		env      string
		profile  *bool // nil means unset
		path     adfmode.Path
		expected adfmode.Mode
	}{
		{
			name:     "default for read path is best-effort",
			path:     adfmode.PathRead,
			expected: adfmode.ModeBestEffort,
		},
		{
			name:     "default for mutation submit is strict",
			path:     adfmode.PathMutationSubmit,
			expected: adfmode.ModeStrict,
		},
		{
			name:     "default for render is best-effort",
			path:     adfmode.PathRender,
			expected: adfmode.ModeBestEffort,
		},
		{
			name:     "profile overrides default",
			path:     adfmode.PathRead,
			profile:  new(true),
			expected: adfmode.ModeStrict,
		},
		{
			name:     "env overrides profile",
			path:     adfmode.PathMutationSubmit,
			profile:  new(true),
			env:      "false",
			expected: adfmode.ModeBestEffort,
		},
		{
			name:     "env truthy values: 1",
			path:     adfmode.PathRead,
			env:      "1",
			expected: adfmode.ModeStrict,
		},
		{
			name:     "env truthy values: yes",
			path:     adfmode.PathRead,
			env:      "yes",
			expected: adfmode.ModeStrict,
		},
		{
			name:     "env truthy values: on",
			path:     adfmode.PathRead,
			env:      "on",
			expected: adfmode.ModeStrict,
		},
		{
			name:     "env falsy values: 0",
			path:     adfmode.PathMutationSubmit,
			env:      "0",
			expected: adfmode.ModeBestEffort,
		},
		{
			name:     "env falsy values: no",
			path:     adfmode.PathMutationSubmit,
			env:      "no",
			expected: adfmode.ModeBestEffort,
		},
		{
			name:     "env falsy values: off",
			path:     adfmode.PathMutationSubmit,
			env:      "off",
			expected: adfmode.ModeBestEffort,
		},
		{
			name:     "flag overrides env (strict)",
			path:     adfmode.PathRead,
			flag:     adfmode.FlagStrict,
			env:      "false",
			expected: adfmode.ModeStrict,
		},
		{
			name:     "flag overrides env (best-effort)",
			path:     adfmode.PathMutationSubmit,
			flag:     adfmode.FlagBestEffort,
			env:      "true",
			profile:  new(true),
			expected: adfmode.ModeBestEffort,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adfmode.Resolve(adfmode.Inputs{
				Flag:    tc.flag,
				Env:     tc.env,
				Profile: tc.profile,
				Path:    tc.path,
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("got %v want %v", got, tc.expected)
			}
		})
	}
}

// FlagStrict and FlagBestEffort are mutually exclusive at the cobra layer.
// The resolver MUST surface a typed error (not silently coerce) if both are
// somehow set. Defensive contract against caller bugs.
func TestResolveRejectsMutuallyExclusiveFlags(t *testing.T) {
	_, err := adfmode.Resolve(adfmode.Inputs{
		Flag: adfmode.FlagStrict | adfmode.FlagBestEffort,
		Path: adfmode.PathRead,
	})
	if err == nil {
		t.Fatalf("expected mutex error when both flags set, got nil")
	}
}

// Unparseable env values MUST be a typed error at resolve time so the user
// sees the bad config instead of a silent fallback to default.
func TestResolveRejectsUnparseableEnv(t *testing.T) {
	_, err := adfmode.Resolve(adfmode.Inputs{
		Env:  "perhaps",
		Path: adfmode.PathRead,
	})
	if err == nil {
		t.Fatalf("expected error for unparseable env, got nil")
	}
}
