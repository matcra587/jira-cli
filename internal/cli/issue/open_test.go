package issue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestOpenCanonicalTargetAndBrowserHandoff(t *testing.T) {
	t.Setenv("BROWSER", "selected-browser")
	for _, mode := range []output.Format{output.FormatJSON, output.FormatCompact, output.FormatHuman, ""} {
		t.Run(string(mode), func(t *testing.T) {
			cli.TakeWarnings()
			defer cli.TakeWarnings()
			calls := []recordedOpen{}
			siteCalls := 0
			deps := Deps{
				BrowseSite: func() (string, error) { siteCalls++; return "https://example.invalid/jira/", nil },
				JiraClient: func(ctx context.Context) (core.JiraClient, error) {
					t.Fatal("opening constructed a client")
					return nil, nil
				},
				Cache: func(ctx context.Context) (core.Cache, error) { t.Fatal("opening constructed cache"); return nil, nil },
			}
			ctx := t.Context()
			if mode != "" {
				ctx = cli.WithFormat(ctx, mode)
			}
			got, err := runOpen(ctx, deps, []string{"KEY-007,KEY-7:7"}, fakeOpener(&calls, nil))
			human := mode == output.FormatHuman
			if err != nil || got.Issue.Key != "KEY-7" || got.WebURL != "https://example.invalid/jira/browse/KEY-7" || got.Opened != human || siteCalls != 1 {
				t.Fatalf("target/handoff changed: %+v siteCalls=%d err=%v", got, siteCalls, err)
			}
			wantCalls := 0
			if human {
				wantCalls = 1
			}
			if len(calls) != wantCalls || (human && (calls[0].name != "selected-browser" || len(calls[0].args) != 1 || calls[0].args[0] != got.WebURL)) {
				t.Fatalf("browser argv or mode boundary changed: %+v", calls)
			}
			if warnings := cli.TakeWarnings(); len(warnings) != 0 {
				t.Fatalf("unexpected warning: %+v", warnings)
			}
		})
	}
}

func TestOpenRejectsTargetsBeforeDependencies(t *testing.T) {
	for _, expression := range []string{"", "KEY-0", "key-1", "KEY-1,KEY-2", "KEY-1:2", "KEY-1..OTHER-2", "KEY-1:1001", "KEY-1/../../", "KEY-1\n", "KEY-1;touch file"} {
		t.Run(expression, func(t *testing.T) {
			deps := Deps{BrowseSite: func() (string, error) { t.Fatal("invalid target resolved site"); return "", nil }}
			_, err := runOpen(humanContext(t), deps, []string{expression}, func(string, ...string) error { t.Fatal("invalid target launched browser"); return nil })
			var input *cli.InputError
			if !errors.As(err, &input) || input.Code != cli.CodeArgValueInvalid {
				t.Fatalf("target refusal lost taxonomy: %v", err)
			}
		})
	}
}

func TestOpenCancellationPreventsSiteAndBrowserEffects(t *testing.T) {
	for _, tc := range []struct {
		name       string
		duringSite bool
	}{
		{name: "before site", duringSite: false},
		{name: "during site", duringSite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(humanContext(t))
			defer cancel()
			if !tc.duringSite {
				cancel()
			}
			siteCalls := 0
			deps := Deps{BrowseSite: func() (string, error) {
				siteCalls++
				cancel()
				return "https://example.invalid", nil
			}}
			_, err := runOpen(ctx, deps, []string{"KEY-1"}, func(string, ...string) error { t.Fatal("canceled opening launched browser"); return nil })
			wantCalls := 0
			if tc.duringSite {
				wantCalls = 1
			}
			if !errors.Is(err, context.Canceled) || siteCalls != wantCalls {
				t.Fatalf("cancellation crossed dependency boundary: calls=%d err=%v", siteCalls, err)
			}
		})
	}
}

func TestOpenMissingSiteDependenciesReturnsConfigurationError(t *testing.T) {
	_, err := runOpen(humanContext(t), Deps{}, []string{"KEY-1"}, func(string, ...string) error {
		t.Fatal("missing site dependencies launched browser")
		return nil
	})
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeConfigInvalid || input.Field != "base_url" {
		t.Fatalf("missing site dependencies lost configuration error: %v", err)
	}
}

func TestOpenRetainsSiteFailureAndRejectsUnsafeBrowseTargets(t *testing.T) {
	sentinel := errors.New("settings unavailable")
	_, err := runOpen(humanContext(t), Deps{BrowseSite: func() (string, error) { return "", sentinel }}, []string{"KEY-1"}, func(string, ...string) error { t.Fatal("failed site launched browser"); return nil })
	if !errors.Is(err, sentinel) {
		t.Fatalf("site failure lost cause: %v", err)
	}
	for _, site := range []string{"", "not a URL", "https://user:password@example.invalid", "https://"} {
		t.Run(site, func(t *testing.T) {
			_, err := runOpen(humanContext(t), Deps{BrowseSite: func() (string, error) { return site, nil }}, []string{"KEY-1"}, func(string, ...string) error { t.Fatal("unsafe site launched browser"); return nil })
			var input *cli.InputError
			if !errors.As(err, &input) || (input.Code != cli.CodePathInvalid && input.Code != cli.CodeConfigInvalid) {
				t.Fatalf("unsafe site accepted or misclassified: %v", err)
			}
			if strings.Contains(err.Error(), "password") {
				t.Fatal("site failure exposed userinfo")
			}
		})
	}
}

func TestOpenBrowserFailureKeepsURLAndHonestWarning(t *testing.T) {
	t.Setenv("BROWSER", "selected-browser")
	cli.TakeWarnings()
	defer cli.TakeWarnings()
	calls := []recordedOpen{}
	got, err := runOpen(humanContext(t), Deps{BrowseSite: func() (string, error) { return "https://example.invalid", nil }}, []string{"KEY-1"}, fakeOpener(&calls, errors.New("cannot start opener")))
	warnings := cli.TakeWarnings()
	if err != nil || got.Opened || got.WebURL != "https://example.invalid/browse/KEY-1" || len(calls) != 1 || len(warnings) != 1 || warnings[0].Type != envelope.WarningBrowserOpenFailed || warnings[0].Lossy {
		t.Fatalf("browser failure lost URL or fabricated handoff: %+v warnings=%+v calls=%+v err=%v", got, warnings, calls, err)
	}
}
