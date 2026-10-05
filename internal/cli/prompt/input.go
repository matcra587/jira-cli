package prompt

import (
	"io"
	"strings"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli"
)

// WithAccessibleDefault retains editable defaults for nonsecret input fields
// in both rich and accessible forms. Do not use it for password fields.
func WithAccessibleDefault(input *huh.Input, validate func(string) error) huh.Field {
	return &defaultInput{Input: input.Validate(validate), validate: validate}
}

// Huh v2.0.3 hides Input defaults in accessible mode and validates blank
// answers before applying them. Keep its native reader and rich field, but
// display the nonsecret default and validate the value it will actually use.
type defaultInput struct {
	*huh.Input
	validate func(string) error
}

func (f *defaultInput) RunAccessible(w io.Writer, r io.Reader) error {
	value := f.GetValue().(string)
	if value != "" {
		if err := huh.NewNote().Description("Default: "+cli.Text(value)).RunAccessible(w, r); err != nil {
			return err
		}
	}
	f.Validate(func(answer string) error {
		if strings.TrimSpace(answer) == "" {
			answer = value
		}
		return f.validate(answer)
	})
	defer f.Validate(f.validate)
	return f.Input.RunAccessible(w, r)
}
