package contract

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCIQualityGateRunsRequiredGoChecks(t *testing.T) {
	ci, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("ReadFile(ci) error = %v", err)
	}
	security, err := os.ReadFile("../../.github/workflows/security.yml")
	if err != nil {
		t.Fatalf("ReadFile(security) error = %v", err)
	}
	mise, err := os.ReadFile("../../.mise.toml")
	if err != nil {
		t.Fatalf("ReadFile(.mise.toml) error = %v", err)
	}
	hk, err := os.ReadFile("../../hk.pkl")
	if err != nil {
		t.Fatalf("ReadFile(hk.pkl) error = %v", err)
	}
	tasks, err := os.ReadFile("../../tasks.toml")
	if err != nil {
		t.Fatalf("ReadFile(tasks.toml) error = %v", err)
	}
	buildTask, err := os.ReadFile("../../.mise/tasks/build")
	if err != nil {
		t.Fatalf("ReadFile(.mise/tasks/build) error = %v", err)
	}
	buildEnv, err := os.ReadFile("../../.mise/tasks/lib/build-env.sh")
	if err != nil {
		t.Fatalf("ReadFile(.mise/tasks/lib/build-env.sh) error = %v", err)
	}
	combined := strings.Join([]string{
		string(ci),
		string(security),
		string(mise),
		string(hk),
		string(tasks),
		string(buildTask),
		string(buildEnv),
	}, "\n")
	for _, want := range []string{
		"-race",
		"go vet ./...",
		"run = \"golangci-lint run ./...\"",
		"&& golangci-lint run",
		"go tool govulncheck ./...",
		"go mod tidy",
		"git diff --exit-code",
		"goreleaser check",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("quality gates missing %q\nCI:\n%s\nmise:\n%s\nhk:\n%s\ntasks:\n%s", want, ci, mise, hk, tasks)
		}
	}
	for _, want := range []string{
		"matcra587/github-actions/.github/workflows/go-ci.yml@",
		"matcra587/github-actions/.github/workflows/md-lint.yml@",
		// Workflow linting moved local (hk's actionlint/zizmor builtins,
		// asserted below); the remote side is the shared security workflow,
		// pinned by SHA with SARIF uploads to code scanning.
		"matcra587/github-actions/.github/workflows/security.yml@",
		"security-events: write",
		"sarif: true",
		"lockfile = true",

		"[task_config]",
		"includes = [\"tasks.toml\"]",
		"Builtins.actionlint",
		"Builtins.zizmor",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("shared workflow gate missing %q\nCI:\n%s\nSecurity:\n%s", want, ci, security)
		}
	}
	for _, unwanted := range []string{"biome =", "bun =", "node ="} {
		if strings.Contains(string(mise), unwanted) {
			t.Fatalf("mise config should not include node-related tool %q:\n%s", unwanted, mise)
		}
	}
	// PRs and local checks must use the same release binary.
	localPin := regexp.MustCompile(`(?m)^golangci-lint = "([0-9.]+)"`).FindSubmatch(mise)
	if len(localPin) != 2 || !strings.Contains(string(ci), "golangci-version: v"+string(localPin[1])) {
		t.Fatal("local and CI golangci-lint versions must match")
	}
	for _, tool := range []string{"actionlint", "cosign", "hk", "pkl", "rumdl", "zizmor", "shellcheck"} {
		if !regexp.MustCompile(`(?m)^` + tool + ` = "[0-9]+\.[0-9]+\.[0-9]+"`).Match(mise) {
			t.Errorf("%s must have an exact version pin", tool)
		}
	}
	assertPinnedWorkflowUses(t, string(ci))
	assertPinnedWorkflowUses(t, string(security))
}

func TestCIWorkflowGatesMainPushesForRelease(t *testing.T) {
	ci, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("ReadFile(ci) error = %v", err)
	}
	got := string(ci)
	for _, want := range []string{
		// The release workflow awaits this gate instead of re-running the
		// suite at tag time, so main pushes must run the full local gate
		// plus the cross-platform snapshot build.
		"name: Release gate",
		"if: github.event_name == 'push'",
		"run: mise run ci",
		"run: mise run release:build",
		// Push runs group per commit so a rapid push series cannot cancel
		// a gate run that a release tag is waiting on.
		"group: ${{ github.workflow }}-${{ github.event_name == 'push' && github.sha || github.ref }}",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ci workflow missing release gate wiring %q\n%s", want, ci)
		}
	}
	if !strings.Contains(got, "push:") || !strings.Contains(got, "- main") {
		t.Fatalf("ci workflow must trigger on pushes to main\n%s", ci)
	}
}

func TestLocalCITaskIncludesReleasePreflightInputs(t *testing.T) {
	tasks, err := os.ReadFile("../../tasks.toml")
	if err != nil {
		t.Fatalf("ReadFile(tasks.toml) error = %v", err)
	}
	got := string(tasks)
	for _, want := range []string{
		`["workflow:lint"]`,
		`run = ["actionlint .github/workflows/*.yml", "zizmor --persona pedantic .github/"]`,
		`depends = ["check", "test:integration", "tidy", "workflow:lint", "security"]`,
		`run = ["mise run ci", "mise run release:check", "mise run release:build"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tasks.toml missing local CI gate %q\n%s", want, tasks)
		}
	}
}

func assertPinnedWorkflowUses(t *testing.T, workflow string) {
	t.Helper()
	uses := regexp.MustCompile(`(?m)^\s*(?:- )?uses: (\S+)`).FindAllStringSubmatch(workflow, -1)
	if len(uses) == 0 {
		t.Fatal("workflow has no action references")
	}
	for _, use := range uses {
		if !regexp.MustCompile(`@[a-f0-9]{40}$`).MatchString(use[1]) {
			t.Errorf("action reference must use a full commit SHA: %s", use[1])
		}
	}
}
