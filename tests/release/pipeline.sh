#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd)"
# shellcheck source=.mise/tasks/lib/release-version.sh
source "$repo_root/.mise/tasks/lib/release-version.sh"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

assert_equal() {
	[[ "$1" == "$2" ]] || fail "$3: expected [$2], got [$1]"
}

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/jira-release-test.XXXXXX")"
trap 'rm -rf -- "$fixture_root"' EXIT

for tag in v0.0.0 v0.15.2 v0.16.0 v10.20.30; do
	assert_equal "$(release_prerelease "$tag")" false "stable classification $tag"
done
for tag in v0.16.0-beta.0 v0.16.0-beta.1 v0.16.0-rc.0 v1.0.0-alpha v1.0.0-0 v1.0.0-0A v1.0.0-beta-01; do
	assert_equal "$(release_prerelease "$tag")" true "prerelease classification $tag"
done
for tag in '' 0.16.0 v0.16 v01.16.0 v0.016.0 v0.16.00 v0.16.0- v0.16.0-beta..0 v0.16.0-beta.01 v0.16.0-01 v0.16.0+build.1 'v0.16.0-beta;exit' $'v0.16.0\n'; do
	if release_prerelease "$tag" >"$fixture_root/classification" 2>"$fixture_root/error"; then
		fail "invalid tag accepted: $tag"
	fi
	[[ ! -s "$fixture_root/classification" ]] || fail 'invalid tag emitted a classification'
	[[ -s "$fixture_root/error" ]] || fail 'invalid tag omitted its diagnostic'
done
printf 'PASS: release tag classification\n'

# Execute the actual workflow's validation body; keep values in environment
# variables exactly as Actions does, rather than substituting them into code.
step_block() {
	awk -v step="      - name: $1" '
		/^      - name:/ { if (found) exit; found = ($0 == step) }
		found { print }
	' "$repo_root/.github/workflows/release.yml"
}
validation_script="$(step_block 'Validate release tag' | awk '
	/^        run: \|$/ { body = 1; next }
	body { sub(/^          /, ""); print }
')"
[[ -n "$validation_script" ]] || fail 'workflow validation body missing'
for tag in v0.16.0 v0.16.0-beta.0 v0.16.0-rc.0; do
	: >"$fixture_root/github-output"
	(cd "$repo_root" && TAG_NAME="$tag" GITHUB_OUTPUT="$fixture_root/github-output" bash -euo pipefail -c "$validation_script")
	expected=false
	[[ "$tag" != *-* ]] || expected=true
	assert_equal "$(cat "$fixture_root/github-output")" "prerelease=$expected" "workflow output $tag"
done
: >"$fixture_root/github-output"
if (cd "$repo_root" && TAG_NAME=v0.16.0-beta.01 GITHUB_OUTPUT="$fixture_root/github-output" bash -euo pipefail -c "$validation_script") >"$fixture_root/workflow-refusal" 2>&1; then
	fail 'workflow accepted malformed prerelease'
fi
[[ ! -s "$fixture_root/github-output" ]] || fail 'failed workflow validation published an output'
for step in 'Create packaging token' 'Publish Homebrew formula'; do
	condition="$(step_block "$step" | sed -n 's/^        if: //p')"
	assert_equal "$condition" "steps.release-tag.outputs.prerelease == 'false'" "$step stable-only admission"
done
condition="$(step_block 'Download release checksums' | sed -n 's/^        if: //p')"
assert_equal "$condition" "steps.release-state.outputs.exists == 'true' && steps.release-tag.outputs.prerelease == 'false'" 'stable rerun admission'
printf 'PASS: workflow validation and stable packaging gates\n'

