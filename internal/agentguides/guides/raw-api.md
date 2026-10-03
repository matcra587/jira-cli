---
slug: raw-api
title: Send an explicit scoped Jira API request
description: Use native platform or Agile requests when a domain command cannot express the task.
when_to_use: When inspecting an unsupported endpoint, sending an exact native body, or recovering an HTTP receipt.
commands: [jira api get, jira api head, jira api options, jira api post, jira api put, jira api patch, jira api delete]
order: 30
---

## Decide

Choose the method and a site-relative `/rest/api/...` or `/rest/agile/...`
endpoint explicitly. The shared client owns authentication, routing, read-only
policy, cancellation and safe retries. A failed domain write never triggers an
automatic raw fallback.

Live requests need the site, credentials and permissions described in
Preconditions. A local `--dry-run` needs no credentials or network.

## Run

```sh
jira api get /rest/api/3/issue/KEY-123 --query fields=summary,status --output=json
jira api post /rest/api/3/search/jql --input search.json --output=json
jira api post /rest/api/3/issue --input issue.json --dry-run --output=json
```

### Prepare the exact request body

For the search example, `search.json` contains a native search body:

```json
{"jql":"project = KEY","fields":["summary"],"maxResults":1}
```

For the preview, `issue.json` contains the native issue-create body you intend
to inspect. `--input FILE` or `--input -` for piped stdin sends exact bytes,
up to 1 MiB. Bodies are not converted or validated against a domain schema.
The content type defaults to JSON. Set `--content-type` for another media type.
Repeated `--query name=value` preserves commas and repeated values. Absolute
URLs, encoded path segments and embedded query strings are refused.

### Check whether the request writes

GET, HEAD and OPTIONS are reads. POST to `/rest/api/3/search/jql`,
`/rest/api/3/search/approximate-count` or `/rest/api/3/jql/parse` is also a read.
Other live POST, PUT, PATCH and DELETE requests require `--force`, including at a terminal.
Local previews need no acknowledgment flag.
Read-only policy still refuses those writes when `--force` is supplied.
Writes are never automatically replayed.

### Check whether the endpoint enforces preconditions

`--if-match` sends a quoted entity tag, a list of quoted tags, or `*`.
Commas inside a quoted tag are preserved. `--if-unmodified-since` sends a
canonical HTTP date in GMT, such as `Thu, 01 Jan 1970 00:00:00 GMT`.
Both options are independently optional. Explicit empty values are refused.
If-Match takes precedence when both are supplied. Protection depends on the
endpoint enforcing the precondition. Live issue-description and comment PUT
probes applied stale bodies despite both kinds of requested preconditions.
These options alone do not establish safe concurrent editing on those routes.

## Save

Retain the validated `request`, `attempted` and optional `response` receipt.
`attempted` means the client was invoked, not that the server applied a write.
The response includes HTTP status, allowed headers, exact `body_base64`, optional
UTF-8 `body_text` and an optional complete JSON view. The default capture bound
is 16 MiB. `--max-response-bytes=0` explicitly removes it.
Supplied HTTP preconditions remain in the request receipt, including previews
and HTTP failures. Unset precondition fields are omitted.

## Preconditions

Live requests need a configured site and valid credentials with the required
permissions. Inspect the native payload before acknowledging a mutation.

## Recover

### The request failed or its outcome is uncertain

HTTP failures retain their receipt in the full failure frame, including compact
mode. Incomplete or oversized captures fail with output exit `8`.
Timeouts and cancellation preserve available response facts. Decode `body_base64` for exact
captured bytes. Inspect mutation outcomes independently before retrying a failed
or ambiguous request. A dry-run validates local preparation, not server acceptance.
HTTP 412 reports `jira_precondition_failed`. Fetch the current resource,
reconcile edits, and choose a new precondition before sending another write.

### The current-account response is invalid

`current_user_response_invalid` means the current-account endpoint did not
return one complete account object with an account ID. Authentication is not
confirmed. Login does not store the candidate token and `@me` writes do not
start. Missing private email or display name alone does not cause this error.
Inspect the native response using `jira api get /rest/api/3/myself --output=json`.
Current-account verification waits for the complete response, including its
end. Use `--timeout=30s` to bound connection, response and body reads together.
The shared default `--timeout=0` waits without a deadline. Command
interruption still cancels an unfinished body.

## Next

Use a domain command when it can express the workflow. Use `machine-output` for
receipt filtering and `safe-mutation` for preview and verification.
