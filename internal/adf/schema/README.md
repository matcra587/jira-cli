# Vendored ADF JSON Schema

`full.json` is the Atlassian Document Format JSON Schema (draft-04),
vendored verbatim from the `@atlaskit/adf-schema` npm package. It is the
single source of truth for ADF validation (`internal/adf/validate.go`) and
for the generated typed-node catalog (`internal/adf/nodes_gen.go`, produced
by `internal/adf/gen`). Nothing fetches it at build or test time — the file
is pinned here so builds stay hermetic and a schema change is always a
deliberate, reviewed bump.

## Pin

The version and source-URL lines below carry `clover:` directives, so
`clover run` proposes the bump and rewrites both lines in lockstep. The
SHA-256 is of the *extracted* `full.json`, which no registry serves — it is
updated by hand in step 3 of the procedure.

*   Package: `@atlaskit/adf-schema`
<!-- clover: id=adf-schema provider=http url=https://registry.npmjs.org/@atlaskit/adf-schema/latest jq=.version -->
*   Version: **57.6.18**
*   File within the package: `json-schema/v1/full.json`
<!-- clover: from=adf-schema -->
*   Source URL: <https://registry.npmjs.org/@atlaskit/adf-schema/-/adf-schema-57.6.18.tgz>
*   SHA-256 of `full.json`: `5128562b75278c8a83e7e3619a570205bc80d59696985ec31a7a7883cff66fbe`

The package also ships `json-schema/v1/stage-0.json` (experimental,
pre-release node stages). We deliberately vendor only `full.json`: stage-0
nodes are not part of what Jira Cloud serves as stable ADF, and the loose
substrate preserves anything unknown byte-for-byte anyway.

## Stable status colors

Authored status nodes accept `neutral`, `purple`, `blue`, `red`, `yellow`,
`green`, or a six-digit `#RRGGBB` color. Hex digits may use either case; named
colors are lowercase. Three-digit shorthand, alpha channels, non-hex digits,
and surrounding whitespace are invalid. Validation does not rewrite colors.

For example, save this native document as `comment.json`:

```json
{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":"#a1B2c3"}}]}]}
```

Preview the exact body through the native comment route:

```sh
jira issue comment add KEY-123 --json-input comment.json --dry-run --output=json --jq '.data.body'
```

This is local schema validation. A preview does not establish whether a Jira
site accepts the body or how its editor renders the color. Server-originated
ADF remains unvalidated on reads so unknown future content can pass through.

## Bump procedure

1.  Bump the pin: `clover run` rewrites the Version and Source URL lines
    above to the newest published version (`clover run --dry-run` to
    preview first).
2.  Download the pinned tarball and extract the schema:

    ```sh
    curl -sO https://registry.npmjs.org/@atlaskit/adf-schema/-/adf-schema-<VERSION>.tgz
    tar xzf adf-schema-<VERSION>.tgz package/json-schema/v1/full.json
    cp package/json-schema/v1/full.json internal/adf/schema/full.json
    ```

3.  Update the SHA-256 in this file
    (`sha256sum internal/adf/schema/full.json`).
4.  Regenerate the typed layer: `go generate ./internal/adf`.
5.  Run `go test ./internal/adf/...` — `drift_test.go` fails naming any
    definition the generated catalog does not account for, and the
    generator's own test fails if `nodes_gen.go` is stale.
6.  Review the `nodes_gen.go` diff (new node types, changed variants) and
    commit schema + generated output together in one change.

A regen failure blocks only the bump: the committed `nodes_gen.go` is the
last-known-good output and the build never depends on running the generator.