# Publication is disabled in the archive fixtures below. Guard these explicit
# native settings separately so removing one cannot hide behind a dry-run pass.
config_section() {
	awk -v section="$1:" '
		$0 == section { found = 1; next }
		found && /^[^[:space:]#]/ { exit }
		found { print }
	' "$repo_root/.goreleaser.yaml"
}
release_policy="$(config_section release)"
assert_equal "$(printf '%s\n' "$release_policy" | sed -n 's/^  prerelease: //p')" auto 'GitHub prerelease policy'
assert_equal "$(printf '%s\n' "$release_policy" | sed -n 's/^  make_latest: //p')" \
	"'{{ if .Prerelease }}false{{ else }}true{{ end }}'" 'GitHub stable-latest policy'
scoop_policy="$(config_section scoops)"
scoop_entries="$(printf '%s\n' "$scoop_policy" | awk '/^  - / { count++ } END { print count+0 }')"
scoop_guards="$(printf '%s\n' "$scoop_policy" | awk '/^  - skip_upload: auto$/ { count++ } END { print count+0 }')"
[[ "$scoop_entries" != 0 ]] || fail 'Scoop configuration missing'
assert_equal "$scoop_guards" "$scoop_entries" 'every Scoop entry skips prerelease publication'
printf 'PASS: native GitHub and Scoop publication policies\n'

# Real disposable Git repositories establish the release task's ordering and
# tag publication and refusal behavior. Only the expensive preflight is substituted.
git_fixture() {
	GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
		git -c user.name='Release Fixture' -c user.email='fixture@example.invalid' \
		-c commit.gpgsign=false -c tag.gpgsign=false "$@"
}
git_fixture init --bare --quiet "$fixture_root/origin.git"
git_fixture init --initial-branch=main --quiet "$fixture_root/task"
mkdir -p "$fixture_root/task/.mise/tasks/lib" "$fixture_root/bin"
cp "$repo_root/.mise/tasks/release" "$fixture_root/task/.mise/tasks/release"
cp "$repo_root/.mise/tasks/lib/release-version.sh" "$fixture_root/task/.mise/tasks/lib/release-version.sh"
git_fixture -C "$fixture_root/task" add .mise
git_fixture -C "$fixture_root/task" commit --quiet -m 'release fixture'
git_fixture -C "$fixture_root/task" remote add origin "$fixture_root/origin.git"
git_fixture -C "$fixture_root/task" push --quiet origin main
cat >"$fixture_root/bin/mise" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" >>"${RELEASE_TEST_PREFLIGHT_LOG:?}"
exit "${RELEASE_TEST_PREFLIGHT_EXIT:-0}"
EOF
chmod +x "$fixture_root/bin/mise"

run_release() {
	: >"$fixture_root/preflight"
	: >"$fixture_root/pre-push"
	release_status=0
	release_output="$(cd "$fixture_root/task" && PATH="$fixture_root/bin:$PATH" \
		GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
		GIT_COMMITTER_NAME='Release Fixture' GIT_COMMITTER_EMAIL='fixture@example.invalid' \
		RELEASE_TEST_PREFLIGHT_LOG="$fixture_root/preflight" \
		RELEASE_TEST_HOOK_LOG="$fixture_root/pre-push" \
		bash .mise/tasks/release "$@" 2>&1)" || release_status=$?
}
for version in 0.16.0 v0.16.0-beta.0 0.16.0-rc.0; do
	run_release --dry-run "$version"
	assert_equal "$release_status" 0 "task dry run $version: $release_output"
	assert_equal "$(cat "$fixture_root/preflight")" $'run\nrelease:preflight' 'preflight entered once'
	[[ "$release_output" == *"would create annotated tag v${version#v} at "* ]] || fail 'dry run omitted target'
	assert_equal "$(git_fixture -C "$fixture_root/task" tag --list)" '' 'dry run created a tag'
	assert_equal "$(git_fixture --git-dir="$fixture_root/origin.git" tag --list)" '' 'dry run pushed a tag'
done
run_release --dry-run 0.16.0-beta.01
[[ "$release_status" != 0 && "$release_output" == *'invalid release version'* ]] || fail 'task malformed-version refusal'
[[ ! -s "$fixture_root/preflight" ]] || fail 'invalid version entered preflight'
git_fixture -C "$fixture_root/task" checkout --quiet -b feature
run_release --dry-run 0.16.0-beta.0
[[ "$release_status" != 0 && "$release_output" == *'must release from main'* ]] || fail 'prerelease bypassed main-only policy'
[[ ! -s "$fixture_root/preflight" ]] || fail 'branch refusal entered preflight'
git_fixture -C "$fixture_root/task" checkout --quiet main
RELEASE_TEST_PREFLIGHT_EXIT=42 run_release --dry-run 0.16.0-beta.0
assert_equal "$release_status" 42 'preflight failure propagation'
[[ "$release_output" != *'would create annotated tag'* ]] || fail 'failed preflight advertised tag creation'
assert_equal "$(git_fixture -C "$fixture_root/task" tag --list)" '' 'failed preflight created a tag'
printf 'PASS: local release dry runs and refusal ordering\n'

# Exercise Git's real pre-push boundary, including the annotated tag object ID.
# The marker is excluded so the release task reaches the hook's refusal.
printf '/.no-push\n' >>"$fixture_root/task/.git/info/exclude"
cat >"$fixture_root/task/.git/hooks/pre-push" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" >"${RELEASE_TEST_HOOK_LOG:?}"
cat >>"$RELEASE_TEST_HOOK_LOG"
if [[ -f .no-push ]]; then
	echo 'fixture no-push refused' >&2
	exit 17
