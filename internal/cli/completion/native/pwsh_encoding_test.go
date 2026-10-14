package native_test

import (
	"slices"
	"strconv"
	"testing"

	"github.com/gechr/clib/complete"
)

func TestPowerShellCompletionUTF8CallbacksPreserveCallerState(t *testing.T) {
	const value = "café 🧪 古い$literal'quote"
	const description = "Visible résumé 古い"
	for _, codePage := range []int{437, 28591} {
		t.Run("cp"+strconv.Itoa(codePage), func(t *testing.T) {
			for _, tc := range []struct {
				name, line  string
				generator   complete.Generator
				wantArgs    []string
				wantValue   string
				currentWord bool
			}{
				{
					name: "ordinary positional", line: "fixture ca",
					generator: complete.Generator{AppName: "fixture", DynamicArgs: []string{"value"}},
					wantArgs:  []string{value}, wantValue: value,
				},
				{
					name: "ordinary flag", line: "fixture --value ca",
					generator: complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Dynamic: "value"}}},
					wantArgs:  []string{"--value", value}, wantValue: value,
				},
				{
					name: "ordinary inline flag", line: "fixture --value=ca",
					generator: complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Dynamic: "value"}}},
					wantArgs:  []string{"--value=" + value}, wantValue: "--value=" + value,
				},
				{
					name: "ordinary csv", line: "fixture --value 'earlier,ca'",
					generator: complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, CommaList: true, Dynamic: "value"}}},
					wantArgs:  []string{"--value", "earlier," + value}, wantValue: "earlier," + value,
				},
				{
					name: "current word positional", line: "fixture café",
					generator: complete.Generator{AppName: "fixture", DynamicArgs: []string{"word_value"}},
					wantArgs:  []string{value}, wantValue: value, currentWord: true,
				},
				{
					name: "current word flag", line: "fixture --value café",
					generator: complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Dynamic: "word_value"}}},
					wantArgs:  []string{"--value", value}, wantValue: value, currentWord: true,
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					matches, args := pwshRepliesWithOptions(t, &tc.generator, tc.line, "", value+"\t"+description+"\n", pwshReplyOptions{codePage: codePage})
					if len(args) == 0 || len(matches) != 1 {
						t.Fatalf("UTF-8 callback did not enter or match: args=%q matches=%#v", args, matches)
					}
					wantCallback := []string{"--@complete=value"}
					if tc.currentWord {
						wantCallback = []string{"--@shell=pwsh", "--@complete=word_value", "--", "--@word=café", "--"}
					}
					if !slices.Equal(args, wantCallback) {
						t.Fatalf("callback changed literal protocol or Unicode prefix: got=%q want=%q", args, wantCallback)
					}
					match := matches[0]
					if !match.Literal || match.Tooltip != description || !slices.Equal(match.Args, []string{tc.wantValue}) || !slices.Equal(match.CompletedArgs, tc.wantArgs) {
						t.Fatalf("UTF-8 completion changed identity, tooltip or executed argv: %#v", match)
					}
				})
			}
		})
	}
}

func TestPowerShellCompletionRestoresEncodingWithoutCandidates(t *testing.T) {
	for _, codePage := range []int{437, 28591} {
		t.Run("cp"+strconv.Itoa(codePage), func(t *testing.T) {
			for _, tc := range []struct {
				name, line string
				options    pwshReplyOptions
				entered    bool
			}{
				{name: "callback failure", line: "fixture ca", options: pwshReplyOptions{failCallback: true}, entered: true},
				{name: "empty reply", line: "fixture ca", entered: true},
				{name: "refused context", line: "fixture $unknown ca"},
				{name: "callback exception", line: "fixture ca", options: pwshReplyOptions{throwCallback: true}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					tc.options.codePage = codePage
					generator := &complete.Generator{AppName: "fixture", DynamicArgs: []string{"value"}}
					matches, args := pwshRepliesWithOptions(t, generator, tc.line, "", "", tc.options)
					if len(matches) != 0 || (len(args) != 0) != tc.entered {
						t.Fatalf("callback outcome or admission differs: matches=%#v args=%q", matches, args)
					}
				})
			}
		})
	}
}

func TestPowerShellStaticCompletionPreservesHostEncoding(t *testing.T) {
	const value = "café 🧪 古い"
	for _, codePage := range []int{437, 28591} {
		t.Run("cp"+strconv.Itoa(codePage), func(t *testing.T) {
			generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Values: []string{value}}}}
			matches, args := pwshRepliesWithOptions(t, generator, "fixture --value ca", "", "", pwshReplyOptions{codePage: codePage})
			if len(args) != 0 || len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].CompletedArgs, []string{"--value", value}) {
				t.Fatalf("static completion invoked a callback or changed literal identity: matches=%#v args=%q", matches, args)
			}
		})
	}
}
