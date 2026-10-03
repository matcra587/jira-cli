package envelope

// GuideSummary is the routing metadata for one embedded workflow guide.
type GuideSummary struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	WhenToUse   string   `json:"when_to_use"`
	Commands    []string `json:"commands"`
	Aliases     []string `json:"aliases,omitempty"`
	Order       *int     `json:"order,omitempty"`
}

// GuideOutput serves one guide source through the human and agent doors.
type GuideOutput struct {
	ContractVersion string         `json:"contract_version"`
	Kind            string         `json:"kind"`
	Guides          []GuideSummary `json:"guides"`
	Document        string         `json:"document"`
	Slug            string         `json:"slug,omitempty"`
	Section         string         `json:"section,omitempty"`
}

func init() {
	for _, op := range []string{"agent.guide", "guide"} {
		Register(op, GuideOutput{}, map[string]any{
			"properties": map[string]any{
				"kind":     map[string]any{"enum": []any{"index", "runbook", "section", "all"}},
				"document": map[string]any{"description": "Canonical Markdown from the embedded guide source. All-guide output retains form-feed separators; --jq '.data.document' extracts it."},
			},
		})
	}
}
