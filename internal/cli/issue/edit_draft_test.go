package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/issueedit"
	"github.com/matcra587/jira-cli/internal/jira"
)

const issueDraftNative = `{"type":"doc","version":1,"future":{"large":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"futureNode","attrs":{"nativeCase":"retained"}}]}`

type issueDraftReader struct {
	unusedJiraClient
	t      *testing.T
	scope  core.JiraScope
	native string
	calls  int
	keys   []string
}

func (c *issueDraftReader) JiraScope() core.JiraScope { return c.scope }
func (c *issueDraftReader) GetIssue(ctx context.Context, key string, fields []string, expand ...string) (core.IssueRead, error) {
	if err := ctx.Err(); err != nil {
		return core.IssueRead{}, err
	}
	if !slices.Equal(fields, []string{"description", "updated"}) || len(expand) != 0 {
		c.t.Fatal("description projection changed", fields, expand)
	}
	c.calls++
	c.keys = append(c.keys, key)
	var issue jira.IssueBean
	if err := json.Unmarshal([]byte(`{"id":"123456","key":"KEY-1","fields":{"description":`+c.native+`,"updated":null}}`), &issue); err != nil {
		c.t.Fatal(err)
	}
	return core.IssueRead{Issue: &issue}, nil
}

func draftTestDeps(t *testing.T) (Deps, *issueDraftReader, *int) {
	t.Helper()
	client := &issueDraftReader{t: t, scope: core.JiraScope{Site: "https://jira.example.test", Account: "editor@example.test", APIBaseURL: "https://jira.example.test"}, native: issueDraftNative}
	constructors := new(int)
	deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) { *constructors++; return client, nil }}
	return deps, client, constructors
}

func TestIssueDraftPreparationAndLocalInspectionUseBoundOwner(t *testing.T) {
	t.Parallel()
	deps, client, constructors := draftTestDeps(t)
	directory := filepath.Join(t.TempDir(), "draft space~$")
	out, err := runEditDraft(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, directory, false)
	if err != nil || !out.Draft.Ready || !out.Draft.DirectoryCreated || out.Source == nil || out.Changed == nil || *out.Changed || client.calls != 1 || *constructors != 1 || !bytes.Equal(out.Description, out.Source.DescriptionBytes) {
		t.Fatal("bound draft preparation failed", err, out)
	}
	source, err := os.ReadFile(out.Draft.SourceFile)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(out.Draft.MarkdownFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out.Draft.MarkdownFile, bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	inspect, err := runEditInspect(t.Context(), Deps{JiraClient: func(context.Context) (core.JiraClient, error) {
		t.Fatal("local inspection constructed credentials")
		return nil, nil
	}}, directory, false)
	if err != nil || inspect.Refreshed || inspect.Changed == nil || !*inspect.Changed || !bytes.Contains(inspect.Description, []byte("authored")) || !bytes.Contains(inspect.Description, []byte(`"nativeCase":"retained"`)) || inspect.Draft.SourcePublished || inspect.Draft.DirectoryCreated {
		t.Fatal("local inspection lost candidate or claimed publication", err, inspect)
	}
	after, err := os.ReadFile(out.Draft.SourceFile)
	if err != nil || !bytes.Equal(source, after) {
		t.Fatal("inspection changed native recovery bytes", err)
	}
}

func TestIssueDraftPreviewAndHeadlessEditorRefuseBeforeRemoteOrArtifacts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"preview", "headless edit", "existing target", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			deps, client, constructors := draftTestDeps(t)
			deps.EditorCommand = func(context.Context) ([]string, error) { t.Fatal("refusal opened editor settings"); return nil, nil }
			deps.EditFile = func(context.Context, []string, string) error { t.Fatal("refusal launched editor"); return nil }
			directory := filepath.Join(t.TempDir(), "draft")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inv := command.Invocation{Args: []string{"KEY-1"}, DryRun: mode == "preview", NoInput: true}
			if mode == "existing target" {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "canceled" {
				cancel()
			}
			out, err := runEditDraft(ctx, deps, inv, directory, mode == "headless edit")
			if mode == "preview" {
				if err != nil || !out.DryRun || out.Draft.DirectoryCreated || out.Source != nil || out.Description != nil || out.Changed != nil {
					t.Fatal("preview fabricated a native snapshot or changed artifacts", err, out)
				}
			} else if err == nil {
				t.Fatal("early refusal accepted")
			}
			if client.calls != 0 || *constructors != 0 {
				t.Fatal("pure refusal/preview constructed client")
			}
			if mode != "existing target" {
				if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("early refusal created editing files", err)
				}
			}
		})
	}
}

