package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
)

type brewFixture struct {
	brew     *Brew
	keg      string
	binary   string
	version  string
	metadata string
	files    string
	commands [][]string
	onRun    func(context.Context, string, []string) ([]byte, bool, error, bool)
}

func newBrewFixture(t *testing.T) *brewFixture {
	t.Helper()
	keg := filepath.Join(t.TempDir(), "Cellar", "jira", "1.2.3")
	binary := filepath.Join(keg, "bin", "jira")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("owned original executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &brewFixture{
		keg: keg, binary: binary, version: "v1.2.3", files: binary + "\n",
		metadata: `{"formulae":[{"full_name":"matcra587/tap/jira","installed":[{"version":"1.2.3"}]}]}`,
	}
	f.brew = &Brew{program: "owned-brew", run: f.run}
	return f
}

func (f *brewFixture) run(ctx context.Context, program string, args ...string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	f.commands = append(f.commands, slices.Clone(args))
	if f.onRun != nil {
		data, started, err, handled := f.onRun(ctx, program, args)
		if handled {
			return data, started, err
		}
	}
	if program == f.binary {
		return fmt.Appendf(nil, `{"ok":true,"data":{"version":%q}}`, f.version), true, nil
	}
	switch args[0] {
	case "info":
		return []byte(f.metadata), true, nil
	case "--prefix":
		return []byte(f.keg + "\n"), true, nil
	case "list":
		return []byte(f.files), true, nil
	case "update", "upgrade":
		return nil, true, nil
	default:
		return nil, false, fmt.Errorf("unexpected owned operation %v", args)
	}
}

func (f *brewFixture) prepare(t *testing.T) {
	t.Helper()
	before, err := f.brew.inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.brew.before = before
	f.commands = nil
}

func TestBrewInspectionRefusesUnownedInstallation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		alter func(*testing.T, *brewFixture)
		want  error
	}{
		{name: "wrong tap", alter: func(_ *testing.T, f *brewFixture) {
			f.metadata = strings.ReplaceAll(f.metadata, "matcra587/tap/jira", "other/tap/jira")
		}, want: ErrBrewOwnership},
		{name: "not installed", alter: func(_ *testing.T, f *brewFixture) {
			f.metadata = `{"formulae":[{"full_name":"matcra587/tap/jira","installed":[]}]}`
		}, want: ErrBrewOwnership},
		{name: "missing metadata", alter: func(_ *testing.T, f *brewFixture) { f.metadata = `{}` }, want: ErrBrewOwnership},
		{name: "trailing metadata", alter: func(_ *testing.T, f *brewFixture) { f.metadata += `{}` }, want: ErrBrewInspection},
		{name: "unlisted executable", alter: func(_ *testing.T, f *brewFixture) { f.files = filepath.Join(f.keg, "README") }, want: ErrBrewOwnership},
		{name: "relative prefix", alter: func(_ *testing.T, f *brewFixture) { f.keg = "Cellar/jira" }, want: ErrBrewOwnership},
		{name: "multiple prefixes", alter: func(_ *testing.T, f *brewFixture) { f.keg += "\n/other" }, want: ErrBrewOwnership},
		{name: "invalid version", alter: func(_ *testing.T, f *brewFixture) { f.version = "unknown" }, want: ErrBrewInspection},
		{name: "external symlink", alter: func(t *testing.T, f *brewFixture) {
			outside := filepath.Join(t.TempDir(), "jira")
			if err := os.WriteFile(outside, []byte("unowned"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.binary); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, f.binary); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}, want: ErrBrewOwnership},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newBrewFixture(t)
			tc.alter(t, f)
			if _, err := f.brew.inspect(t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("inspection error = %v, want %v", err, tc.want)
			}
			for _, args := range f.commands {
				if args[0] == "update" || args[0] == "upgrade" {
					t.Fatalf("inspection mutated: %v", f.commands)
				}
			}
		})
	}
}

