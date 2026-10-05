//go:build outputaudit

package audit

func init() {
	const native = `{"object":{"url":"https://example.com/runbook","title":"Runbook","icon":{"url16x16":"https://example.com/icon"},"status":{"resolved":false}},"application":{"name":"Example","type":"com.example"},"globalId":"` + corpusWeblinkGlobalID + `","future":{"id":900719925474099312345,"text":"\u003c\/","customMember":"native link custom value"}}`
	// Keep the existing two-target replacement and preview recipes. Native input
	// adds rich request coverage to those same independently checked outcomes.
	for i := range scenarios {
		s := &scenarios[i]
		if s.Op != "issue.weblink.add" || (s.Category != CategorySuccess && s.Category != CategoryDryRun) {
			continue
		}
		s.Args = append(append([]string(nil), s.Args[:5]...), "--json-input=remote-link.json")
		if s.Category == CategoryDryRun {
			s.Args = append(s.Args, "--dry-run")
		}
		s.Files = map[string]string{"remote-link.json": native}
	}
	passthroughBoundary["issue.weblink.add"] = []string{"payload"}
	passthroughFidelity["issue.weblink.add"] = struct {
		path  []string
		value string
	}{path: []string{"payload", "future", "customMember"}, value: "native link custom value"}
}
