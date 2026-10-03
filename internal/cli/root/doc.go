// Package root is the composition root: it builds the Conductor runtime,
// assembles the command tree, and owns the only place adapters are
// constructed. Command packages receive their dependencies from here and
// never build their own I/O.
package root
