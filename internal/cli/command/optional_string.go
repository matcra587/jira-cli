package command

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// OptionalString retains actual parser presence independently of the value.
// A handler can distinguish omission from an explicitly empty string without
// holding a command or its output streams.
type OptionalString struct {
	value string
	flag  *pflag.Flag
}

// Value returns the parsed value and whether the caller supplied the option.
func (option OptionalString) Value() (string, bool) {
	return option.value, option.flag != nil && option.flag.Changed
}

// AddOptionalStringVar uses the ordinary string registration/metadata owner.
func AddOptionalStringVar(cmd *cobra.Command, option *OptionalString, name, usage string, opts FlagOpts) {
	AddStringVar(cmd, &option.value, name, "", usage, opts)
	option.flag = cmd.Flags().Lookup(name)
}
