//go:build outputaudit

package main

import native "github.com/zalando/go-keyring"

// Output audit builds run the real command tree, filesystem owners and keyring
// adapter against the upstream in-memory native boundary. They cannot consult
// the host keyring, even when migration intentionally bypasses current settings.
// Production builds exclude this file. Real provider acceptance uses nativekeyring.
func init() { native.MockInit() }
