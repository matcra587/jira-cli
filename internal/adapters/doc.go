// Package adapters implements core's I/O seams with concrete types: the
// system clock and the in-process metadata cache live here, and the Jira
// REST client lives in the jira subpackage. Adapters and their subpackages
// are the only code that touches I/O — commands accept the core interfaces
// and never construct their own.
//
// Constructors return concrete structs; the composition root in
// internal/cli/root wires them in, lazily, so help and completion paths
// construct nothing.
package adapters
