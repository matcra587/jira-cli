//go:build flagmatrix

// Package matrix is the standalone flag matrix: the enumerator that
// derives every (command path, flag) pair from the LIVE assembled tree
// — hidden compatibility aliases included — and the coverage gate that
// fails, naming each pair, until a running case has proved it.
//
// Phase 1 (this package) is the enumerator, the proof registry, and the
// gate. The per-flag cases are phase 2, written against what the gate
// reports missing — the order is deliberate: with the gate first,
// "total coverage" is a number the tree computes, not a claim a person
// makes.
//
// Coverage is recorded by the cases themselves at run time (Prove), so
// a deleted or skipped case drops its pair back into the missing set
// with no bookkeeping. An inherited (persistent) flag is proved once,
// at the command that declares it — proving --output on all 30+ leaves
// would inflate the count without adding evidence, so a proof recorded
// through any descendant credits the declaring pair.
//
// Build-tagged `flagmatrix` for tests/audit's reason: the phase-2 cases
// execute the real binary per pair, which is a slow opt-in lane, not
// part of the unit sweep. Run via:
//
//	go test -tags flagmatrix -count=1 ./tests/matrix/
//
// While uncovered pairs remain, that run is RED by design; the failure
// output is the phase-2 worklist.
package matrix
