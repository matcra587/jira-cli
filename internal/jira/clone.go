package jira

import (
	"encoding/json"
	"maps"

	xslices "github.com/gechr/x/slices"
)

func cloneJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneJSONMap(v)
	case map[string]string:
		out := make(map[string]string, len(v))
		maps.Copy(out, v)
		return out
	case []any:
		return xslices.Map(v, cloneJSONValue)
	case []map[string]any:
		return xslices.Map(v, cloneJSONMap)
	case []map[string]string:
		return xslices.Map(v, func(value map[string]string) map[string]string {
			return cloneJSONValue(value).(map[string]string)
		})
	case []string:
		return append([]string(nil), v...)
	case []json.RawMessage:
		return xslices.Map(v, func(value json.RawMessage) json.RawMessage {
			return append(json.RawMessage(nil), value...)
		})
	case map[string][]any:
		out := make(map[string][]any, len(v))
		for key, value := range v {
			out[key] = cloneJSONValue(value).([]any)
		}
		return out
	case map[string][]string:
		out := make(map[string][]string, len(v))
		for key, value := range v {
			out[key] = append([]string(nil), value...)
		}
		return out
	case json.RawMessage:
		return append(json.RawMessage(nil), v...)
	default:
		return value
	}
}
