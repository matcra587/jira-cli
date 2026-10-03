package api

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func render(v *output.View, env envelope.Envelope) error {
	var out envelope.APIOutput
	dry := false
	switch data := env.Data.(type) {
	case envelope.APIOutput:
		out = data
	case envelope.APIMutationOutput:
		out, dry = data.APIOutput, data.DryRun
	default:
		return fmt.Errorf("api: human renderer received %T, want APIOutput or APIMutationOutput", env.Data)
	}
	if dry {
		v.Line("Would send " + cli.Text(out.Request.Method) + " " + cli.Text(out.Request.Endpoint))
	} else {
		v.Line(cli.Text(out.Request.Method) + " " + cli.Text(out.Request.Endpoint))
	}
	for _, query := range out.Request.Query {
		for _, value := range query.Values {
			v.Line("Query: " + cli.Text(query.Name) + "=" + cli.Text(value))
		}
	}
	if out.Request.IfMatch != "" {
		v.Line("If-Match: " + cli.Text(out.Request.IfMatch))
	}
	if out.Request.IfUnmodifiedSince != "" {
		v.Line("If-Unmodified-Since: " + cli.Text(out.Request.IfUnmodifiedSince))
	}
	if dry {
		if out.Request.BodyBase64 != nil {
			body, err := base64.StdEncoding.DecodeString(*out.Request.BodyBase64)
			if err != nil {
				return fmt.Errorf("api: decode preview body: %w", err)
			}
			v.Line("Content-Type: " + cli.Text(out.Request.ContentType))
			renderBody(v, body, *out.Request.BodyBase64)
		}
		v.Line("No request sent.")
		return nil
	}
	if out.Response == nil {
		if out.Attempted {
			v.Line("No HTTP response received; inspect diagnostics for delivery and mutation outcomes.")
		}
		return nil
	}
	v.Line("HTTP " + strconv.Itoa(out.Response.StatusCode))
	for _, header := range out.Response.Headers {
		for _, value := range header.Values {
			v.Line(cli.Text(header.Name) + ": " + cli.Text(value))
		}
	}
	if out.Response.Truncated {
		v.Line("Response capture incomplete; inspect the partial receipt before retrying.")
	}
	if out.Response.BodyText != nil {
		for _, line := range strings.Split(*out.Response.BodyText, "\n") {
			v.Line(cli.Text(line))
		}
	} else {
		v.Line("Body (base64): " + out.Response.BodyBase64)
	}
	return nil
}

func renderBody(v *output.View, body []byte, encoded string) {
	if !utf8.Valid(body) {
		v.Line("Body (base64): " + encoded)
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		v.Line(cli.Text(line))
	}
}
