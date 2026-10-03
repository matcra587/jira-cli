package alias

import (
	"fmt"
	"strconv"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func renderList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AliasListOutput)
	if !ok {
		return fmt.Errorf("alias.list: unexpected data %T", env.Data)
	}
	v.Line("Config: " + cli.Text(out.Path))
	if out.Count == 0 {
		v.Line("No aliases")
	}
	for _, name := range sortedNames(out.Aliases) {
		v.Fields(output.Field{Label: cli.Text(strconv.Quote(name)), Value: cli.Text(strconv.Quote(out.Aliases[name]))})
	}
	return nil
}

func renderSet(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AliasSetOutput)
	if !ok {
		return fmt.Errorf("alias.set: unexpected data %T", env.Data)
	}
	switch {
	case out.DryRun:
		v.Line("Would set alias " + cli.Text(out.Name))
	case out.Changed:
		v.Line("Set alias " + cli.Text(out.Name))
	default:
		v.Line("Alias unchanged: " + cli.Text(out.Name))
	}
	v.Fields(output.Field{Label: "Expansion", Value: cli.Text(out.Expansion)}, output.Field{Label: "Config", Value: cli.Text(out.Path)})
	return nil
}

func renderDelete(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AliasDeleteOutput)
	if !ok {
		return fmt.Errorf("alias.delete: unexpected data %T", env.Data)
	}
	switch {
	case !out.Deleted:
		v.Line("Alias not found: " + cli.Text(out.Name))
	case out.DryRun:
		v.Line("Would delete alias " + cli.Text(out.Name))
	default:
		v.Line("Deleted alias " + cli.Text(out.Name))
	}
	v.Fields(output.Field{Label: "Config", Value: cli.Text(out.Path)})
	return nil
}

func renderImport(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AliasImportOutput)
	if !ok {
		return fmt.Errorf("alias.import: unexpected data %T", env.Data)
	}
	if out.DryRun {
		v.Line(fmt.Sprintf("Would import %d aliases", out.Imported))
	} else {
		v.Line(fmt.Sprintf("Imported %d aliases", out.Imported))
	}
	for _, name := range out.Aliases {
		v.Line(cli.Text(name))
	}
	for _, name := range sortedNames(out.Skipped) {
		v.Fields(output.Field{Label: "Skipped " + cli.Text(name), Value: cli.Text(out.Skipped[name])})
	}
	v.Fields(output.Field{Label: "Config", Value: cli.Text(out.Path)})
	return nil
}
