package boardscope

import (
	"context"
	stdliberrors "errors"
	"fmt"
	"strconv"
	"strings"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/matcra587/jira-cli/internal/errtax"
	"github.com/spf13/cobra"

	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/cache"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/jql"
)

// The three values the envelope's data.precedence field carries when a
// --board scope is in play.
const (
	precedenceFlag    = "flag"
	precedenceDefault = "default_board"
	precedenceNone    = "none"
)

// ValidationError is a typed error that asks the error mapper to classify the
// failure as a validation error (exit 3) — used for both default-board-missing
// and ambiguous-name disambiguation. Its CandidateRows method lets the
// envelope writer surface disambiguation choices.
type ValidationError struct {
	Msg        string
	Candidates []map[string]any
}

func (e ValidationError) Error() string { return e.Msg }

// Code varies with state, as the taxonomy contract requires: multiple
// boards matched → board_ambiguous, so the caller picks from candidates[];
// a candidate-less failure (a configured default_board that no longer
// resolves) keeps the validation_failed catch-all and its canonical hint.
func (e ValidationError) Code() errtax.Code {
	if len(e.Candidates) > 0 {
		return errtax.CodeBoardAmbiguous
	}
	return errtax.CodeValidationFailed
}

// CandidateRows surfaces the disambiguation rows for the envelope's
// candidates[] field. The method is CandidateRows (not Candidates)
// because Candidates is a field.
func (e ValidationError) CandidateRows() []map[string]any { return e.Candidates }

// The value forms pin the intended value receivers.
var (
	_ errtax.Coded      = ValidationError{}
	_ errtax.Candidated = ValidationError{}
)

// FromFlags reads the active command's `--board NAME` / `--board-id N` flag
// values, falls through to the active profile's `default_board` config (when
// defined), resolves the requested board against the cache, and returns the
// resulting (scope, precedence, err).
//
// Precedence rules:
//
//	--board NAME explicit → flag wins, precedence "flag"
//	--board-id N  explicit → flag wins, precedence "flag"
//	--board ""    explicit → suppresses default, precedence "none"
//	neither + default set → resolves default, precedence "default_board"
//	neither + no default  → no scope, precedence "none"
func FromFlags(cmd *cobra.Command) (jira.BoardScope, string, error) {
	flags := cmd.Flags()

	// Distinguish "flag absent" from "flag present with empty value" —
	// the latter explicitly suppresses any configured default_board.
	var (
		boardName    string
		boardNameSet bool
		boardID      int
		boardIDSet   bool
	)
	if f := flags.Lookup("board"); f != nil {
		boardName = f.Value.String()
		boardNameSet = f.Changed
	}
	if f := flags.Lookup("board-id"); f != nil {
		raw := f.Value.String()
		if f.Changed && raw != "" {
			boardIDSet = true
			if v, err := strconv.Atoi(raw); err == nil {
				boardID = v
			}
		}
	}

	// Mutual exclusion is enforced by cobra at parse time via
	// MarkFlagsMutuallyExclusive; defensive double-check kept here so a
	// future caller wiring the flags without that bind still gets a
	// useful error.
	if boardNameSet && boardIDSet {
		return jira.BoardScope{}, precedenceNone, fmt.Errorf("--board and --board-id are mutually exclusive")
	}

	// Resolve config (active profile) — needed for the default_board
	// fallthrough AND to drive the resolver. Failing config load is
	// non-fatal here: we treat it as "no default" and only return an
	// error if the user explicitly asked for a board.
	cfg, err := config.Load(config.WithPath(cmdutil.ConfigPath(cmd)))
	if err != nil {
		if boardIDSet || (boardNameSet && !xstrings.IsBlank(boardName)) {
			return jira.BoardScope{}, precedenceFlag, err
		}
		cfg = nil
	}
	profile := config.Profile{}
	profileName := "default"
	cacheProfile := cache.Key(profileName, "", cmdutil.CacheConfigPath(cmd))
	if cfg != nil {
		profile = cmdutil.ActiveProfile(cmd, cfg)
		if profile.Name != "" {
			profileName = profile.Name
		}
		cacheProfile = cmdutil.CacheKeyForProfile(cmd, profile)
	}

	// Flag path: --board NAME (when non-empty) or --board-id N.
	switch {
	case boardIDSet:
		scope, err := resolveByID(cmd.Context(), cacheProfile, boardID)
		if err != nil {
			return jira.BoardScope{}, precedenceFlag, err
		}
		scope.Precedence = precedenceFlag
		return scope, precedenceFlag, nil
	case boardNameSet && xstrings.IsBlank(boardName):
		// `--board ""` explicitly suppresses any default. precedence "none".
		return jira.BoardScope{}, precedenceNone, nil
	case boardNameSet:
		// ResolveOne is cache-only — no client needed, no credential
		// backend touched. Pinned by tests/unit/board_resolver_test.go
		// which fails the test if the resolver hits the network.
		svc := cmdutil.ServicesForClient(nil).Board()
		scope, err := svc.ResolveOne(cmd.Context(), cacheProfile, boardName)
		if err != nil {
			return jira.BoardScope{}, precedenceFlag, classifyErr(err)
		}
		scope.Precedence = precedenceFlag
		return scope, precedenceFlag, nil
	}

	// Default-board fallthrough — when no flag is supplied and the
	// profile carries a configured default_board, resolve that against
	// the cache. Empty / unset → no scope, precedence "none".
	if def := strings.TrimSpace(profile.DefaultBoard); def != "" {
		svc := cmdutil.ServicesForClient(nil).Board()
		scope, err := svc.ResolveOne(cmd.Context(), cacheProfile, def)
		if err != nil {
			// Pinned wording when default_board doesn't resolve.
			if stdliberrors.Is(err, jira.ErrBoardNotFound) {
				return jira.BoardScope{}, precedenceDefault, ValidationError{
					Msg: jira.DefaultBoardMissingMessage(profileName, def),
				}
			}
			return jira.BoardScope{}, precedenceDefault, classifyErr(err)
		}
		scope.Precedence = precedenceDefault
		return scope, precedenceDefault, nil
	}

	return jira.BoardScope{}, precedenceNone, nil
}

