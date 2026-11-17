package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
)

// readNativeJSONResponse bounds actual bytes before JSON buffering or native
// decoding. Content-Length is advisory, and a decoded record limit cannot
// constrain the size of a single record or an unknown response member.
func readNativeJSONResponse(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, core.DefaultMaxAPIResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > core.DefaultMaxAPIResponseBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrAPIResponseTooLarge, core.DefaultMaxAPIResponseBytes)
	}
	return raw, nil
}

func decodeNativeJSONResponse(body io.Reader, destination any) error {
	raw, err := readNativeJSONResponse(body)
	if err != nil {
		return err
	}
	// Keep each caller's existing JSON admission policy. Readers that require
	// EOF or exact native members perform those checks separately.
	return json.NewDecoder(bytes.NewReader(raw)).Decode(destination)
}
