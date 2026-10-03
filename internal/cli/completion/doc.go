// Package completion implements `jira completion`: the visible,
// discoverable path to a shell completion script. Conductor wires
// clib's hidden completion flags but adds no command, so without this
// surface the only way to get a script is a flag no help ever shows —
// a discovery hole the help standard forbids. The command prints the
// script for one named shell; the script's dynamic callbacks re-enter
// the binary through the completion preflight in internal/cli/root.
package completion
