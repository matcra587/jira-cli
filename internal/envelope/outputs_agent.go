package envelope

import "encoding/json"

// AgentSchemaOutput carries the framework-neutral discovery artifact. JSON
// Schema property names and host extensions are the artifact's open boundary;
// the operation's own output shape remains a typed, registered object.
type AgentSchemaOutput struct {
	// References inside Schema resolve against this extracted artifact, not
	// the enclosing invocation frame. --jq '.data.schema' extracts it whole.
	Schema json.RawMessage `json:"schema"`
}

func init() {
	for _, op := range []string{"root", "agent.schema"} {
		Register(op, AgentSchemaOutput{}, map[string]any{
			"properties": map[string]any{"schema": map[string]any{
				"type": "object", "description": "Docent command tree; local $refs resolve within this object. Full-tree routing omits shape bodies; agent schema --path or --shapes embeds them.",
			}},
		})
	}
}
