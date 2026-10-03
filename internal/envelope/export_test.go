package envelope

// ResetRegistryForTest drops every registration. Registration is a
// startup-only, panic-on-duplicate contract; a test suite re-running in
// one process (go test -count=N, the standard flake hunt) would otherwise
// trip that panic on its own second pass. Test-only by construction —
// this file compiles solely under `go test`.
func ResetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registrations = map[string]Registration{}
}
