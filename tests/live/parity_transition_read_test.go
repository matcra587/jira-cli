//go:build live

package live

import (
	"strings"
	"testing"
)

const parityWorkflowFixture = `{"transitions":[{"id":"31","name":"Close","hasScreen":false,"isAvailable":true,"to":{"id":"100","name":"Done","statusCategory":{"key":"done"}},"fields":{},"future":{"number":900719925474099312345}}]}`

func TestParityTransitionReadPreservesEachLaneContract(t *testing.T) {
	const legacy = `{"issue":{"key":"PAIR-1"},"transitions":[{"id":"31","name":"Close","hasScreen":false}]}`
	if !parityTransitionRead([]byte(legacy), []byte(parityWorkflowFixture), "before") || !parityTransitionRead([]byte(parityWorkflowFixture), []byte(parityWorkflowFixture), "after") {
		t.Fatal("declared native and legacy workflow layouts refused")
	}
	for _, raw := range []string{`{}`, `{"transitions":null}`, strings.Replace(legacy, `"hasScreen":false`, `"hasScreen":true`, 1), strings.Replace(legacy, `"hasScreen":false`, `"missing":false`, 1), strings.Replace(legacy, `"id":"31"`, `"id":"32"`, 1)} {
		if parityTransitionRead([]byte(raw), []byte(parityWorkflowFixture), "before") {
			t.Fatal("legacy projection lost a native identity or screen state")
		}
	}
	if parityTransitionRead([]byte(legacy), []byte(parityWorkflowFixture), "after") || parityTransitionRead([]byte(strings.Replace(parityWorkflowFixture, "900719925474099312345", "900719925474099300000", 1)), []byte(parityWorkflowFixture), "after") {
		t.Fatal("current workflow read silently accepted legacy metadata loss or changed precision")
	}
	args := []string{"issue", "transition", "PAIR-1"}
	if parityExpectedOperation("before", args, 0) != "issue.transitions" || parityExpectedOperation("before", args, 3) != "issue.transition" || parityExpectedOperation("after", []string{"issue", "transition", "list", "PAIR-1"}, 0) != "issue.transition.list" {
		t.Fatal("relocated workflow attribution lost its argv/outcome distinction")
	}
}
