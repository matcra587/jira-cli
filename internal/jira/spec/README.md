# Vendored Jira OpenAPI documents

The JSON documents in this directory are vendored, pinned copies of
Atlassian's published OpenAPI specs. They are committed verbatim so builds,
tests, and code generation stay hermetic — nothing here is fetched at build
or test time.

Provenance (URL, `info.version`, SHA-256, retrieval date) is recorded in
[spec.go](spec.go); `drift_test.go` verifies the committed files still match
those pins.

| File | Source |
| ---- | ------ |
| `platform.json` | Jira Cloud platform REST API v3 (`swagger-v3.v3.json`) |
| `agile.json` | Jira Software Cloud REST API (`/rest/agile/1.0` surface) |

## Bumping a spec

1.  Re-download the document from the URL recorded in `spec.go`.
2.  Replace the file here and update that document's `Pin` (version, digest,
    date).
3.  Regenerate the wire types: `go generate ./internal/jira`.
4.  Run `go test ./internal/jira/...` — the drift test names any property
    the bump added or removed on a modeled schema; review those before
    committing the regenerated output.

A regeneration failure blocks only the bump, never the build: the
last-known-good `wire_gen.go` is always committed.

## Bulk move schema exceptions

The pinned move schema names `targetToMultipleSourceMapping` and `issueIdOrKeys`
as required members, but its actual properties and endpoint examples use
`targetToSourcesMapping` and `issueIdsOrKeys`. Native request validation checks
the actual names, explicit targets and duplicate declarations before submission.

The progress schema declares `created`, `started` and `updated` as strings,
while the [bulk-operation reference](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-bulk-operations/)
shows numeric epoch milliseconds. Generator overrides retain these values as
raw JSON, preserving either representation and absent/null distinctions.
Submission acknowledgement and job completion are separate facts; complete
progress must account for processed, failed and inaccessible issues.