fi
EOF
chmod +x "$fixture_root/task/.git/hooks/pre-push"
run_release 0.16.0-beta.0
assert_equal "$release_status" 0 "release tag push: $release_output"
assert_equal "$(cat "$fixture_root/preflight")" $'run\nrelease:preflight' 'tag push entered preflight once'
release_tag="$(git_fixture -C "$fixture_root/task" rev-parse refs/tags/v0.16.0-beta.0)"
assert_equal "$(git_fixture -C "$fixture_root/task" cat-file -t "$release_tag")" tag 'release tag is annotated'
expected_hook="$(printf 'origin\n%s\nrefs/tags/v0.16.0-beta.0 %s refs/tags/v0.16.0-beta.0 %040d' "$fixture_root/origin.git" "$release_tag" 0)"
assert_equal "$(cat "$fixture_root/pre-push")" "$expected_hook" 'release tag pre-push invocation'
assert_equal "$(git_fixture --git-dir="$fixture_root/origin.git" rev-parse refs/tags/v0.16.0-beta.0)" "$release_tag" 'remote received exact annotated tag'
assert_equal "$(git_fixture --git-dir="$fixture_root/origin.git" rev-parse 'refs/tags/v0.16.0-beta.0^{}')" \
	"$(git_fixture -C "$fixture_root/task" rev-parse HEAD)" 'release tag targets verified commit'
printf 'PASS: release tag push runs pre-push hook\n'

: >"$fixture_root/task/.no-push"
run_release 0.16.0-beta.1
assert_equal "$release_status" 1 'release propagates rejected tag push'
[[ -s "$fixture_root/pre-push" && "$release_output" == *'fixture no-push refused'* ]] || fail 'tag push did not reach marker refusal'
[[ "$release_output" != *'release tag pushed:'* ]] || fail 'refused tag push advertised publication'
assert_equal "$(git_fixture -C "$fixture_root/task" cat-file -t refs/tags/v0.16.0-beta.1)" tag 'refused push retains local tag'
remote_tag_status=0
git_fixture --git-dir="$fixture_root/origin.git" show-ref --verify --quiet refs/tags/v0.16.0-beta.1 || remote_tag_status=$?
assert_equal "$remote_tag_status" 1 'refused tag is absent from remote'
printf 'PASS: release tag hook refusal prevents publication\n'

# Build the production archive configuration against a tiny, independent Go
# program. This proves tag/version/archive handling, not remote publication.
# Explicit skip flags and an unroutable proxy prevent publishing or signing.
mkdir -p "$fixture_root/build/cmd/jira"
cp "$repo_root/.goreleaser.yaml" "$fixture_root/build/.goreleaser.yaml"
for file in README.md CHANGELOG.md LICENSE; do
	printf 'Release fixture %s\n' "$file" >"$fixture_root/build/$file"
done
# These names match GoReleaser's default changelog glob. They must remain
# outside every archive even though the files exist beside CHANGELOG.md.
for file in changelog.go changelog_test.go; do
	printf 'package fixture\n' >"$fixture_root/build/$file"
done
printf 'module example.com/release-fixture\n\ngo 1.24\n' >"$fixture_root/build/go.mod"
printf 'package main\n\nfunc main() {}\n' >"$fixture_root/build/cmd/jira/main.go"
git_fixture init --initial-branch=main --quiet "$fixture_root/build"
git_fixture -C "$fixture_root/build" add .
git_fixture -C "$fixture_root/build" commit --quiet -m 'build fixture'
git_fixture -C "$fixture_root/build" remote add origin https://github.com/example/release-fixture.git
for tag in v0.16.0-beta.0 v0.16.0; do
	git_fixture -C "$fixture_root/build" commit --allow-empty --quiet -m "$tag"
	git_fixture -C "$fixture_root/build" tag "$tag"
	if ! (cd "$fixture_root/build" && GITHUB_TOKEN=fixture SCOOP_GITHUB_TOKEN=fixture \
		HTTP_PROXY=http://127.0.0.1:9 HTTPS_PROXY=http://127.0.0.1:9 \
		goreleaser release --clean --skip=publish,sign,validate,before,announce) >"$fixture_root/build.log" 2>&1; then
		cat "$fixture_root/build.log" >&2
		fail "GoReleaser fixture $tag"
	fi
	jq --exit-status --arg tag "$tag" --arg version "${tag#v}" \
		'.tag == $tag and .version == $version' "$fixture_root/build/dist/metadata.json" >/dev/null || fail 'release metadata mismatch'
	for platform in darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64; do
		extension=tar.gz
		[[ "$platform" != windows_* ]] || extension=zip
		[[ -s "$fixture_root/build/dist/jira_${tag#v}_${platform}.${extension}" ]] || fail "missing $tag $platform archive"
		archive="$fixture_root/build/dist/jira_${tag#v}_${platform}.${extension}"
		binary=jira
		if [[ "$extension" == zip ]]; then
			members="$(unzip -Z1 "$archive" | LC_ALL=C sort)"
			binary=jira.exe
		else
			members="$(tar -tzf "$archive" | LC_ALL=C sort)"
		fi
		assert_equal "$members" "$(printf '%s\n' CHANGELOG.md LICENSE README.md "$binary")" "archive members $tag $platform"
	done
	jq --exit-status --arg version "${tag#v}" '.version == $version' \
		"$fixture_root/build/dist/scoop/bucket/jira.json" >/dev/null || fail 'Scoop fixture version mismatch'
	printf 'PASS: GoReleaser fixture archives %s (publishing disabled)\n' "$tag"
done
