package command

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// AddJQLFilterVar preserves CSV and repeated-flag syntax while refusing empty
// input and controls before parsing. pflag v1.0.10 reads only the first CSV
// record and can discard newlines and everything after them before validation.
func AddJQLFilterVar(cmd *cobra.Command, values *[]string, name, usage string, opts FlagOpts) {
	if opts.Complete == "" {
		switch name {
		case "project":
			opts.Complete = "predictor=cacheproject,comma"
		case "key":
			opts.Complete = "predictor=issuekey,comma"
		case "epic":
			opts.Complete = "predictor=cacheepic,comma"
		case "status":
			opts.Complete = "predictor=cachestatus,comma"
		case "type":
			opts.Complete = "predictor=cacheissuetype,comma"
		case "label":
			opts.Complete = "predictor=cachelabel,comma"
		case "priority":
			opts.Complete = "predictor=cachepriority,comma"
		}
	}
	AddStringSliceVar(cmd, values, name, nil, usage, opts)
	flag := cmd.Flags().Lookup(name)
	flag.Value = jqlSliceValue{Value: flag.Value, SliceValue: flag.Value.(pflag.SliceValue)}
}

type jqlSliceValue struct {
	pflag.Value
	pflag.SliceValue
}

func (v jqlSliceValue) Set(value string) error {
	if _, err := cleanJQLValue(value); err != nil {
		return err
	}
	return v.Value.Set(value)
}

func (v jqlSliceValue) Append(value string) error {
	if _, err := cleanJQLValue(value); err != nil {
		return err
	}
	return v.SliceValue.Append(value)
}

func (v jqlSliceValue) Replace(values []string) error {
	for _, value := range values {
		if _, err := cleanJQLValue(value); err != nil {
			return err
		}
	}
	return v.SliceValue.Replace(values)
}
