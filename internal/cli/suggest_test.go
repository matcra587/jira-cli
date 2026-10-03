package cli

import (
	"reflect"
	"testing"
)

func TestSuggest(t *testing.T) {
	t.Parallel()
	candidates := []string{"output", "verbose", "quiet", "color", "version", "help"}
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "close typo", input: "outpt", want: []string{"output"}},
		{name: "transposition", input: "coolr", want: []string{"color"}},
		{name: "no match", input: "definitely-not-a-flag", want: nil},
		{name: "empty input", input: "", want: nil},
		// An exact match is not a typo; suggesting the input back is noise.
		{name: "exact match excluded", input: "output", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Suggest(tt.input, candidates); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Suggest(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestSuggestSkipsShortNames pins the length gate: names below four runes
// sit within distance 2 of each other, so suggesting them is noise.
func TestSuggestSkipsShortNames(t *testing.T) {
	t.Parallel()
	if got := Suggest("gt", []string{"go", "git"}); got != nil {
		t.Errorf("Suggest offered short names %v, want none", got)
	}
}

// TestSuggestCapsAndOrders pins the limit and closest-first ordering:
// the two distance-1 candidates win in caller order and the distance-2
// one falls off the cap.
func TestSuggestCapsAndOrders(t *testing.T) {
	t.Parallel()
	got := Suggest("liist", []string{"lists", "list", "linst"})
	want := []string{"list", "linst"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Suggest = %v, want closest-first capped %v", got, want)
	}
}

func TestForeignFlagSuggestions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		flag string
		want []string
	}{
		{name: "bare name", flag: "template", want: []string{"--output=json"}},
		{name: "dashed and cased", flag: "--No-Headers", want: []string{"--output=json"}},
		{name: "unknown flag", flag: "frobnicate", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ForeignFlagSuggestions(tt.flag); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ForeignFlagSuggestions(%q) = %v, want %v", tt.flag, got, tt.want)
			}
		})
	}
}

func TestUnknownFlagErrorClassification(t *testing.T) {
	t.Parallel()
	t.Run("near miss wins", func(t *testing.T) {
		t.Parallel()
		e := UnknownFlagError("unknown flag: --outpt", "outpt", []string{"--output"})
		if e.Code != CodeFlagUnknown || !reflect.DeepEqual(e.Suggestions, []string{"--output"}) {
			t.Errorf("got code %q suggestions %v", e.Code, e.Suggestions)
		}
	})
	t.Run("foreign without near miss", func(t *testing.T) {
		t.Parallel()
		e := UnknownFlagError("unknown flag: --template", "template", nil)
		if e.Code != CodeFlagForeign || len(e.Suggestions) == 0 {
			t.Errorf("got code %q suggestions %v, want flag_foreign with equivalents", e.Code, e.Suggestions)
		}
	})
	t.Run("plain unknown", func(t *testing.T) {
		t.Parallel()
		e := UnknownFlagError("unknown flag: --frobnicate", "frobnicate", nil)
		if e.Code != CodeFlagUnknown || e.Suggestions != nil {
			t.Errorf("got code %q suggestions %v, want bare flag_unknown", e.Code, e.Suggestions)
		}
	})
}

// TestSuggestValuesWaivesTheLengthFloor pins the split between an open
// identifier pool and a closed declared vocabulary. Suggest's minimum
// length keeps a growing set of flag and command names quiet; applied to
// a fixed six-value enum it only suppressed correct answers, so `zshh`
// returned no route forward at all — in the envelope as well as on
// screen, since both read the same suggestions.
func TestSuggestValuesWaivesTheLengthFloor(t *testing.T) {
	t.Parallel()
	shells := []string{"bash", "elvish", "fish", "nu", "pwsh", "zsh"}
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "short value below the open-set floor", input: "zshh", want: []string{"zsh"}},
		{name: "shortest value of all", input: "nuu", want: []string{"nu"}},
		{name: "long values still work", input: "basj", want: []string{"bash"}},
		{name: "nothing close enough", input: "powershell", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SuggestValues(tt.input, shells); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SuggestValues(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}

	// The floor must stay in force for the open set it was written for.
	if got := Suggest("zshh", shells); got != nil {
		t.Errorf("Suggest(%q) = %v, want the open-set floor to still suppress it", "zshh", got)
	}
}
