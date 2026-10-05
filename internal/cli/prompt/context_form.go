package prompt

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	xslices "github.com/gechr/x/slices"
)

// Form owns one native Huh group and retains accessible field failures that
// Huh otherwise discards. A form has one caller and one reader at a time.
type Form struct {
	native *huh.Form
	ctx    context.Context //nolint:containedctx // Huh's accessible fields have no context parameter; this is set only during the synchronous run and cleared after readers join.
	err    error
}

// NewForm builds native fields with command context and shared error retention.
// Password fields must use WithPasswordContext to retain native masking.
func NewForm(fields ...huh.Field) *Form {
	form := &Form{}
	wrapped := xslices.Map(fields, func(field huh.Field) huh.Field {
		return &contextField{Field: field, form: form}
	})
	form.native = huh.NewForm(huh.NewGroup(wrapped...))
	return form
}

// WithOutput sets the native form's rendering stream.
func (f *Form) WithOutput(w io.Writer) *Form {
	f.native.WithOutput(w)
	return f
}

// WithInput sets the borrowed native input stream.
func (f *Form) WithInput(r io.Reader) *Form {
	f.native.WithInput(r)
	return f
}

// WithTheme sets the native rich and accessible styles.
func (f *Form) WithTheme(theme huh.Theme) *Form {
	f.native.WithTheme(theme)
	return f
}

// WithAccessible selects Huh's accessible input mode explicitly.
func (f *Form) WithAccessible(accessible bool) *Form {
	f.native.WithAccessible(accessible)
	return f
}

// RunWithContext runs the native form and retains each accessible read outcome.
// Unix text readers use owned pollable duplicates; other files retain native
// before/after context checks until their active interruption is implemented.
func (f *Form) RunWithContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.ctx, f.err = ctx, nil
	defer func() { f.ctx = nil }()
	err := f.native.RunWithContext(ctx)
	return errors.Join(err, f.err)
}

type contextField struct {
	huh.Field
	form *Form
}

// Native Huh fields update in place. Keep this wrapper when Huh replaces its
// group entry with the returned model, so rich updates retain the shared owner.
func (f *contextField) Update(msg tea.Msg) (huh.Model, tea.Cmd) {
	_, cmd := f.Field.Update(msg)
	return f, cmd
}

func (f *contextField) RunAccessible(w io.Writer, r io.Reader) error {
	if f.form.err != nil {
		return f.form.err
	}
	if err := f.form.ctx.Err(); err != nil {
		f.form.err = err
		return err
	}
	var err error
	if _, password := f.Field.(*PasswordInput); password {
		// Native password input bypasses Reader.Read through Fd. It needs its
		// existing adapter and original descriptor flags, without a second reader.
		err = f.Field.RunAccessible(w, r)
	} else {
		err = runAccessibleField(f.form.ctx, f.Field, w, r)
	}
	f.form.err = err
	return err
}
