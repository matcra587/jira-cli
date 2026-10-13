package root

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/completion/native"
)

// completionWord admits only the explicit native callback protocol. Bash
// transports its raw current token; the selected shell.Split decodes literal
// quoting without expanding variables or executing substitutions. A final quote
// can still be unfinished while the user asks for completion.
func completionWord(shellName string, args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	word, ok := strings.CutPrefix(args[0], native.WordArgument)
	if !ok || len(word) > 64*1024 || !utf8.ValidString(word) {
		return "", nil, false
	}
	if shellName == "bash" && word != "" {
		word, ok = bashCompletionQuotes(word, true)
		if !ok {
			return "", nil, false
		}
		decoded := false
		for _, suffix := range []string{"", "\x00'", "\x00\""} {
			parts, err := shell.Split(word + suffix)
			if err == nil && len(parts) == 1 {
				if suffix != "" && !strings.HasSuffix(parts[0], "\x00") {
					continue
				}
				word, decoded = strings.TrimSuffix(parts[0], "\x00"), true
				break
			}
		}
		if !decoded {
			return "", nil, false
		}
	}
	if !utf8.ValidString(word) || cli.Text(word) != word || strings.ContainsAny(word, "\t\r\n") {
		return "", nil, false
	}
	completed := slices.Clone(args[1:])
	if shellName == "bash" {
		for i, raw := range completed {
			if !utf8.ValidString(raw) {
				return "", nil, false
			}
			raw, ok := bashCompletionQuotes(raw, false)
			if !ok {
				return "", nil, false
			}
			parts, err := shell.Split(raw)
			if err != nil || len(parts) != 1 {
				return "", nil, false
			}
			completed[i] = parts[0]
		}
	}
	return word, completed, true
}

func completionPrefixMatches(shellName, value, prefix string) bool {
	if shellName == "pwsh" {
		value, prefix = strings.ToLower(value), strings.ToLower(prefix)
	}
	return strings.HasPrefix(value, prefix)
}

func matchingWordCandidates(shellName, prefix string, values []string) []string {
	return slices.DeleteFunc(slices.Clone(values), func(line string) bool {
		value, _, _ := strings.Cut(line, "\t")
		return !completionPrefixMatches(shellName, value, prefix)
	})
}

func attachmentWordCandidates(ctx context.Context, shellName string, args []string) []string {
	prefix, args, ok := completionWord(shellName, args)
	if !ok || ctx.Err() != nil {
		return nil
	}
	flags, positionals, err := completionPositionals(args)
	if err != nil {
		return nil
	}
	if len(flags.files) > 0 || len(positionals) == 0 {
		return matchingWordCandidates(shellName, prefix, issueKeyCandidates(args))
	}
	return wordPathCandidates(shellName, localPathCandidates(ctx, prefix))
}

func wordPathCandidates(shellName string, paths []string) []string {
	if shellName == "pwsh" {
		for i, path := range paths {
			if strings.HasSuffix(path, string(filepath.Separator)) {
				paths[i] += "\t" + native.WordDirectoryHint
			}
		}
	}
	return paths
}

func csvFileWordCandidates(ctx context.Context, shellName string, args []string) []string {
	prefix, args, ok := completionWord(shellName, args)
	if !ok || ctx.Err() != nil {
		return nil
	}
	if _, _, err := completionPositionals(args); err != nil {
		return nil
	}
	completion, err := command.ParseCSVCompletion(prefix)
	if err != nil {
		return nil
	}
	var candidates []string
	selected := completion.Selected()
	for _, path := range localPathCandidates(ctx, completion.Prefix()) {
		if slices.Contains(selected, path) {
			continue
		}
		candidate, err := completion.Candidate(path)
		if shellName == "fish" {
			candidate, err = completion.QuotedCandidate(path)
		}
		if strings.HasSuffix(path, string(filepath.Separator)) {
			candidate, err = completion.DirectoryCandidate(path)
		}
		if err != nil {
			return nil
		}
		if shellName == "pwsh" && strings.HasSuffix(path, string(filepath.Separator)) {
			candidate += "\t" + native.WordDirectoryHint
		}
		candidates = append(candidates, candidate)
	}
	if ctx.Err() != nil {
		return nil
	}
	return candidates
}

func configWordCandidates(ctx context.Context, shellName string, args []string) []string {
	prefix, args, ok := completionWord(shellName, args)
	if !ok || ctx.Err() != nil {
		return nil
	}
	_, positionals, err := completionPositionals(args)
	if err != nil || len(positionals) != 1 {
		return nil
	}
	if positionals[0] == "theme_path" {
		paths := localPathCandidates(ctx, prefix)
		for i, path := range paths {
			if ctx.Err() != nil {
				return nil
			}
			absolute := path
			if !filepath.IsAbs(path) {
				cwd, err := os.Getwd()
				if err != nil {
					return nil
				}
				volume := filepath.VolumeName(path)
				if volume != "" {
					// Windows drive-relative names use that drive's native
					// working directory, then retain the authored traversal.
					cwd, err = filepath.Abs(volume + ".")
					if err != nil {
						return nil
					}
					path = strings.TrimPrefix(path, volume)
				} else if len(path) > 0 && os.IsPathSeparator(path[0]) {
					// A Windows rooted name omits only its current volume.
					cwd = filepath.VolumeName(cwd)
				}
				// Join without cleaning: symlink/.. traversal belongs to the
				// selected path and can resolve differently from a clean path.
				separator := string(filepath.Separator)
				if len(path) > 0 && os.IsPathSeparator(path[0]) {
					separator = ""
				}
				absolute = cwd + separator + path
			}
			if !utf8.ValidString(absolute) || cli.Text(absolute) != absolute || strings.ContainsAny(absolute, "\t\r\n") {
				return nil
			}
			paths[i] = absolute
		}
		return wordPathCandidates(shellName, paths)
	}
	return matchingWordCandidates(shellName, prefix, configValueCandidates(args))
}