func TestBrewUpdateObservesActualBytes(t *testing.T) {
	t.Parallel()
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			t.Parallel()
			f := newBrewFixture(t)
			f.prepare(t)
			f.onRun = func(_ context.Context, _ string, args []string) ([]byte, bool, error, bool) {
				if args[0] == "upgrade" && changed {
					if err := os.WriteFile(f.binary, []byte("owned replacement executable"), 0o700); err != nil {
						t.Fatal(err)
					}
					f.version = "v1.3.0"
				}
				return nil, false, nil, false
			}
			out, err := f.brew.Update(t.Context())
			if err != nil || !out.Attempted || out.Updated != changed || out.Uncertain || out.After == nil {
				t.Fatalf("outcome = %+v, error = %v", out, err)
			}
			if out.Before.Version != "v1.2.3" || out.After.Version != f.version || out.After.SHA256 == "" {
				t.Fatalf("missing independent observation: %+v", out)
			}
			assertBrewMutations(t, f.commands, 1, 1)
		})
	}
}

func TestBrewUpdateRetainsPublicationAfterFailure(t *testing.T) {
	t.Parallel()
	f := newBrewFixture(t)
	f.prepare(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.onRun = func(_ context.Context, _ string, args []string) ([]byte, bool, error, bool) {
		if args[0] != "upgrade" {
			return nil, false, nil, false
		}
		if err := os.WriteFile(f.binary, []byte("published before cancellation"), 0o700); err != nil {
			t.Fatal(err)
		}
		f.version = "v1.3.0"
		cancel()
		return nil, true, context.Canceled, true
	}
	out, err := f.brew.Update(ctx)
	if !errors.Is(err, context.Canceled) || !out.Attempted || !out.Updated || out.Uncertain || out.After == nil || out.After.Version != "v1.3.0" {
		t.Fatalf("canceled publication lost: %+v, %v", out, err)
	}
	assertBrewMutations(t, f.commands, 1, 1)
}

func TestBrewUpdateDoesNotInferPublicationFromVersion(t *testing.T) {
	t.Parallel()
	f := newBrewFixture(t)
	f.prepare(t)
	f.onRun = func(_ context.Context, _ string, args []string) ([]byte, bool, error, bool) {
		if args[0] == "upgrade" {
			f.version = "v1.3.0"
		}
		return nil, false, nil, false
	}
	out, err := f.brew.Update(t.Context())
	if err != nil || !out.Attempted || out.Updated || out.Uncertain || out.After == nil || out.After.Version != "v1.3.0" {
		t.Fatalf("version change fabricated publication: %+v, %v", out, err)
	}
	if out.Before.SHA256 != out.After.SHA256 {
		t.Fatal("test executable changed")
	}
}

func TestBrewUpdateFailureBoundaries(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"canceled", "refresh", "upgrade start", "upgrade", "upgrade and inspection"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			f := newBrewFixture(t)
			f.prepare(t)
			failure := errors.New("owned subprocess failure")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if phase == "canceled" {
				cancel()
				failure = context.Canceled
			}
			f.onRun = func(_ context.Context, _ string, args []string) ([]byte, bool, error, bool) {
				if args[0] == "update" && phase == "refresh" {
					return nil, true, failure, true
				}
				if args[0] == "upgrade" {
					if phase == "upgrade and inspection" {
						f.version = "unknown"
					}
					return nil, phase != "upgrade start", failure, true
				}
				return nil, false, nil, false
			}
			out, err := f.brew.Update(ctx)
			attempted := phase == "upgrade" || phase == "upgrade and inspection"
			uncertain := phase == "upgrade and inspection"
			if !errors.Is(err, failure) || out.Attempted != attempted || out.Updated || out.Uncertain != uncertain {
				t.Fatalf("failure outcome = %+v, %v", out, err)
			}
			if (out.After != nil) != (phase == "upgrade") {
				t.Fatalf("incorrect post-attempt observation: %+v", out)
			}
			if uncertain && !errors.Is(err, ErrBrewInspection) {
				t.Fatalf("inspection failure lost: %v", err)
			}
			refreshes, upgrades := 1, 1
			switch phase {
			case "canceled":
				refreshes, upgrades = 0, 0
			case "refresh":
				upgrades = 0
			}
			assertBrewMutations(t, f.commands, refreshes, upgrades)
		})
	}
}