// classifyErr maps a BoardService.ResolveOne error onto the right CLI exit
// code. Ambiguous-name → validation (exit 3). The bare ErrBoardNotFound
// returned for explicit --board NAME / --board-id N remains classified as
// not_found (exit 2) by the error mapper's default substring rules.
func classifyErr(err error) error {
	if ambig, ok := stdliberrors.AsType[*jira.AmbiguousBoardError](err); ok {
		cands := make([]map[string]any, 0, len(ambig.Candidates))
		for _, b := range ambig.Candidates {
			row := map[string]any{}
			if b.ID != nil {
				row["id"] = *b.ID
			}
			if b.Name != nil {
				row["name"] = *b.Name
			}
			if b.Type != nil {
				row["type"] = *b.Type
			}
			row["project_keys"] = b.ProjectKeys
			cands = append(cands, row)
		}
		return ValidationError{Msg: err.Error(), Candidates: cands}
	}
	return err
}

// resolveByID does a numeric-id lookup against the local cache. Mirrors
// BoardService.ResolveOne but keyed off the unambiguous id rather than the
// name. Cache-only — never round-trips to the server.
func resolveByID(_ context.Context, profile string, id int) (jira.BoardScope, error) {
	entry, ok, err := cache.ReadCachedOrEmpty(profile, "boards")
	if err != nil {
		return jira.BoardScope{}, fmt.Errorf("board resolve by id: %w", err)
	}
	if !ok {
		return jira.BoardScope{}, fmt.Errorf("%w (boards cache empty — run `jira cache boards`)", jira.ErrBoardNotFound)
	}
	boards, err := jira.DecodeBoardsCache(entry.Data)
	if err != nil {
		return jira.BoardScope{}, fmt.Errorf("board resolve by id: decode cache: %w", err)
	}
	for _, b := range boards {
		if b.ID != nil && *b.ID == id {
			return jira.BoardScope{Board: b}, nil
		}
	}
	return jira.BoardScope{}, fmt.Errorf("%w: id=%d", jira.ErrBoardNotFound, id)
}

// EnvelopeData renders a BoardScope into the envelope's data.board_scope map.
// The `applied` flag is sourced from JQLClause — the single owner of the "did
// this scope produce a clause?" decision — so any future emission rule flows
// through one predicate.
func EnvelopeData(scope jira.BoardScope) map[string]any {
	_, applied := scope.JQLClause()
	resolved := (scope.Board.ID != nil && *scope.Board.ID != 0) ||
		(scope.Board.Name != nil && *scope.Board.Name != "")
	if !resolved && !applied {
		return map[string]any{"applied": false}
	}
	out := map[string]any{
		"applied":      applied,
		"project_keys": scope.Board.ProjectKeys,
	}
	if out["project_keys"] == nil {
		out["project_keys"] = []string{}
	}
	if scope.Board.ID != nil {
		out["id"] = *scope.Board.ID
	}
	if scope.Board.Name != nil {
		out["name"] = *scope.Board.Name
	}
	if scope.Board.Type != nil {
		out["type"] = *scope.Board.Type
	}
	return out
}

// ApplyClauseToJQL inserts the board scope's JQL clause into query, preserving
// any top-level ORDER BY suffix so the result stays a valid query.
func ApplyClauseToJQL(query string, scope jira.BoardScope) string {
	clause, ok := scope.JQLClause()
	if !ok {
		return query
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return clause
	}
	filter, orderBy := jql.SplitTopLevelOrderBy(q)
	return jql.CombineClauses(clause, jql.ParenthesizeIfTopLevelOR(filter)) + orderBy
}

// AddFlags wires the `--board NAME` / `--board-id N` flag pair (with mutual
// exclusion) onto a list-style command. Shared by `issue list` and `jql build`
// so the surface stays in lockstep.
func AddFlags(cmd *cobra.Command) {
	cmdutil.AddString(cmd.Flags(), "board", "", "Restrict to issues whose project belongs to the named board (case-insensitive exact match against the cache)", clib.FlagExtra{Group: "Filters", Placeholder: "NAME", Complete: "predictor=cacheboard", Terse: "board filter"})
	cmdutil.AddInt(cmd.Flags(), "board-id", 0, "Restrict to issues whose project belongs to the board with this id", clib.FlagExtra{Group: "Filters", Placeholder: "ID", Terse: "board id"})
	cmd.MarkFlagsMutuallyExclusive("board", "board-id")
}
