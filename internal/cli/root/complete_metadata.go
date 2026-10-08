package root

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"

	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boards"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// completionCache resolves public identity without opening credentials or Jira.
// Constructing the scoped reader does not create storage on a cache miss.
func completionCache(args []string) (core.Cache, string) {
	flags := globalFlagLayer(args)
	if flags.ProfileSet && flags.Profile == "" {
		return nil, ""
	}
	resolved, err := config.Load(config.Options{Path: flags.Path, Profile: flags.Profile})
	if err != nil || config.ValidateBaseURL(resolved.Config.BaseURL) != nil || strings.TrimSpace(resolved.Config.Email) == "" {
		return nil, ""
	}
	store, err := newScopedDiskCache(resolved.Config.BaseURL, resolved.Config.Email)
	if err != nil {
		return nil, ""
	}
	return store, resolved.Config.BaseURL
}

// metadataCandidates reads validated producer snapshots with their default TTL.
// Fresh bounded walks offer known values; their candidates are not exhaustive.
// Missing, stale or malformed snapshots never trigger credential or Jira access.
func metadataCandidates(args []string, name string) []string {
	store, site := completionCache(args)
	resourceName := name
	if resourceName == "boardids" {
		resourceName = "boards"
	}
	for _, resource := range cachecmd.MetadataResources() {
		if resource.Name != resourceName {
			continue
		}
		value, _, state := cachecmd.InspectMetadata(store, site, resource.Name, resource.DefaultTTL, boards.DecodeMetadata)
		if state != "fresh" {
			return nil
		}
		if resourceName == "boards" {
			return boardCandidates(value, name == "boardids")
		}
		return metadataItemCandidates(value, resourceName)
	}
	return nil
}

func metadataItemCandidates(value cachecmd.MetadataValue, resource string) []string {
	var candidates []string
	seen := map[string]bool{}
	for _, raw := range value.Items {
		var candidate, description string
		if resource == "labels" {
			if jsonv2.Unmarshal(raw, &candidate) != nil {
				return nil
			}
		} else {
			// JSON names are exact; native unknown members remain in their owner.
			var item struct {
				ID      string          `json:"id"`
				Name    string          `json:"name"`
				Key     string          `json:"key"`
				Inward  string          `json:"inward"`
				Outward string          `json:"outward"`
				Fields  json.RawMessage `json:"fields"`
			}
			if jsonv2.Unmarshal(raw, &item) != nil {
				return nil
			}
			switch resource {
			case "fields":
				candidate, description = item.ID, item.Name
			case "projects":
				candidate, description = item.Key, item.Name
			case "epics":
				var fields struct {
					Summary jira.Nullable[string] `json:"summary"`
				}
				if jsonv2.Unmarshal(item.Fields, &fields) != nil {
					return nil
				}
				candidate, description = item.Key, fields.Summary.Value
			case "linktypes":
				candidate, description = item.Name, fmt.Sprintf("%s (%s / %s)", item.ID, item.Inward, item.Outward)
			default:
				candidate = item.Name
			}
		}
		if strings.TrimSpace(candidate) == "" || cli.Text(candidate) != candidate || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if description != "" {
			candidate += "\t" + cli.Text(description)
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func boardCandidates(value cachecmd.MetadataValue, ids bool) []string {
	var snapshot struct {
		Boards []envelope.BoardRow `json:"boards"`
	}
	if jsonv2.Unmarshal(value.Payload, &snapshot) != nil {
		return nil
	}
	// Match the board resolver's trimmed Unicode case folding without
	// changing candidate spelling or choosing one board from an ambiguous name.
	names := xslices.Map(snapshot.Boards, func(board envelope.BoardRow) string { return strings.TrimSpace(board.Name) })
	slices.SortFunc(names, xstrings.CompareFold)
	ambiguous := map[string]bool{}
	for i := 1; i < len(names); i++ {
		if strings.EqualFold(names[i-1], names[i]) {
			ambiguous[names[i-1]], ambiguous[names[i]] = true, true
		}
	}
	var candidates []string
	for _, board := range snapshot.Boards {
		if !ids && ambiguous[strings.TrimSpace(board.Name)] {
			continue
		}
		id := strconv.FormatInt(board.ID, 10)
		candidate, description := board.Name, id
		if ids {
			candidate, description = id, board.Name
		}
		if cli.Text(candidate) != candidate {
			continue
		}
		var details []string
		if board.Type.Set && !board.Type.Null {
			details = append(details, board.Type.Value)
		}
		details = append(details, board.ProjectKeys...)
		if len(details) > 0 {
			description += " (" + strings.Join(details, ", ") + ")"
		}
		if !board.ProjectsComplete {
			description += " [projects incomplete]"
		}
		candidates = append(candidates, candidate+"\t"+cli.Text(description))
	}
	return candidates
}
