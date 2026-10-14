//go:build !windows

package agentguides

func exportSymlinkPrivilegeUnavailable(error) bool { return false }
