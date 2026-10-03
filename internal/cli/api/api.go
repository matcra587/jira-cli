// Package api exposes explicit raw requests through the shared Jira transport.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	flagQuery             = "query"
	flagInput             = "input"
	flagContentType       = "content-type"
	flagMaxResponseBytes  = "max-response-bytes"
	flagIfMatch           = "if-match"
	flagIfUnmodifiedSince = "if-unmodified-since"
)

// Deps carries a lazy client constructor. Help and previews construct no client.
type Deps struct {
	JiraClient func() (core.JiraClient, error)
}

// New builds the raw API group. HTTP aliases preserve the canonical operation.
func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{Use: "api", Short: "Send an explicit Jira platform or Agile API request", Long: apiHelp, Args: cobra.NoArgs}
	// Keep operation declarations explicit so the source audit can attribute
	// the shared renderer to every method without guessing dynamic op names.
	cmd.AddCommand(methodCommand(deps, http.MethodGet, command.Spec[envelope.APIOutput]{Op: "api.get", Human: render, Passthrough: true, RetainOnError: retainRead}, wrapRead))
	cmd.AddCommand(methodCommand(deps, http.MethodHead, command.Spec[envelope.APIOutput]{Op: "api.head", Human: render, RetainOnError: retainRead}, wrapRead))
	cmd.AddCommand(methodCommand(deps, http.MethodOptions, command.Spec[envelope.APIOutput]{Op: "api.options", Human: render, Passthrough: true, RetainOnError: retainRead}, wrapRead))
	cmd.AddCommand(methodCommand(deps, http.MethodPost, command.Spec[envelope.APIMutationOutput]{Op: "api.post", Human: render, WritesJira: true, Passthrough: true, RetainOnError: retainMutation}, wrapMutation))
	cmd.AddCommand(methodCommand(deps, http.MethodPut, command.Spec[envelope.APIMutationOutput]{Op: "api.put", Human: render, WritesJira: true, Passthrough: true, RetainOnError: retainMutation}, wrapMutation))
	cmd.AddCommand(methodCommand(deps, http.MethodPatch, command.Spec[envelope.APIMutationOutput]{Op: "api.patch", Human: render, WritesJira: true, Passthrough: true, RetainOnError: retainMutation}, wrapMutation))
	cmd.AddCommand(methodCommand(deps, http.MethodDelete, command.Spec[envelope.APIMutationOutput]{Op: "api.delete", Human: render, WritesJira: true, Passthrough: true, RetainOnError: retainMutation}, wrapMutation))
	return cmd
}

func retainRead(out envelope.APIOutput) bool                     { return out.Attempted }
func retainMutation(out envelope.APIMutationOutput) bool         { return out.Attempted }
func wrapRead(out envelope.APIOutput, _ bool) envelope.APIOutput { return out }
func wrapMutation(out envelope.APIOutput, dry bool) envelope.APIMutationOutput {
	return envelope.APIMutationOutput{MutationState: envelope.MutationState{DryRun: dry}, APIOutput: out}
}

const apiHelp = "Send a request to /rest/api/... or /rest/agile/... on the configured site. " +
	"Absolute URLs, encoded path segments and embedded query strings are refused. " +
	"Use repeated --query name=value flags for query parameters.\n\n" +
	"--input FILE (or - for piped standard input) sends exact bytes, up to 1 MiB. " +
	"JSON is the default content type; use --content-type for other bodies. " +
	"Raw bodies are not converted or checked against a domain schema.\n\n" +
	"GET, HEAD and OPTIONS are reads. POST to /rest/api/3/search/jql, " +
	"/rest/api/3/search/approximate-count, or /rest/api/3/jql/parse is also a read. All other POST, PUT, " +
	"PATCH and DELETE requests require --force, even at a terminal. " +
	"--dry-run prepares the request locally without authentication or network access. " +
	"Read-only configuration still refuses writes with --force. Writes are never automatically replayed.\n\n" +
	"--if-match sends a quoted entity tag, a list of tags, or *. --if-unmodified-since " +
	"sends an HTTP date in GMT. If-Match takes precedence when both are supplied. " +
	"These are HTTP preconditions; protection depends on the endpoint enforcing them.\n\n" +
	"The response includes HTTP status, allowed headers, exact body_base64 bytes, " +
	"and optional UTF-8 body_text and complete JSON views. The default capture bound " +
	"is 16 MiB; --max-response-bytes=0 explicitly removes it. HTTP failures and " +
	"incomplete captures retain the receipt in a full failure frame, even in compact mode. " +
	"Inspect mutation outcomes before retrying; no raw fallback runs implicitly."

