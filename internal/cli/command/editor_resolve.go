package command

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
)

// ResolveEditor returns the editor command, or an error naming what to
// set. There is deliberately no fallback: dropping someone into an
// editor they did not choose is harder to escape than to configure, and
// the error can say the exact thing to type.
func ResolveEditor(configured string) ([]string, error) {
	for _, choice := range []struct{ name, raw string }{
		{"JIRA_EDITOR", os.Getenv("JIRA_EDITOR")},
		{"editor", configured},
		{"VISUAL", os.Getenv("VISUAL")},
		{"EDITOR", os.Getenv("EDITOR")},
	} {
		name, raw := choice.name, choice.raw
		if raw == "" {
			continue
		}
		parts, splitErr := shell.Split(raw)
		if splitErr != nil || len(parts) == 0 {
			// The reason travels: an unbalanced quote is the usual cause,
			// and "could not be read as a command" alone does not say
			// which character to go and look at.
			detail := "it is empty once parsed"
			if splitErr != nil {
				detail = splitErr.Error()
			}
			return nil, editorError(name,
				fmt.Sprintf("%s is set to %q, which could not be read as a command: %s", name, raw, detail))
		}
		// Resolve the executable HERE, not at launch. Otherwise
		// `--dry-run` reports success for an editor that cannot run —
		// which is the one thing a preview exists to check — and the real
		// invocation fails inside exec with an untyped error that lands
		// on generic validation advice instead of naming the editor.
		resolved, lookErr := exec.LookPath(parts[0])
		if lookErr != nil {
			return nil, editorError(name,
				fmt.Sprintf("%s is set to %q, which was not found as an executable: %v", name, parts[0], lookErr))
		}
		parts[0] = resolved
		return parts, nil
	}
	// Nothing is set at all, so no variable is the offender — EDITOR is
	// named as the one to reach for.
	return nil, editorError("EDITOR", "no editor configured")
}

// editorError types a failure about the editor setting, carrying the
// variable the caller has to change in Field — which is not always the
// one they set, since the last resort is to name EDITOR.
func editorError(variable, msg string) *cli.InputError {
	e := cli.NewInputError(cli.CodeEditorMissing, msg)
	e.Field = variable
	return e
}
