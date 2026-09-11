package cmdutil_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
)

// The raw-warnings envelope path must emit single-line JSON like the typed
// path, so agent and log consumers get one envelope per line regardless of
// which writer a command happens to use.
func TestWriteEnvelopeWithRawWarningsEmitsSingleLineJSON(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetContext(cmdutil.WithDetector(context.Background(), cli.Detection{Mode: cli.ModeJSON}))
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	warnings := []map[string]any{{"type": "cache_truncated", "message": "list truncated"}}
	if err := cmdutil.WriteEnvelopeWithRawWarnings(cmd, "issue.list", map[string]any{"count": 1}, warnings); err != nil {
		t.Fatalf("WriteEnvelopeWithRawWarnings: %v", err)
	}
	body := strings.TrimRight(buf.String(), "\n")
	if strings.Contains(body, "\n") {
		t.Fatalf("raw-warnings envelope must be single-line, got:\n%s", buf.String())
	}
}

// The --all drain has no *jira.Response, so it builds pagination directly:
// after a full drain the result set is complete (isLast true) and total is
// the count actually held. Agents are told to treat meta.pagination.isLast as
// authoritative, so the drain path must carry it rather than omit it.
func TestWriteEnvelopeWithPaginationAndRawWarningsCarriesPagination(t *testing.T) {
	cases := []struct {
		name       string
		pagination *cli.Pagination
		wantLast   bool
		wantTotal  float64
	}{
		{"complete drain", &cli.Pagination{MaxResults: 3, Total: new(3), IsLast: true}, true, 3},
		{"truncated drain", &cli.Pagination{MaxResults: 2, Total: new(2), IsLast: false}, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			cmd := &cobra.Command{Use: "x"}
			cmd.SetContext(cmdutil.WithDetector(context.Background(), cli.Detection{Mode: cli.ModeJSON}))
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			err := cmdutil.WriteEnvelopeWithPaginationAndRawWarnings(cmd, "search.jql", map[string]any{"issues": []any{}}, tc.pagination, nil)
			if err != nil {
				t.Fatalf("WriteEnvelopeWithPaginationAndRawWarnings: %v", err)
			}
			var env struct {
				Meta struct {
					Pagination *struct {
						Total  float64 `json:"total"`
						IsLast bool    `json:"isLast"`
					} `json:"pagination"`
				} `json:"meta"`
			}
			if uerr := json.Unmarshal(buf.Bytes(), &env); uerr != nil {
				t.Fatalf("unmarshal envelope: %v\n%s", uerr, buf.String())
			}
			if env.Meta.Pagination == nil {
				t.Fatalf("drain envelope must carry meta.pagination; got:\n%s", buf.String())
			}
			if env.Meta.Pagination.IsLast != tc.wantLast {
				t.Fatalf("isLast = %v, want %v", env.Meta.Pagination.IsLast, tc.wantLast)
			}
			if env.Meta.Pagination.Total != tc.wantTotal {
				t.Fatalf("total = %v, want %v", env.Meta.Pagination.Total, tc.wantTotal)
			}
		})
	}
}

// WriteEnvelopeWithRawWarnings (no pagination) must NOT invent a pagination
// block — only the drain path carries one.
func TestWriteEnvelopeWithRawWarningsOmitsPagination(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetContext(cmdutil.WithDetector(context.Background(), cli.Detection{Mode: cli.ModeJSON}))
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmdutil.WriteEnvelopeWithRawWarnings(cmd, "issue.list", map[string]any{"count": 1}, nil); err != nil {
		t.Fatalf("WriteEnvelopeWithRawWarnings: %v", err)
	}
	if strings.Contains(buf.String(), "pagination") {
		t.Fatalf("raw-warnings envelope must not carry pagination; got:\n%s", buf.String())
	}
}
