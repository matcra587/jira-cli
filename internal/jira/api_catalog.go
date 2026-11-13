package jira

import (
	"slices"
	"strings"

	xslices "github.com/gechr/x/slices"
)

// APICatalogVersion identifies the bundled discovery format, not a server API.
const APICatalogVersion = 1

// APIOperation is one documented method/path pair from a pinned public spec.
// It supplies suggestions, never tenant availability or write authorization.
type APIOperation struct {
	Family      string `json:"family"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operation_id"`
	Summary     string `json:"summary"`
	Deprecated  bool   `json:"deprecated"`
}

// APICatalogSource binds the local catalog to its reviewed upstream document.
type APICatalogSource struct {
	Family  string `json:"family"`
	URL     string `json:"url"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// APIOperations returns an isolated, path/method-sorted selection. Empty method
// selects every documented method; query searches path, operation ID and summary
// case-insensitively. An empty selection remains an explicit JSON array.
func APIOperations(method, query string) []APIOperation {
	method, query = strings.ToUpper(method), strings.ToLower(query)
	return xslices.Filter(apiCatalogOperations, func(operation APIOperation) bool {
		if method != "" && method != operation.Method {
			return false
		}
		searchable := operation.Path + " " + operation.OperationID + " " + operation.Summary
		return query == "" || strings.Contains(strings.ToLower(searchable), query)
	})
}

// APICatalogSources returns provenance without reading files or the network.
func APICatalogSources() []APICatalogSource { return slices.Clone(apiCatalogSources) }
