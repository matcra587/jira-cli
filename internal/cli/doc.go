// Package cli holds the command layer's cross-cutting machinery — today
// the error taxonomy that turns any failure into a stable snake_case code,
// an actionable hint, and a contract exit code. Command packages live in
// subdirectories (root, and per-domain packages as they land); this parent
// package hosts what they all share.
//
// Primary entry points: MapError and ExitCode. The exit-code table is a
// published contract (0 ok … 8 output failure) — change it deliberately or
// not at all.
package cli
