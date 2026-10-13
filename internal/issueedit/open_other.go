//go:build !unix

package issueedit

import "os"

func openMember(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
