package contract

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestNativeCompletionArgumentObserver is reached only through an owned shell
// fixture. Completion callbacks run the actual CLI; accepted commands record
// literal argv here instead of uploading files or writing configuration.
func TestNativeCompletionArgumentObserver(t *testing.T) {
	if os.Getenv("JIRA_TEST_COMPLETION_OBSERVER") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 {
		os.Exit(21)
	}
	encoded, err := json.Marshal(os.Args[index+1:])
	if err != nil || os.WriteFile(os.Getenv("JIRA_TEST_COMPLETION_RECEIPT"), encoded, 0o600) != nil {
		os.Exit(22)
	}
	os.Exit(0)
}
