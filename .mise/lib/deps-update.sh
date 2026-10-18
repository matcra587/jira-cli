#!/usr/bin/env bash
# [USAGE] unknown_flags "error"
# [USAGE] flag "-h --help" help="Show task help"
# A concrete flag prevents mise's empty-spec raw-argv fallback.
set -euo pipefail

query="${DEPS_UPDATE_QUERY:-patch}"

case "$query" in
patch | latest) ;;
*)
	echo "unsupported dependency update query: $query" >&2
	exit 2
	;;
esac

mods=()
# Capture before looping: errors in process substitutions do not propagate
# through a successful while loop, even with set -e and pipefail.
direct_modules="$(go list -m -f '{{ if and (not .Main) (not .Indirect) }}{{ .Path }} {{ .Version }}{{ end }}' all)"
while read -r path current; do
	[[ -n "$path" && -n "$current" ]] || continue

	update=""
	case "$query" in
	patch)
		current_core="${current%%-*}"
		[[ "$current_core" =~ ^(v[0-9]+\.[0-9]+\.)[0-9]+$ ]] || continue
		patch_prefix="${BASH_REMATCH[1]}"

		available_versions="$(go list -m -versions -f '{{ range .Versions }}{{ . }}{{ "\n" }}{{ end }}' "$path")"
		while read -r version; do
			[[ "$version" == *-* ]] && continue
			[[ "$version" == "$patch_prefix"* ]] && update="$version"
		done <<<"$available_versions"
		;;
	latest)
		update="$(go list -m -u -f '{{ if .Update }}{{ .Update.Version }}{{ end }}' "$path")"
		;;
	esac

	[[ -n "$update" && "$update" != "$current" ]] || continue
	mods+=("${path}@${update}")
done <<<"$direct_modules"

if ((${#mods[@]} > 0)); then
	go get "${mods[@]}"
fi

go mod tidy
