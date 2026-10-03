//go:build !unix

package main

// Non-Unix targets do not have Unix's default SIGPIPE termination behavior.
func configureOutputSignals() {}
