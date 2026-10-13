package config

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"strconv"

	"github.com/knadh/koanf/v2"
)

const defaultRefreshInterval = 30

func parseRefreshInterval(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, strconv.IntSize)
	if err != nil || parsed <= 0 {
		return 0, errors.New("refresh_interval must be a positive integer number of seconds")
	}
	return parsed, nil
}

// parseTeamAccountIDs retains list order and literal identities. JSON avoids
// treating commas inside an opaque account ID as separators or clearing nulls.
func parseTeamAccountIDs(value string) ([]string, error) {
	failure := errors.New("team_account_ids must be a JSON array of strings")
	var decoded any
	if err := jsonv2.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, failure
	}
	values, ok := decoded.([]any)
	if !ok {
		return nil, failure
	}
	ids := make([]string, len(values))
	for index, value := range values {
		id, ok := value.(string)
		if !ok {
			return nil, failure
		}
		ids[index] = id
	}
	return ids, nil
}

// prepareClientPreferenceEnvironment validates these public settings before
// credential callbacks. File arrays were already decoded with strict types.
func prepareClientPreferenceEnvironment(layer *koanf.Koanf) error {
	if layer.Exists("refresh_interval") {
		parsed, err := parseRefreshInterval(layer.String("refresh_interval"))
		if err != nil {
			return err
		}
		if err := layer.Set("refresh_interval", parsed); err != nil {
			return err
		}
	}
	if layer.Exists("team_account_ids") {
		parsed, err := parseTeamAccountIDs(layer.String("team_account_ids"))
		if err != nil {
			return err
		}
		return layer.Set("team_account_ids", parsed)
	}
	return nil
}
