package pipeline

import "maps"

// FieldsFromPayload accepts both --json-input payload shapes and returns
// the field set. The Jira-native form nests everything under a top-level
// "fields" object, matching the REST API; the flat convenience form puts
// the keys at the top level. Historically issue create demanded flat and
// issue edit demanded nested — the single biggest agent tripwire in the
// input model — so both commands now route through this normalization and
// accept either.
//
// When a "fields" object is present it is the field set; any other
// top-level keys are merged underneath it (a hybrid payload never
// silently overrides an explicit fields entry). Without one, the payload
// itself is the field set.
func FieldsFromPayload(payload map[string]any) map[string]any {
	fields, ok := payload["fields"].(map[string]any)
	if !ok {
		return payload
	}
	merged := make(map[string]any, len(fields)+len(payload))
	maps.Copy(merged, fields)
	for k, v := range payload {
		if k == "fields" {
			continue
		}
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	return merged
}
