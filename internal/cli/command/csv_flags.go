package command

import (
	"encoding/csv"
	"errors"
	"strings"

	"github.com/spf13/pflag"
)

// preserveCSVInput validates before pflag v1.0.10's single-record parser can
// discard later records or normalize line endings. x/strings.SplitCSV is not
// equivalent: it trims values and does not preserve CSV quoting. Keep pflag's
// parsing, defaults, repetition, metadata and literal SliceValue operations.
func preserveCSVInput(flag *pflag.Flag) {
	flag.Value = csvSliceValue{Value: flag.Value, SliceValue: flag.Value.(pflag.SliceValue)}
}

type csvSliceValue struct {
	pflag.Value
	pflag.SliceValue
}

func (v csvSliceValue) Set(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		fields, err := csv.NewReader(strings.NewReader(value)).Read()
		if err != nil {
			return err
		}
		newlines, returns := strings.Count(value, "\n"), strings.Count(value, "\r")
		for _, field := range fields {
			newlines -= strings.Count(field, "\n")
			returns -= strings.Count(field, "\r")
		}
		// A quoted literal LF or CR survives unchanged. Record separators,
		// skipped blank lines and normalized CRLF do not; refuse atomically.
		if newlines != 0 || returns != 0 {
			return errors.New("csv parsing would discard or normalize line endings; quote literal newlines, repeat the flag, or use the command's literal input route")
		}
	}
	return v.Value.Set(value)
}