type input struct {
	query             []string
	path              string
	contentType       string
	maxResponseBytes  int64
	ifMatch           string
	ifUnmodifiedSince string
	mutation          command.MutationFlags
}

// methodCommand derives shared help, flags and execution for each declaration.
// The complete Spec still goes through the ordinary construction checks.
func methodCommand[T any](deps Deps, method string, spec command.Spec[T], wrap func(envelope.APIOutput, bool) T) *cobra.Command {
	var in input
	var cmd *cobra.Command
	name := strings.ToLower(method)
	spec.Use = name + " ENDPOINT"
	spec.Aliases = []string{method}
	spec.Short = "Send an explicit " + method + " request"
	spec.Long = apiHelp
	spec.Example = methodExample(method)
	spec.Args = command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ENDPOINT"}}}
	spec.Flags = func(c *cobra.Command) {
		cmd = c
		command.AddStringArrayVar(c, &in.query, flagQuery, nil, "Add a literal query name=value (repeatable; commas are preserved)", command.FlagOpts{Terse: "Query parameter", Placeholder: "name=value"})
		command.AddStringVar(c, &in.path, flagInput, "", "Send exact body bytes from FILE or - for piped stdin (1 MiB limit)", command.FlagOpts{Terse: "Request body", Placeholder: "file"})
		command.AddStringVar(c, &in.contentType, flagContentType, "application/json", "Request body media type", command.FlagOpts{Terse: "Content type", Placeholder: "media-type"})
		command.AddInt64Var(c, &in.maxResponseBytes, flagMaxResponseBytes, core.DefaultMaxAPIResponseBytes, "Maximum captured response bytes; 0 explicitly removes the bound", command.FlagOpts{Terse: "Response byte bound", Placeholder: "bytes"})
		command.AddStringVar(c, &in.ifMatch, flagIfMatch, "", "Send If-Match with a quoted entity tag, list of tags, or *", command.FlagOpts{Terse: "Entity-tag precondition", Placeholder: "etag"})
		command.AddStringVar(c, &in.ifUnmodifiedSince, flagIfUnmodifiedSince, "", "Send If-Unmodified-Since with an HTTP date in GMT", command.FlagOpts{Terse: "Modification-date precondition", Placeholder: "http-date"})
		if spec.WritesJira {
			command.AddMutationFlags(c, &in.mutation, command.WithRequiredForce())
		}
	}
	spec.Validate = func() error {
		for _, option := range []struct{ name, value string }{{flagIfMatch, in.ifMatch}, {flagIfUnmodifiedSince, in.ifUnmodifiedSince}} {
			if cmd.Flags().Changed(option.name) && option.value == "" {
				return invalidFlag(option.name, "HTTP preconditions cannot be empty")
			}
		}
		return nil
	}
	spec.Run = func(ctx context.Context, inv command.Invocation) (T, error) {
		out, err := run(ctx, deps, inv, method, in)
		return wrap(out, inv.DryRun), err
	}
	return command.MustNew(spec)
}

func methodExample(method string) string {
	switch method {
	case http.MethodGet:
		return "$ jira api get /rest/api/3/issue/KEY-123 --query fields=summary,status --output=json\n\n" +
			"$ jira api GET /rest/agile/1.0/board --query maxResults=10 --output=compact"
	case http.MethodPost:
		return "$ jira api post /rest/api/3/search/jql --input search.json --output=json\n\n" +
			"# preview an explicit mutation before sending it with --force\n" +
			"$ jira api post /rest/api/3/issue --input issue.json --dry-run --output=json"
	default:
		return "# inspect the " + method + " endpoint's documentation before sending a request\n" +
			"$ jira api " + strings.ToLower(method) + " --help"
	}
}