func TestBrewInspectionRejectsInvalidVersionEnvelopes(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		`{"ok":false,"data":{"version":"v1.2.3"}}`,
		`{"ok":true,"data":{}}`,
		`{"ok":true,"data":{"version":"v1.2.3"}}{}`,
	} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			f := newBrewFixture(t)
			f.onRun = func(_ context.Context, program string, _ []string) ([]byte, bool, error, bool) {
				if program == f.binary {
					return []byte(data), true, nil, true
				}
				return nil, false, nil, false
			}
			if _, err := f.brew.inspect(t.Context()); !errors.Is(err, ErrBrewInspection) {
				t.Fatalf("invalid version accepted: %v", err)
			}
		})
	}
}

func TestBrewUpdateReportsFailedInspection(t *testing.T) {
	t.Parallel()
	f := newBrewFixture(t)
	f.prepare(t)
	f.onRun = func(_ context.Context, _ string, args []string) ([]byte, bool, error, bool) {
		if args[0] == "upgrade" {
			f.version = "unknown"
		}
		return nil, false, nil, false
	}
	out, err := f.brew.Update(t.Context())
	if !errors.Is(err, ErrBrewInspection) || !out.Attempted || out.Updated || !out.Uncertain || out.After != nil {
		t.Fatalf("unverified installation reported success: %+v, %v", out, err)
	}
}

func TestBrewUpdateStopsBeforeUpgradeOnChange(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"before refresh", "after refresh"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			f := newBrewFixture(t)
			f.prepare(t)
			change := func() {
				if err := os.WriteFile(f.binary, []byte("intervening update"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "before refresh" {
				change()
			}
			f.onRun = func(_ context.Context, _ string, args []string) ([]byte, bool, error, bool) {
				if args[0] == "update" && phase == "after refresh" {
					change()
				}
				return nil, false, nil, false
			}
			out, err := f.brew.Update(t.Context())
			if !errors.Is(err, ErrTargetChanged) || out.Attempted || out.Updated {
				t.Fatalf("intervening installation overwritten: %+v, %v", out, err)
			}
			refreshes := 0
			if phase == "after refresh" {
				refreshes = 1
			}
			assertBrewMutations(t, f.commands, refreshes, 0)
		})
	}
}

func TestBrewUpdateKeepsHeadChannel(t *testing.T) {
	t.Parallel()
	f := newBrewFixture(t)
	head := filepath.Join(filepath.Dir(f.keg), "HEAD-abcdef012345")
	if err := os.Rename(f.keg, head); err != nil {
		t.Fatal(err)
	}
	f.keg, f.binary = head, filepath.Join(head, "bin", "jira")
	f.files = f.binary + "\n"
	f.prepare(t)
	if !f.brew.before.Head {
		t.Fatal("active HEAD keg not recognized")
	}
	if _, err := f.brew.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, args := range f.commands {
		if args[0] == "upgrade" && !slices.Equal(args, []string{"upgrade", "--fetch-HEAD", "matcra587/tap/jira"}) {
			t.Fatalf("HEAD upgrade arguments = %v", args)
		}
	}
	assertBrewMutations(t, f.commands, 1, 1)
}

