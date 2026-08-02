package spec

// Pin records the provenance of one vendored OpenAPI document. The drift
// test verifies the committed file still matches its recorded digest and
// version, so an accidental edit (or a bump that forgot to update the pin)
// fails loudly instead of silently changing what the generator reads.
type Pin struct {
	// File is the document's name inside this directory.
	File string
	// URL is the canonical Atlassian source the document was fetched from.
	URL string
	// Version is the document's own info.version.
	Version string
	// SHA256 is the hex digest of the committed file.
	SHA256 string
	// Retrieved is the fetch date (UTC, YYYY-MM-DD).
	Retrieved string
}

// Platform pins the Jira Cloud platform REST API v3 document — the source
// wire_gen.go is generated from.
var Platform = Pin{
	File:      "platform.json",
	URL:       "https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json",
	Version:   "1001.0.0-SNAPSHOT-8269c6f70784b362be829eee45ed6fd183aa9e20",
	SHA256:    "2a694bebbad5b7b4444337482f20243eed0c53ac96267ce86fcaeaa231320ee2",
	Retrieved: "2026-08-02",
}

// Agile pins the Jira Software Cloud (agile) REST API document. No agile
// wire types are generated yet — the sprint/board domain comes later — but
// the pin and drift harness are wired now so that work starts from a known
// document.
var Agile = Pin{
	File:      "agile.json",
	URL:       "https://developer.atlassian.com/cloud/jira/software/swagger.v3.json",
	Version:   "1001.0.0",
	SHA256:    "4e108d54b99064475c6ba0f986cce46dcace81336e034b58a5400b93174b927a",
	Retrieved: "2026-08-02",
}

// Pins lists every vendored document, in a stable order.
func Pins() []Pin {
	return []Pin{Platform, Agile}
}
