//go:build outputaudit

package audit

import "github.com/matcra587/jira-cli/internal/cli/command"

func init() {
	for index := range scenarios {
		scenario := &scenarios[index]
		if scenario.Category != CategorySuccess || scenario.Variant != "" {
			continue
		}
		query := "ORDER BY updated ASC"
		switch scenario.Op {
		case "issue.list":
			// Retain the existing scoped query and two-page native stimulus.
			for index, arg := range scenario.Args {
				if arg != "--project" || index+1 >= len(scenario.Args) {
					continue
				}
				var err error
				query, err = command.ComposeJQL(command.JQLInput{Project: []string{scenario.Args[index+1]}})
				if err != nil {
					panic(err)
				}
				scenario.Args[index], scenario.Args[index+1] = "--saved", "audit"
				break
			}
		case "issue.mine":
			scenario.Args = append(scenario.Args, "--saved=audit")
		default:
			continue
		}
		if scenario.Files == nil {
			scenario.Files = map[string]string{}
		}
		scenario.Files["queries/audit.jql"] = "---\nname: Saved audit query\ndescription: Native paging comparison\nproject: descriptive-only\n---\n" + query
		scenario.Env = append(scenario.Env, "JIRA_QUERIES_PATH=queries")
	}
}
