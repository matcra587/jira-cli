package cli

import "testing"

func TestTextBlockRetainsLinesWithoutTerminalControls(t *testing.T) {
	got := TextBlock("first\x1b[31m red\x1b[0m\r\n\n  second\x00\n")
	if got != "first red\n\n  second\n" {
		t.Fatalf("multiline text changed structure or retained terminal controls: %q", got)
	}
}
