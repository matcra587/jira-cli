package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	xfilepath "github.com/gechr/x/filepath"
	"github.com/gechr/x/shell"
	koanftoml "github.com/knadh/koanf/parsers/toml"
	"go.yaml.in/yaml/v3"
)

// Query is a local .jql search with optional YAML or TOML frontmatter.
// Project is descriptive metadata; it never adds a hidden JQL constraint.
type Query struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Project     string `yaml:"project"`
	JQL         string `yaml:"-"`
}

// DefaultQueriesPath retains the v0.15 saved-query location on each platform.
// A directory resolution failure is deferred to a saved-query invocation;
// unrelated commands must not require a home directory.
func DefaultQueriesPath() string {
	path, err := shell.ConfigDir("jira-cli", "queries")
	if err != nil {
		return ""
	}
	return path
}

// ValidateQueryName accepts one literal filename stem on every platform.
func ValidateQueryName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("saved query name must be a nonempty .jql filename stem without path separators or controls")
	}
	return nil
}

// LoadQueries reads local .jql files in filename order, checking cancellation
// before each read. Names are literal, case-sensitive filename stems. No file
// is written, and no credentials or Jira client are resolved.
func LoadQueries(ctx context.Context, dir string) (map[string]Query, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = xfilepath.Expand(dir)
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("queries_path must name a saved-query directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read saved-query directory: %w", err)
	}
	queries := make(map[string]Query)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jql" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name(), ".jql")
		if err := ValidateQueryName(name); err != nil {
			return nil, fmt.Errorf("saved query %q: %w", name, err)
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect saved query %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("saved query %q must be a regular file", name)
		}
		data, err := os.ReadFile(path) //nolint:gosec // files selected from the configured saved-query directory
		if err != nil {
			return nil, fmt.Errorf("read saved query %q: %w", name, err)
		}
		query, err := parseQuery(string(data), name)
		if err != nil {
			return nil, fmt.Errorf("saved query %q: %w", name, err)
		}
		queries[name] = query
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queries, nil
}

func parseQuery(body, name string) (Query, error) {
	if !utf8.ValidString(body) || strings.ContainsFunc(body, func(r rune) bool { return unicode.IsControl(r) && r != '\r' && r != '\n' && r != '\t' }) {
		return Query{}, errors.New("saved query must contain valid UTF-8 without controls other than line breaks and tabs")
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	var query Query
	for _, delimiter := range []string{"---", "+++"} {
		if !strings.HasPrefix(body, delimiter+"\n") {
			continue
		}
		rest := strings.TrimPrefix(body, delimiter+"\n")
		head, tail, found := strings.Cut(rest, "\n"+delimiter+"\n")
		if strings.HasPrefix(rest, delimiter+"\n") {
			head, tail, found = "", strings.TrimPrefix(rest, delimiter+"\n"), true
		}
		if !found && strings.HasSuffix(rest, "\n"+delimiter) {
			head, tail, found = strings.TrimSuffix(rest, "\n"+delimiter), "", true
		}
		if !found {
			return Query{}, errors.New("unterminated saved-query frontmatter")
		}
		var fields map[string]any
		if delimiter == "---" {
			decoder := yaml.NewDecoder(strings.NewReader(head))
			if err := decoder.Decode(&fields); err != nil && (!errors.Is(err, io.EOF) || strings.TrimSpace(head) != "") {
				return Query{}, fmt.Errorf("parse YAML frontmatter: %w", err)
			}
			var trailing yaml.Node
			if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				return Query{}, errors.New("YAML frontmatter must contain one document")
			}
		} else {
			var err error
			fields, err = koanftoml.Parser().Unmarshal([]byte(head))
			if err != nil {
				return Query{}, fmt.Errorf("parse TOML frontmatter: %w", err)
			}
		}
		for _, field := range []struct {
			name string
			text *string
		}{{"name", &query.Name}, {"description", &query.Description}, {"project", &query.Project}} {
			if value, found := fields[field.name]; found {
				text, ok := value.(string)
				if !ok {
					return Query{}, fmt.Errorf("frontmatter %s must be a string", field.name)
				}
				*field.text = text
			}
		}
		body = tail
		break
	}
	if query.Name == "" {
		query.Name = name
	}
	query.JQL = strings.TrimSpace(body)
	if query.JQL == "" {
		return Query{}, errors.New("saved query holds no JQL; refusing an empty query that would search every issue")
	}
	return query, nil
}
