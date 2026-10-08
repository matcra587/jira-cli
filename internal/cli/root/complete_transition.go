package root

import (
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

// transitionCandidates offers cached IDs in target slots, never global statuses.
// Multiple explicit keys need the same ID and known destination on every issue.
// Native input and selectors leave the target role unknown and offer nothing.
func transitionCandidates(args []string) []string {
	ctx, positionals, err := completionPositionals(args)
	if err != nil || ctx.jql != "" || ctx.filter != "" || ctx.fromFile != "" || ctx.jsonInput != "" ||
		len(positionals) == 0 || len(positionals) > cachecmd.IssueKeysCap {
		return nil
	}
	keys := make([]string, len(positionals))
	for index, value := range positionals {
		key, err := command.ParseIssueKey(strings.ToUpper(strings.TrimSpace(value)))
		if err != nil {
			return nil
		}
		keys[index] = key
	}
	store, _ := completionCache(args)
	if store == nil {
		return nil
	}
	var common []cachecmd.TransitionHint
	for index, key := range keys {
		hints, _, state := cachecmd.TransitionHints(store, key)
		if state != "fresh" {
			return nil
		}
		if index == 0 {
			common = hints
			continue
		}
		available := make(map[string]cachecmd.TransitionHint, len(hints))
		for _, hint := range hints {
			available[hint.ID] = hint
		}
		retained := make([]cachecmd.TransitionHint, 0, len(common))
		for _, hint := range common {
			if other, ok := available[hint.ID]; ok && hint.StatusID != "" && other == hint {
				retained = append(retained, hint)
			}
		}
		common = retained
	}
	var candidates []string
	for _, hint := range common {
		description := hint.Name
		if hint.Status != "" && hint.Status != description {
			if description == "" {
				description = hint.Status
			} else {
				description += " (" + hint.Status + ")"
			}
		}
		candidate := hint.ID
		if description != "" {
			candidate += "\t" + cli.Text(description)
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}
