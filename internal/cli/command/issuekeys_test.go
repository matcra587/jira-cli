package command

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestIssueKeyExpressionsOrderDedupAndBounds(t *testing.T) {
	t.Parallel()
	keys, err := ParseIssueKeys([]string{"KEY-003,KEY-1:3", "KEY-4..KEY-5", "KEY-5", "X-1"})
	if err != nil || !reflect.DeepEqual(keys, []string{"KEY-3", "KEY-1", "KEY-2", "KEY-4", "KEY-5", "X-1"}) {
		t.Fatalf("expression ordering/canonicalization changed: %v %v", keys, err)
	}
	keys, err = ParseIssueKeys([]string{"KEY-1:1000", "KEY-1000"})
	if err != nil || len(keys) != 1000 || keys[999] != "KEY-1000" {
		t.Fatalf("exact bound or duplicate at bound refused: %d %v", len(keys), err)
	}
	maxInt := int(^uint(0) >> 1)
	endpoint := fmt.Sprintf("KEY-%d..%d", maxInt-1, maxInt)
	keys, err = ParseIssueKeys([]string{endpoint})
	if err != nil || len(keys) != 2 || keys[1] != "KEY-"+strconv.Itoa(maxInt) {
		t.Fatalf("maximum integer endpoint overflowed: %v %v", keys, err)
	}
}

func TestIssueKeyExpressionsRefuseInvalidBeforeExpansion(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "KEY-0", "key-1", "KEY-1,", ",KEY-1", "KEY-1, KEY-2", "KEY-1\n", "KEY-1:0", "KEY-3:1", "KEY-1:OTHER-2", "KEY-1:2:3", "KEY-1..", "KEY-1:1001", "KEY-1:999999999999999999999", "KEY-1:1000,KEY-1001"} {
		t.Run(input, func(t *testing.T) {
			keys, err := ParseIssueKeys([]string{input})
			if err == nil || keys != nil || cli.MapError(err).Code != cli.CodeArgValueInvalid {
				t.Fatalf("invalid expression accepted or partial targets returned: %v %v", keys, err)
			}
		})
	}
	for _, input := range []string{"KEY-1,KEY-2", "KEY-1:1", "KEY-1..KEY-1"} {
		if _, err := ParseIssueKey(input); err == nil {
			t.Fatalf("one-key parent accepted expression %q", input)
		}
	}
}
