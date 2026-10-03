// Package command is the shared construction path for typed operations.
//
// A command declares what it is — its operation, its help surface, its
// positional arguments, its flags — and returns typed DATA. It never
// writes. That is the whole design: a handler with no writer in scope
// cannot bypass the envelope, so the output contract, the theme, the
// coloring and --jq all follow from the returned value rather than from
// the author remembering to call the right helper.
//
// Only three things are ever per-command: the flags, the data shape,
// and the handler body. Two commands in this CLI legitimately produce
// bytes rather than an envelope — `auth token` so command substitution
// works, and `completion` so a shell can source it. They stay
// hand-written, declaring a non-empty Meta.NoSuccessEnvelope, and there
// is deliberately no raw sibling to MustNew: naming an escape hatch
// puts it on the paved road.
//
// This package owns the middle of the write-ownership chain —
// command → output → cobra's io.Writer — so commands never touch
// process streams and every destination failure has a return path to
// root.
package command
