package issue

import (
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/output"
)

// renderBody uses the same native document renderer as local ADF review.
func renderBody(v *output.View, raw []byte) string {
	return command.RenderADF(v, raw, "see --output=json")
}
