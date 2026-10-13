package config

import (
	"errors"
	"strings"

	xos "github.com/gechr/x/os"
	"github.com/knadh/koanf/v2"
)

func validateTokenEnvironmentKey(key string) error {
	if key == "" || key == "JIRA_API_TOKEN" {
		return nil
	}
	suffix, ok := strings.CutPrefix(key, "JIRA_TOKEN_")
	if ok && suffix != "" {
		valid := true
		for _, character := range suffix {
			if character != '_' && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				valid = false
				break
			}
		}
		if valid {
			return nil
		}
	}
	return errors.New("api_token_env must be empty, JIRA_API_TOKEN, or a JIRA_TOKEN_ name using uppercase letters, digits and underscores")
}

// normalizeTokenEnvironmentNames preserves values while making the JIRA_
// namespace agree with Windows' case-insensitive environment semantics. Both
// ordinary settings and named-token selection consume this same snapshot.
func normalizeTokenEnvironmentNames(variables []string) {
	if !xos.IsWindows() {
		return
	}
	for index, variable := range variables {
		name, value, ok := strings.Cut(variable, "=")
		if ok && strings.HasPrefix(strings.ToUpper(name), EnvPrefix) {
			variables[index] = strings.ToUpper(name) + "=" + value
		}
	}
}

// resolveTokenEnvironment adds a named token to the existing environment layer.
// A selected named token uses configured identity, so unrelated native records
// must not contribute their site/account. Generic JIRA_API_TOKEN keeps its
// existing precedence and fallback. The caller supplies one captured environment.
func resolveTokenEnvironment(settings, environment *koanf.Koanf, variables []string) (bool, string, error) {
	key := settings.String("api_token_env")
	if environment.Exists("api_token_env") {
		key = environment.String("api_token_env")
	}
	if err := validateTokenEnvironmentKey(key); err != nil {
		return false, "", err
	}
	if environment.Exists("api_token") {
		return false, "JIRA_API_TOKEN", nil
	}
	if key == "" {
		return false, "", nil
	}
	// Like the environment provider, ignore empty values and use the last
	// nonempty occurrence in an injected snapshot.
	for index := len(variables) - 1; index >= 0; index-- {
		name, value, ok := strings.Cut(variables[index], "=")
		if !ok || name != key {
			continue
		}
		if value == "" {
			continue
		}
		if err := environment.Set("api_token", value); err != nil {
			return false, "", err
		}
		return true, key, nil
	}
	return false, "", nil
}
