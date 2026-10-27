package issue

import (
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func verificationWarnings(key string, verified *envelope.VerifiedIssue, failed bool) []envelope.Warning {
	var warnings []envelope.Warning
	if failed {
		return []envelope.Warning{{Type: "verification_failed", Message: key + ": the write was acknowledged but its readback failed; inspect the issue before further changes"}}
	}
	if verified == nil {
		return warnings
	}
	for _, field := range verified.Fields {
		switch field.Outcome {
		case "differs":
			warnings = append(warnings, envelope.Warning{Type: "field_value_differs", Message: fmt.Sprintf("%s: field %q differs in the later readback (%s); inspect observed values before further changes", key, field.Field, field.Reason)})
		case "unverified":
			warnings = append(warnings, envelope.Warning{Type: "field_verification_incomplete", Message: fmt.Sprintf("%s: field %q remains unverified (%s); acknowledgement does not prove its stored value", key, field.Field, field.Reason)})
		}
	}
	return warnings
}

func createVerificationWarnings(out envelope.IssueCreateOutput) []envelope.Warning {
	key := "created issue"
	if out.Issue != nil {
		key = out.Issue.Key
	}
	return verificationWarnings(key, out.Verified, out.VerifyFailed)
}

func editVerificationWarnings(out envelope.IssueEditOutput) []envelope.Warning {
	var warnings []envelope.Warning
	for _, row := range out.Results {
		if row.Data != nil {
			warnings = append(warnings, verificationWarnings(row.Issue.Key, row.Data.Verified, row.Data.VerifyFailed)...)
		}
	}
	return warnings
}

func renderVerificationFields(v *output.View, verified *envelope.VerifiedIssue) {
	var fields []output.Field
	if verified.Summary.Set {
		fields = append(fields, output.Field{Label: "Summary", Value: cli.OrDash(cli.Text(verified.Summary.Value))})
	}
	if verified.Status.Set {
		fields = append(fields, output.Field{Label: "Status", Value: cli.OrDash(cli.Text(verified.Status.Value))})
	}
	if verified.Updated.Set {
		fields = append(fields, output.Field{Label: "Updated", Value: cli.OrDash(cli.Time(cli.Text(verified.Updated.Value)))})
	}
	if len(fields) == 0 {
		v.Line("Readback metadata unavailable.")
	} else {
		v.Fields(fields...)
	}
	var matches, differs, unverified int
	for _, field := range verified.Fields {
		switch field.Outcome {
		case "matches":
			matches++
		case "differs":
			differs++
		default:
			unverified++
		}
	}
	if len(verified.Fields) != 0 {
		v.Line(cli.Text(fmt.Sprintf("Field readback: %d match, %d differ, %d unverified.", matches, differs, unverified)))
	}
}
