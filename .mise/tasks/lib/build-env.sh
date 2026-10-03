#!/usr/bin/env bash

jira_git_value() {
	local fallback="$1"
	shift

	git "$@" 2>/dev/null || printf '%s\n' "$fallback"
}

jira_version() {
	jira_git_value dev describe --tags --always --dirty
}

jira_branch() {
	jira_git_value unknown rev-parse --abbrev-ref HEAD
}

jira_build_by() {
	whoami
}

# Version resolution is delegated to clive, so the version ldflag targets
# clive's injection point. Commit and build time come from the VCS metadata
# Go embeds at build time. 0.15 also injected Branch/BuildBy into
# internal/version; that package is gone — the 0.16 version command decides
# whether they return.
jira_ldflags() {
	printf "%s" "-s -w"
	printf " -X 'github.com/gechr/clive.version=%s'" "$(jira_version)"
}

jira_binary_path() {
	local dist_dir="${DIST_DIR:?DIST_DIR is required}"
	local binary_name="${BINARY_NAME:?BINARY_NAME is required}"
	local goos="${GOOS:-$(go env GOOS)}"
	local goarch="${GOARCH:-$(go env GOARCH)}"

	printf "%s/%s-%s-%s\n" "$dist_dir" "$binary_name" "$goos" "$goarch"
}
