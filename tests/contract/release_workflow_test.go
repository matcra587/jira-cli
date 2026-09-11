package contract

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseWorkflowUsesPinnedGoReleaserAndHomebrewPublisher(t *testing.T) {
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	goreleaser, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("ReadFile(goreleaser) error = %v", err)
	}
	assertPinnedWorkflowUses(t, string(release))
	combined := string(release) + "\n" + string(goreleaser)
	for _, want := range []string{
		"actions/checkout@",
		"sigstore/cosign-installer@",
		"jdx/mise-action@",
		"install_args: --locked",
		"id: release-state",
		"gh release view",
		"gh release download",
		"goreleaser/goreleaser-action@",
		"actions/create-github-app-token@",
		"vars.APP_CLIENT_ID",
		"secrets.APP_PRIVATE_KEY",
		"matcra587/github-actions/packages/homebrew-publish-formula@",
		"steps.app-token.outputs.token",
		"tap: matcra587/homebrew-tap",
		"name_template:",
		"{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}",
		"sign-blob",
		"gechr/clive.version={{ .Version }}",
		"internal/version.Branch={{ .Branch }}",
		"internal/version.BuildBy=goreleaser",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("release workflow/config missing %q\nrelease:\n%s\ngoreleaser:\n%s", want, release, goreleaser)
		}
	}
	if strings.Contains(string(release), "HOMEBREW_TAP_TOKEN") {
		t.Fatalf("release workflow should use the Slack-aligned GitHub App token path, not HOMEBREW_TAP_TOKEN")
	}
}

func TestReleaseWorkflowAwaitsCIGateBeforePublishing(t *testing.T) {
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	got := string(release)
	for _, want := range []string{
		"name: Await CI gate for tagged commit",
		"actions/workflows/ci.yml/runs?head_sha=",
		"name: Check GoReleaser configuration",
		"mise run release:check",
		"if: steps.release-state.outputs.exists != 'true'",
		"actions: read",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("release workflow missing CI gate wiring %q\n%s", want, release)
		}
	}
	if strings.Index(got, "name: Await CI gate for tagged commit") > strings.Index(got, "name: Run GoReleaser") {
		t.Fatalf("CI gate await must run before GoReleaser publish\n%s", release)
	}
	if strings.Index(got, "name: Check GoReleaser configuration") > strings.Index(got, "name: Run GoReleaser") {
		t.Fatalf("GoReleaser config check must run before GoReleaser publish\n%s", release)
	}
}

func TestReleaseWorkflowBuildsUncached(t *testing.T) {
	// Release artifacts must never be built from cached state; validation
	// caching lives in the ci workflow's push gate instead. This is the
	// cache-poisoning stance zizmor enforces on publish-triggered workflows.
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	got := string(release)
	if !strings.Contains(got, "cache: false") {
		t.Fatalf("release workflow must disable the mise tool cache\n%s", release)
	}
	if strings.Contains(got, "actions/cache") {
		t.Fatalf("release workflow must not restore caches into the publish path\n%s", release)
	}
}

func TestReleaseWorkflowUsesStrictBashDefaults(t *testing.T) {
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	got := string(release)
	for _, want := range []string{
		"defaults:",
		"run:",
		"shell: bash --noprofile --norc -euo pipefail {0}",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("release workflow missing strict bash default %q\n%s", want, release)
		}
	}
	if strings.Contains(got, "set -euo pipefail") {
		t.Fatalf("release workflow should use defaults.run.shell for strict bash, not per-step set lines\n%s", release)
	}
}

func TestReleaseWorkflowDoesNotRequireLinuxKeyring(t *testing.T) {
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	got := string(release)
	for _, forbidden := range []string{
		"name: Install Linux keyring dependencies",
		"dbus-x11",
		"gnome-keyring",
		"dbus-run-session",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("release workflow should not require Linux keyring setup %q\n%s", forbidden, release)
		}
	}
	if !strings.Contains(got, "mise run release:check") {
		t.Fatalf("release workflow missing GoReleaser config check\n%s", release)
	}
}

func TestReleaseWorkflowValidatesExactSemverTag(t *testing.T) {
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	got := string(release)
	for _, want := range []string{
		"name: Validate release tag",
		`[[ "${TAG_NAME}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("release workflow missing exact tag validation %q\n%s", want, release)
		}
	}
	if strings.Index(got, "name: Validate release tag") > strings.Index(got, "name: Await CI gate for tagged commit") {
		t.Fatalf("release tag validation must run before the CI gate await\n%s", release)
	}
}

func TestReleaseArtifactsUseSupportedTargets(t *testing.T) {
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("ReadFile(release) error = %v", err)
	}
	goreleaser, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("ReadFile(goreleaser) error = %v", err)
	}
	gotRelease := string(release)
	gotGoReleaser := string(goreleaser)
	for _, want := range []string{"darwin/arm64", "linux/amd64", "linux/arm64"} {
		if !strings.Contains(gotRelease, want) {
			t.Fatalf("release workflow missing supported platform %q\n%s", want, release)
		}
	}
	for _, want := range []string{"      - darwin\n", "      - linux\n", "      - windows\n"} {
		if !strings.Contains(gotGoReleaser, want) {
			t.Fatalf("GoReleaser config missing supported goos %q\n%s", want, goreleaser)
		}
	}
	// Windows is a supported target and ships as a zip (via format_overrides),
	// rather than the tar.gz used for darwin/linux.
	if !strings.Contains(gotGoReleaser, "goos: windows") {
		t.Fatalf("GoReleaser config must ship the windows zip archive\n%s", goreleaser)
	}
	// Intel macOS is not a release target; the build matrix must drop it.
	if !strings.Contains(gotGoReleaser, "goos: darwin\n        goarch: amd64") {
		t.Fatalf("GoReleaser config must ignore the darwin/amd64 combination\n%s", goreleaser)
	}
}