func run(ctx context.Context, deps Deps, inv command.Invocation, method string, in input) (envelope.APIOutput, error) {
	var out envelope.APIOutput
	query := url.Values{}
	for _, pair := range in.query {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return out, invalidFlag(flagQuery, "--query requires a nonempty name followed by =value")
		}
		query.Add(name, value)
	}
	req := core.APIRequest{Method: method, Endpoint: inv.Args[0], Query: query, ContentType: in.contentType, MaxResponseBytes: in.maxResponseBytes, IfMatch: in.ifMatch, IfUnmodifiedSince: in.ifUnmodifiedSince}
	if err := adapterjira.ValidateAPIRequest(req); err != nil {
		// Preserve path sentinels; other pure-boundary errors are flag values.
		return out, validateOptions(err)
	}
	read := adapterjira.APIRequestIsRead(req)
	if !inv.DryRun && !read && !in.mutation.Force {
		return out, invalidFlagCode(cli.CodeFlagRequired, command.FlagForce, "raw mutations require --force; inspect a local --dry-run first")
	}
	if in.path != "" {
		body, err := command.ReadInputFileContext(ctx, flagInput, in.path, inv.Stdin, inv.StdinIsTerminal)
		if err != nil {
			return out, err
		}
		if body == nil {
			body = []byte{}
		}
		req.Body = body
	}
	out.Request = requestReceipt(req)
	if inv.DryRun {
		return out, nil
	}
	client, err := deps.JiraClient()
	if err != nil {
		return out, err
	}
	out.Attempted = true
	response, err := command.Spin(ctx, "Sending "+method+" request", func(taskCtx context.Context) (*core.APIResponse, error) {
		return client.DoAPI(taskCtx, req)
	})
	if response != nil {
		out.Response = responseReceipt(response)
	}
	return out, err
}

// Validation is shared with transport. Name flag faults without reimplementing
// its namespace/path rules, which retain their existing sentinel taxonomy.
func validateOptions(cause error) error {
	option, ok := errors.AsType[*adapterjira.APIRequestValidationError](cause)
	if !ok {
		return cause
	}
	flags := map[string]string{"ContentType": flagContentType, "MaxResponseBytes": flagMaxResponseBytes, "IfMatch": flagIfMatch, "IfUnmodifiedSince": flagIfUnmodifiedSince}
	if flag, exists := flags[option.Field]; exists {
		return invalidFlag(flag, option.Reason)
	}
	return cause
}

func invalidFlag(flag, message string) error {
	return invalidFlagCode(cli.CodeFlagValueInvalid, flag, message)
}

func invalidFlagCode(code, flag, message string) error {
	err := cli.NewInputError(code, message)
	err.Field = flag
	return err
}

func requestReceipt(req core.APIRequest) envelope.APIRequest {
	out := envelope.APIRequest{Method: req.Method, Endpoint: req.Endpoint, Query: valueReceipt(req.Query), ContentType: req.ContentType, MaxResponseBytes: req.MaxResponseBytes, IfMatch: req.IfMatch, IfUnmodifiedSince: req.IfUnmodifiedSince}
	if req.Body != nil {
		body := base64.StdEncoding.EncodeToString(req.Body)
		out.BodyBase64 = &body
	}
	return out
}

func responseReceipt(response *core.APIResponse) *envelope.APIResponse {
	out := &envelope.APIResponse{StatusCode: response.StatusCode, Headers: valueReceipt(response.Headers), BodyBase64: base64.StdEncoding.EncodeToString(response.Body), Truncated: response.Truncated, Attempts: response.Retry.Attempts, WaitedMS: response.Retry.Waited.Milliseconds()}
	if utf8.Valid(response.Body) {
		body := string(response.Body)
		out.BodyText = &body
	}
	if !response.Truncated && utf8.Valid(response.Body) && json.Valid(response.Body) {
		out.JSON = slices.Clone(response.Body)
	}
	return out
}

func valueReceipt(values map[string][]string) []envelope.APIValues {
	out := make([]envelope.APIValues, 0, len(values))
	for _, name := range slices.Sorted(maps.Keys(values)) {
		out = append(out, envelope.APIValues{Name: name, Values: slices.Clone(values[name])})
	}
	return out
}
