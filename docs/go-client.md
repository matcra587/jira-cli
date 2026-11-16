# Go client

Go applications can import `github.com/matcra587/jira-cli/auth` and
`github.com/matcra587/jira-cli/api` to reuse saved Jira sign-ins. This initial
surface provides profile discovery and bounded native Platform/Agile requests.
The calling application owns account verification, selection rules, pagination,
scheduling and storage.

## Discover saved accounts

```go
profiles, err := auth.ListProfiles(ctx, auth.Options{})
if err != nil {
    return err
}
```

Discovery reads authored public metadata. It does not open a credential store,
resolve a token or contact Jira. Entries include the profile name, saved default,
site, email and cloud ID. They omit credential IDs and secret-manager locations.
An empty name represents an existing flat configuration; select it by leaving
`Profile` empty. An empty named-profile table remains empty.

Set `ConfigPath` to select another file. Missing default files return an empty
list; missing explicit files, malformed settings and configurations requiring
migration return an error. Use `errors.Is(err, auth.ErrMigrationRequired)` to
recognize the latter. `ListProfiles` lists all profiles regardless of the
current environment-selected profile; `DefaultProfile` is the saved default.

## Create a client for each check or sync batch

```go
client, err := api.NewClientWithContext(ctx, api.ClientOptions{
    Profile:          profileName,
    Timeout:          15 * time.Second,
    MaxResponseBytes: 2 << 20,
    ReadOnly:         true,
})
if err != nil {
    return err
}

response, err := client.Do(ctx, api.Request{
    Method:   http.MethodGet,
    Endpoint: "/rest/api/3/myself",
})
if err != nil {
    return err
}
var account struct {
    AccountID   string `json:"accountId"`
    DisplayName string `json:"displayName"`
}
if err := json.Unmarshal(response.Body, &account); err != nil {
    return err
}
```

These snippets belong inside a function returning errors. Import the public
packages above plus `encoding/json`, `net/http` and `time`. Supply a context
and the selected profile name.

An explicit profile overrides `JIRA_PROFILE` and the saved default. Resolution
captures one configuration/environment snapshot and selects the same keychain,
environment or 1Password provider as the CLI. No Jira request occurs during
construction. Each new client resolves credentials afresh; an existing client
retains its credential, site and request policy throughout a batch.

`client.Identity()` reports configured metadata, not verified remote identity.
Validate the native `accountId` from `/rest/api/3/myself` before enabling a
source. Bind stored data to that account, site/cloud scope and native resource
IDs. Recheck identity on later batches and refuse unexpected account changes.
Jira issue keys can change; preserve the native issue ID for durable identity.

Saved read-only policy remains enforced even if `ClientOptions.ReadOnly` is
false. Positive `Timeout` overrides the saved per-request timeout. Zero retains
the saved timeout or a supplied HTTP client's timeout. A caller context bounds
the entire batch, including credential resolution and pagination.

## Make native requests

`Request.Endpoint` is a site-relative `/rest/api/...` or `/rest/agile/...` path.
The client applies scoped cloud routing. Absolute URLs and traversal are
refused. All redirects are refused, including credential-free redirects, so a
different service cannot supply a successful identity reply.

Use `url.Values` for repeated literal query values. Request bodies are sent
byte-for-byte; nil and explicitly empty bodies remain distinct. Set
`ContentType` when sending JSON. Optional `IfMatch` and `IfUnmodifiedSince`
headers retain explicit HTTP preconditions; they protect a write only when the
remote endpoint enforces them.

Responses retain exact native bytes, status, permitted headers and retry facts.
Decode into native structs, `json.RawMessage`, or a decoder using `UseNumber`
when retaining arbitrary JSON numbers. Walk each endpoint's native pagination
and report incomplete coverage. The client does not choose a query or fetch
linked content automatically.

Responses are capped at 16 MiB by default. A positive `MaxResponseBytes`
overrides that bound. A nonnil response can accompany an error, including a
successful HTTP status with an incomplete body. Inspect `Truncated` and retain
acknowledged write observations before deciding what to do next.

## Handle failures

Use `failure, ok := errors.AsType[*api.Error](err)`. Its safe summary
includes `Code`, `StatusCode` and rate-limit `RetryAfter` where available.
Important codes include `authentication_required`, `forbidden`, `rate_limited`,
`read_only`, `invalid_request`, `response_too_large` and `response_incomplete`.
Use `errors.Is` for context cancellation, `auth.ErrNotAuthenticated`,
`api.ErrReadOnly`, `api.ErrResponseTooLarge` and `api.ErrResponseIncomplete`.

Safe reads use the CLI's bounded retry policy. `Retry: &api.RetryPolicy{}`
disables retries. Writes are never automatically replayed, including after
HTTP 429 or uncertain transport failures. An error or canceled context does
not establish that a write had no effect.

Clients, options and sessions omit credentials from JSON and normal formatting.
Wrapped provider/transport causes and native response bodies can contain
sensitive or untrusted data; inspect them deliberately before logging.

## Inject authentication and HTTP fixtures

For host-managed credentials, supply a nonnil `ClientOptions.Credentials` with
`BaseURL`, `Email`, `APIToken` and optional `CloudID`. This selects explicit
mode and bypasses all saved configuration, environment credentials and providers.
Incomplete explicit credentials fail rather than falling back to a saved login.
Keep these values out of source, subprocess arguments and logs.

`HTTPClient` is copied; construction does not mutate the caller's timeout,
transport or redirect policy. The library's redirect refusal remains mandatory.
HTTPS is required, except explicit loopback HTTP fixtures. A context-aware
`CredentialStore` can also be injected for saved-profile tests.

Native keychain calls may be synchronous once entered. The current 1Password
desktop SDK pins its first account process-wide; separate Jira profiles do not
permit switching desktop SDK accounts in one process. Existing provider
refusals remain intact.

For authentication without this request client, `auth.Resolve(ctx, opts)`
returns a session with configured identity, policy and an explicit `Token()`
accessor. Treat that accessor's result as a secret. Resolution does not start
a login flow, change saved defaults or verify Jira permissions.
