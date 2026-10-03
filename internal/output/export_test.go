package output

// ResetHumanRenderersForTest drops every registered human renderer, for
// the same reason envelope.ResetRegistryForTest exists: registration
// panics on duplicates by design, and a repeated in-process test run
// would trip it. Test-only by construction.
func ResetHumanRenderersForTest() {
	humanMu.Lock()
	defer humanMu.Unlock()
	humanRenderers = map[string]HumanRenderFunc{}
}
