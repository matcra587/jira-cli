#!/usr/bin/env bash

# Shared by the local release task and the tag-triggered workflow. Build
# metadata is deliberately excluded from release tags; prerelease IDs follow
# SemVer's numeric leading-zero rule.
release_prerelease() {
	local number='(0|[1-9][0-9]*)'
	local identifier='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
	local pattern="^v${number}\\.${number}\\.${number}(-${identifier}(\\.${identifier})*)?$"
	if [[ ! "${1:-}" =~ $pattern ]]; then
		printf '%s\n' 'version must match vMAJOR.MINOR.PATCH[-PRERELEASE] without leading numeric zeros' >&2
		return 1
	fi
	if [[ "$1" == *-* ]]; then
		printf 'true\n'
	else
		printf 'false\n'
	fi
}
