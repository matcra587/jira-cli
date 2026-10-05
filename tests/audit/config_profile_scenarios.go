//go:build outputaudit

package audit

import "fmt"

const profileFixture = `format_version=1
default_profile='work'
theme='dark'
[profiles.work]
base_url='https://work.example.invalid'
email='work@example.invalid'
read_only=true
[profiles.personal]
base_url='https://personal.example.invalid'
email='personal@example.invalid'
read_only=false
`

func init() {
	files := map[string]string{"profiles.toml": profileFixture}
	for _, leaf := range []string{"add", "default", "list", "remove", "rename"} {
		op := "config.profile." + leaf
		args := []string{"config", "profile", leaf, "--config=profiles.toml", "--no-input"}
		switch leaf {
		case "add":
			args = append(args, "new", "--base-url=https://new.example.invalid", "--email=new@example.invalid")
		case "default":
			args = append(args, "personal")
		case "list":
			args = append(args, "--profile=personal")
		case "remove":
			args = append(args, "personal", "--force")
		case "rename":
			args = append(args, "work", "office")
		}
		scenarios = append(scenarios,
			Scenario{ID: op + "-success", Op: op, Category: CategorySuccess, Args: args, Files: files},
			Scenario{ID: op + "-help", Op: op, Category: CategoryHelp, Args: []string{"config", "profile", leaf, "--help"}},
		)
		failure := []string{"config", "profile", leaf, "--config=profiles.toml", "--no-input"}
		code := "arg_count_invalid"
		switch leaf {
		case "list":
			failure = append(failure, "unexpected")
		case "remove":
			failure = append(failure, "personal")
			code = "flag_required"
		}
		scenarios = append(scenarios, Scenario{ID: op + "-failure", Op: op, Category: CategoryFailure, Args: failure, Files: files, ExpectExit: 3, ExpectCode: code})
		stylingExpected[op] = false
		if leaf == "list" {
			continue
		}
		preview := append(append([]string(nil), args...), "--dry-run")
		scenarios = append(scenarios, Scenario{ID: op + "-preview", Op: op, Category: CategoryDryRun, Args: preview, Files: files})
		successDataShape[op] = struct{ required, optional []string }{
			required: []string{"dry_run", "path", "name", "default_profile", "would_change", "changed", "status"},
			optional: []string{"previous_name"},
		}
		dryRunPreview[op] = func(data map[string]any) (bool, string) { return checkProfileChange(data, leaf, true) }
		dryRunTense[op] = struct {
			wantProspective string
			forbidCompleted []string
		}{"would update profile", []string{"updated profile", "saved"}}
	}
	successDataShape["config.profile.list"] = struct{ required, optional []string }{required: []string{"path", "file_exists", "profiles"}}
}

func checkProfileChange(data map[string]any, operation string, preview bool) (bool, string) {
	name, defaultName, previous := "", "work", ""
	switch operation {
	case "add":
		name = "new"
	case "default":
		name, defaultName = "personal", "personal"
	case "remove":
		name = "personal"
	case "rename":
		name, defaultName, previous = "office", "office", "work"
	default:
		return false, "unknown profile operation expectation"
	}
	status := "saved"
	if preview {
		status = "prepared"
	}
	if data["dry_run"] != preview || data["name"] != name || data["default_profile"] != defaultName || data["would_change"] != true || data["changed"] != !preview || data["status"] != status {
		return false, "profile receipt lost explicit identity, saved default or publication state"
	}
	if previous != "" {
		if data["previous_name"] != previous {
			return false, "profile rename lost its original name"
		}
	} else if _, present := data["previous_name"]; present {
		return false, "non-rename operation invented a previous name"
	}
	if path, ok := data["path"].(string); !ok || path == "" {
		return false, "profile receipt names no configuration file"
	}
	return true, ""
}

func checkProfileList(data map[string]any) (bool, string) {
	if data["file_exists"] != true {
		return false, "profile list lost file existence"
	}
	if path, ok := data["path"].(string); !ok || path == "" {
		return false, "profile list names no configuration file"
	}
	profiles, ok := data["profiles"].([]any)
	if !ok || len(profiles) != 2 {
		return false, "profile list must retain both authored profiles"
	}
	for index, name := range []string{"personal", "work"} {
		profile, ok := profiles[index].(map[string]any)
		if !ok || len(profile) != 6 || profile["name"] != name || profile["default"] != (name == "work") || profile["selected"] != (name == "personal") || profile["base_url"] != "https://"+name+".example.invalid" || profile["email"] != name+"@example.invalid" || profile["secret_backend"] != "keyring" {
			return false, fmt.Sprintf("profile %d lost authored metadata or confused selected and default state", index)
		}
	}
	return true, ""
}
