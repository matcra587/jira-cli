package cli

// ForeignFlagTableForTest exposes the foreign-flag equivalence table to
// the external test that pins every suggestion to a flag the live
// command tree actually carries — the table must never recommend a flag
// the binary does not have.
var ForeignFlagTableForTest = foreignFlagTable
