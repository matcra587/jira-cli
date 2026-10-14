//go:build !linux && !windows

package prompt

import (
	"context"
	"io"

	"charm.land/huh/v2"
)

func runAccessiblePassword(_ context.Context, input *huh.Input, w io.Writer, r io.Reader) error {
	return input.RunAccessible(w, r)
}
