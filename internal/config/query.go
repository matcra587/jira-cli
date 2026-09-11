package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	xfilepath "github.com/gechr/x/filepath"
	"gopkg.in/yaml.v3"
)

// Query is a saved JQL search loaded from a .jql file: the JQL body plus
// optional frontmatter metadata (display name, description, default project).
type Query struct {
	Name        string
	Description string
	Project     string
	JQL         string
}

type queryFrontmatter struct {
	Name        string `toml:"name" yaml:"name"`
	Description string `toml:"description" yaml:"description"`
	Project     string `toml:"project" yaml:"project"`
}

// LoadQueries reads every .jql file in dir into a map keyed by file basename
// (sans extension). Each file is JQL with optional YAML (---) or TOML (+++)
// frontmatter. A tilde-prefixed dir is expanded. A malformed file's path is
// named in the error; a missing directory returns the os.ReadDir error.
func LoadQueries(dir string) (map[string]Query, error) {
	out := map[string]Query{}
	dir = expandHome(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jql" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSuffix(entry.Name(), ".jql")
		q := Query{Name: key}
		body, meta, err := parseQueryFrontmatter(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if meta.Name != "" {
			q.Name = meta.Name
		}
		q.Description = meta.Description
		q.Project = meta.Project
		q.JQL = strings.TrimSpace(body)
		out[key] = q
	}
	return out, nil
}

func parseQueryFrontmatter(body string) (string, queryFrontmatter, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	switch {
	case strings.HasPrefix(body, "---\n"):
		head, tail, ok := cutFrontmatter(body, "---")
		if !ok {
			return "", queryFrontmatter{}, fmt.Errorf("unterminated YAML frontmatter")
		}
		var meta queryFrontmatter
		if err := yaml.Unmarshal([]byte(head), &meta); err != nil {
			return "", queryFrontmatter{}, fmt.Errorf("parse YAML frontmatter: %w", err)
		}
		return tail, meta, nil
	case strings.HasPrefix(body, "+++\n"):
		head, tail, ok := cutFrontmatter(body, "+++")
		if !ok {
			return "", queryFrontmatter{}, fmt.Errorf("unterminated TOML frontmatter")
		}
		var meta queryFrontmatter
		if _, err := toml.Decode(head, &meta); err != nil {
			return "", queryFrontmatter{}, fmt.Errorf("parse TOML frontmatter: %w", err)
		}
		return tail, meta, nil
	default:
		return body, queryFrontmatter{}, nil
	}
}

func cutFrontmatter(body, delimiter string) (string, string, bool) {
	rest := strings.TrimPrefix(body, delimiter+"\n")
	end := "\n" + delimiter + "\n"
	if head, tail, ok := strings.Cut(rest, end); ok {
		return head, tail, true
	}
	if before, ok := strings.CutSuffix(rest, "\n"+delimiter); ok {
		return before, "", true
	}
	return "", "", false
}

func expandHome(path string) string {
	return xfilepath.Expand(path)
}