func TestIssueDraftRefreshValidatesLocallyAndReadsImmutableID(t *testing.T) {
	t.Parallel()
	deps, client, _ := draftTestDeps(t)
	directory := filepath.Join(t.TempDir(), "draft")
	out, err := runEditDraft(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, directory, false)
	if err != nil {
		t.Fatal(err)
	}
	client.native = strings.Replace(issueDraftNative, `9007199254740993`, `9007199254740995`, 1)
	refreshed, err := runEditInspect(t.Context(), deps, directory, true)
	if err != nil || !refreshed.Refreshed || refreshed.Current == nil || !bytes.Contains(refreshed.Description, []byte("9007199254740995")) || !slices.Equal(client.keys, []string{"KEY-1", "123456"}) {
		t.Fatal("fresh merge lost immutable identity or current state", err, refreshed)
	}
	markdown, err := os.ReadFile(out.Draft.MarkdownFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out.Draft.MarkdownFile, bytes.Replace(markdown, []byte("protected"), []byte("editable"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	before := client.calls
	invalid, err := runEditInspect(t.Context(), deps, directory, true)
	if err == nil || invalid.Description != nil || client.calls != before {
		t.Fatal("bad anchors entered fresh read or exposed a candidate", err)
	}
}

func TestIssueDraftRefreshScopeAndConflictRefuseWithoutCandidate(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"scope", "conflict"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			deps, client, _ := draftTestDeps(t)
			directory := filepath.Join(t.TempDir(), "draft")
			out, err := runEditDraft(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, directory, false)
			if err != nil {
				t.Fatal(err)
			}
			markdown, err := os.ReadFile(out.Draft.MarkdownFile)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(out.Draft.MarkdownFile, bytes.Replace(markdown, []byte("alpha"), []byte("local"), 1), 0o600); err != nil {
				t.Fatal(err)
			}
			if failure == "scope" {
				client.scope.Account = "other@example.test"
			} else {
				client.native = strings.Replace(issueDraftNative, "alpha", "remote", 1)
			}
			before := client.calls
			refused, err := runEditInspect(t.Context(), deps, directory, true)
			if err == nil || refused.Description != nil || refused.Current != nil || refused.Refreshed || refused.Changed != nil {
				t.Fatal("failed reconcile retained an unsafe candidate", err, refused)
			}
			if failure == "scope" && client.calls != before {
				t.Fatal("wrong scope entered Jira")
			}
		})
	}
}

func TestIssueDraftEditorFailureCancellationAndRepairRetainRecovery(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"success", "failure", "cancellation", "invalid"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			deps, _, constructors := draftTestDeps(t)
			directory := filepath.Join(t.TempDir(), "draft")
			prepared, err := runEditDraft(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, directory, false)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := os.ReadFile(prepared.Draft.SourceFile)
			if err != nil {
				t.Fatal(err)
			}
			markdown, err := os.ReadFile(prepared.Draft.MarkdownFile)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			marker := errors.New("editor failed after saving work")
			deps.EditorCommand = func(caller context.Context) ([]string, error) {
				if caller != ctx {
					t.Fatal("editor preference lost context")
				}
				return []string{"owned-editor", "--wait"}, nil
			}
			deps.EditFile = func(caller context.Context, args []string, path string) error {
				if caller != ctx || path != prepared.Draft.MarkdownFile || !slices.Equal(args, []string{"owned-editor", "--wait"}) {
					t.Fatal("shared editor received wrong context, argv or scratch path")
				}
				saved := bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1)
				if outcome == "invalid" {
					saved = bytes.Replace(saved, []byte("protected"), []byte("editable"), 1)
				}
				if err := os.WriteFile(path, saved, 0o600); err != nil {
					t.Fatal(err)
				}
				if outcome == "failure" {
					return marker
				}
				if outcome == "cancellation" {
					cancel()
					return context.Canceled
				}
				return nil
			}
			before := *constructors
			out, err := runEditResume(ctx, deps, command.Invocation{Interactive: true}, directory)
			if !out.Draft.EditorAttempted || out.Source == nil || *constructors != before {
				t.Fatal("resume omitted recovery or opened credentials", out)
			}
			if outcome == "success" {
				if err != nil || out.Changed == nil || !*out.Changed || !out.Draft.EditorSucceeded || !bytes.Contains(out.Description, []byte("authored")) {
					t.Fatal("successful editor lost candidate", err)
				}
			} else if err == nil || out.Description != nil || out.Changed != nil {
				t.Fatal("failed/canceled/invalid edit exposed a candidate", err)
			}
			if outcome == "failure" && !errors.Is(err, marker) {
				t.Fatal("editor cause lost", err)
			}
			if outcome == "cancellation" && cli.MapError(err).Code != "canceled" {
				t.Fatal("cancellation became generic editor failure", err)
			}
			after, readErr := os.ReadFile(prepared.Draft.SourceFile)
			saved, saveErr := os.ReadFile(prepared.Draft.MarkdownFile)
			if readErr != nil || saveErr != nil || !bytes.Equal(after, backup) || !bytes.Contains(saved, []byte("authored")) {
				t.Fatal("failed editor deleted recovery data", readErr, saveErr)
			}
		})
	}
}

func TestIssueDraftResumePreviewDoesNotLaunchOrOpenJira(t *testing.T) {
	t.Parallel()
	deps, _, constructors := draftTestDeps(t)
	directory := filepath.Join(t.TempDir(), "draft")
	prepared, err := runEditDraft(t.Context(), deps, command.Invocation{Args: []string{"KEY-1"}}, directory, false)
	if err != nil {
		t.Fatal(err)
	}
	deps.EditorCommand = func(context.Context) ([]string, error) { return []string{"owned-editor", "--wait"}, nil }
	deps.EditFile = func(context.Context, []string, string) error { t.Fatal("preview launched editor"); return nil }
	before := *constructors
	out, err := runEditResume(t.Context(), deps, command.Invocation{DryRun: true, NoInput: true}, directory)
	if err != nil || !out.DryRun || out.Draft.EditorAttempted || out.Description != nil || out.Source == nil || out.Source.IssueID != prepared.Source.IssueID || *constructors != before {
		t.Fatal("resume preview lost source or changed work", err, out)
	}
	draft, err := issueedit.Open(t.Context(), directory)
	if err != nil {
		t.Fatal("preview damaged ready draft", err)
	}
	if err := draft.Close(); err != nil {
		t.Fatal(err)
	}
}
