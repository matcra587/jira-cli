// Package prompt asks a person a question the command cannot answer for
// them.
//
// Its own package, and NOT part of command, because it is the one thing
// in the command layer that touches the terminal directly. command is
// imported by every command, including the ones that must never open a
// terminal — keeping a form renderer out of it keeps that true by
// construction rather than by care.
//
// Commands do not call in here. They take what they need as a function
// value on their Deps, the way they take a credential store or a Jira
// client, so the composition root supplies the real dialog and a test
// supplies an answer. That seam is the point: the branch where someone
// says "Remove" and the branch where they say "Keep" are the highest
// consequence input a command has, and reaching the terminal directly
// makes both untestable.
package prompt