func assertBrewMutations(t *testing.T, commands [][]string, refreshes, upgrades int) {
	t.Helper()
	var actualRefreshes, actualUpgrades int
	for _, args := range commands {
		switch args[0] {
		case "update":
			actualRefreshes++
		case "upgrade":
			actualUpgrades++
		case "info", "--prefix", "list", "version":
		default:
			t.Fatalf("unexpected mutation: %v", args)
		}
	}
	if actualRefreshes != refreshes || actualUpgrades != upgrades {
		t.Fatalf("refresh/upgrade counts = %d/%d, want %d/%d", actualRefreshes, actualUpgrades, refreshes, upgrades)
	}
}

func TestUpdateProcessCapturesAndBoundsOutput(t *testing.T) {
	t.Setenv("JIRA_OWNED_UPDATE_HELPER", "1")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want error
	}{
		{name: "success"}, {name: "failure"}, {name: "large stdout", want: ErrProcessOutput}, {name: "large stderr", want: ErrProcessOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, started, err := runUpdateProcess(t.Context(), program, "-test.run=^TestUpdateProcessHelper$", "--", tc.name)
			if !started || len(out) > maxProcessOutput {
				t.Fatalf("started=%t, output bytes=%d", started, len(out))
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if tc.name == "success" && (err != nil || string(out) != "owned stdout\n") {
				t.Fatalf("output=%q, error=%v", out, err)
			}
			if tc.name == "failure" {
				if exit, ok := errors.AsType[*os.PathError](err); ok {
					t.Fatalf("helper did not execute: %v", exit)
				}
				if err == nil || !strings.Contains(err.Error(), "owned stderr") {
					t.Fatalf("failure missing context: %v", err)
				}
			}
		})
	}
}

func TestUpdateProcessPreservesCancellation(t *testing.T) {
	t.Setenv("JIRA_OWNED_UPDATE_HELPER", "1")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, started, err := runUpdateProcess(ctx, program, "-test.run=^TestUpdateProcessHelper$", "--", "wait")
	if !started || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline cause lost: started=%t, error=%v", started, err)
	}
}

func TestUpdateProcessHelper(t *testing.T) {
	if os.Getenv("JIRA_OWNED_UPDATE_HELPER") != "1" {
		return
	}
	args := os.Args
	mode := args[len(args)-1]
	for _, name := range []string{"HOMEBREW_NO_ENV_HINTS", "HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSTALL_CLEANUP"} {
		if os.Getenv(name) != "1" {
			os.Exit(19)
		}
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(input) != 0 {
		os.Exit(20)
	}
	switch mode {
	case "success":
		_, _ = io.WriteString(os.Stdout, "owned stdout\n")
	case "failure":
		_, _ = io.WriteString(os.Stderr, "owned stderr\n")
		os.Exit(17)
	case "large stdout":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", maxProcessOutput+1))
	case "large stderr":
		_, _ = io.WriteString(os.Stderr, strings.Repeat("x", maxProcessOutput+1))
	case "wait":
		_, _ = io.WriteString(os.Stderr, "owned stderr before cancellation\n")
		time.Sleep(time.Minute)
	case "descendant parent":
		program, err := os.Executable()
		if err != nil {
			os.Exit(22)
		}
		child := exec.Command(program, "-test.run=^TestUpdateProcessHelper$", "--", "descendant child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Run(); err != nil {
			os.Exit(23)
		}
	case "descendant child":
		root := os.Getenv("JIRA_OWNED_UPDATE_PROCESS_ROOT")
		// Readiness and heartbeat readers must see complete records, including
		// when cancellation interrupts publication between filesystem calls.
		if err := xos.AtomicWrite(filepath.Join(root, "heartbeat"), []byte("0"), 0o600); err != nil {
			os.Exit(25)
		}
		if err := xos.AtomicWrite(filepath.Join(root, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(24)
		}
		for n := 1; ; n++ {
			if err := xos.AtomicWrite(filepath.Join(root, "heartbeat"), []byte(strconv.Itoa(n)), 0o600); err != nil {
				os.Exit(25)
			}
			time.Sleep(5 * time.Millisecond)
		}
	default:
		os.Exit(21)
	}
	os.Exit(0)
}
