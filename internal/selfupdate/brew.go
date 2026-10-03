package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	xos "github.com/gechr/x/os"
)

const (
	brewFormula           = "matcra587/tap/jira"
	maxProcessOutput      = 1 << 20
	brewUpdateTimeout     = 10 * time.Minute
	brewInspectionTimeout = 5 * time.Second
)

var (
	// ErrBrewOwnership refuses an upgrade unless the formula owns this executable.
	ErrBrewOwnership = errors.New("homebrew formula does not own the running executable")
	// ErrBrewInspection means an executable's version or ownership could not be verified.
	ErrBrewInspection = errors.New("homebrew installation inspection failed")
	// ErrProcessOutput bounds captured subprocess output without blocking pipe drains.
	ErrProcessOutput = errors.New("update subprocess output exceeds limit")
)

// BrewInstallation records independently inspected formula and executable facts.
type BrewInstallation struct {
	Executable string
	Version    string
	SHA256     string
	Head       bool
}

// BrewOutcome retains an upgrade attempt and its observed installation even on
// failure. Updated describes changed executable bytes, not a subprocess exit code.
type BrewOutcome struct {
	Attempted bool
	Updated   bool
	Uncertain bool
	Before    BrewInstallation
	After     *BrewInstallation
	Shadow    string
}

// Brew retains a preflight snapshot for one explicitly requested update. Unlike
// clive/updater/brew 0.4.9, it captures bounded output in verbose mode, preserves
// cancellation causes and inspects outcomes after a failed upgrade. Clive's
// public Update returns only an error and cannot provide these receipt facts.
// Stray PATH copies and old kegs are preserved; no uninstall or cleanup runs.
type Brew struct {
	program string
	before  BrewInstallation
	run     func(context.Context, string, ...string) ([]byte, bool, error)
}

// OpenBrew verifies the tap-qualified installed formula before any mutation.
// Path-shaped provenance alone is insufficient, including Go installs in Cellar.
func OpenBrew(ctx context.Context, executable string) (*Brew, error) {
	program, err := exec.LookPath("brew")
	if err != nil {
		return nil, fmt.Errorf("locate homebrew: %w", err)
	}
	b := &Brew{program: program, run: runUpdateProcess}
	before, err := b.inspect(ctx)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve running executable: %w", err)
	}
	if resolved != before.Executable {
		return nil, ErrBrewOwnership
	}
	b.before = before
	return b, nil
}

// Update refreshes metadata and upgrades only the preflighted formula, without
// replaying a failed mutation. A short independent read after the upgrade may
// outlive cancellation so its receipt can distinguish publication from failure.
func (b *Brew) Update(ctx context.Context) (BrewOutcome, error) {
	out := BrewOutcome{Before: b.before}
	ctx, cancel := context.WithTimeout(ctx, brewUpdateTimeout)
	defer cancel()
	if err := b.checkUnchanged(ctx); err != nil {
		return out, err
	}
	if _, _, err := b.run(ctx, b.program, "update", "--quiet"); err != nil {
		return out, fmt.Errorf("refresh homebrew formulae: %w", err)
	}
	if err := b.checkUnchanged(ctx); err != nil {
		return out, err
	}
	args := []string{"upgrade"}
	if b.before.Head {
		args = append(args, "--fetch-HEAD")
	}
	args = append(args, brewFormula)
	var err error
	_, out.Attempted, err = b.run(ctx, b.program, args...)
	if !out.Attempted {
		return out, err
	}
	inspectCtx, inspectCancel := context.WithTimeout(context.WithoutCancel(ctx), brewInspectionTimeout)
	defer inspectCancel()
	after, inspectErr := b.inspect(inspectCtx)
	if inspectErr != nil {
		out.Uncertain = true
		return out, errors.Join(err, fmt.Errorf("inspect upgraded installation: %w", inspectErr), ctx.Err())
	}
	out.After = &after
	out.Updated = after.SHA256 != b.before.SHA256
	if path, lookupErr := exec.LookPath("jira"); lookupErr == nil {
		same, compareErr := xos.SameFile(path, after.Executable)
		if compareErr != nil || !same {
			out.Shadow = path
		}
	}
	return out, errors.Join(err, ctx.Err())
}

func (b *Brew) checkUnchanged(ctx context.Context) error {
	current, err := b.inspect(ctx)
	if err != nil {
		return err
	}
	if current != b.before {
		return ErrTargetChanged
	}
	return nil
}

func (b *Brew) inspect(ctx context.Context) (BrewInstallation, error) {
	var out BrewInstallation
	metadata, _, err := b.run(ctx, b.program, "info", "--json=v2", "--formula", brewFormula)
	if err != nil {
		return out, fmt.Errorf("inspect homebrew formula: %w", err)
	}
	var info struct {
		Formulae []struct {
			FullName  string            `json:"full_name"`
			Installed []json.RawMessage `json:"installed"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal(metadata, &info); err != nil {
		return out, fmt.Errorf("%w: invalid formula metadata: %w", ErrBrewInspection, err)
	}
	if len(info.Formulae) != 1 || info.Formulae[0].FullName != brewFormula || len(info.Formulae[0].Installed) == 0 {
		return out, ErrBrewOwnership
	}
	prefix, _, err := b.run(ctx, b.program, "--prefix", "--installed", brewFormula)
	if err != nil {
		return out, fmt.Errorf("resolve installed formula prefix: %w", err)
	}
	path := strings.TrimSpace(string(prefix))
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
		return out, ErrBrewOwnership
	}
	keg, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out, fmt.Errorf("resolve installed keg: %w", err)
	}
	binary := filepath.Join(keg, "bin", "jira")
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return out, fmt.Errorf("resolve formula executable: %w", err)
	}
	relative, err := filepath.Rel(keg, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return out, ErrBrewOwnership
	}
	files, _, err := b.run(ctx, b.program, "list", "--formula", brewFormula)
	if err != nil {
		return out, fmt.Errorf("list installed formula files: %w", err)
	}
	listed := strings.Split(strings.TrimSpace(string(files)), "\n")
	if !slices.Contains(listed, binary) && !slices.Contains(listed, resolved) {
		return out, ErrBrewOwnership
	}
	target, err := OpenTarget(ctx, resolved)
	if err != nil {
		return out, fmt.Errorf("inspect formula executable: %w", err)
	}
	version, versionErr := b.version(ctx, resolved)
	checkErr := target.checkOriginal(ctx)
	closeErr := target.Close()
	if err := errors.Join(versionErr, checkErr, closeErr); err != nil {
		return out, err
	}
	return BrewInstallation{
		Executable: resolved, Version: version, SHA256: fmt.Sprintf("%x", target.digest),
		Head: strings.HasPrefix(filepath.Base(keg), "HEAD-"),
	}, nil
}

func (b *Brew) version(ctx context.Context, executable string) (string, error) {
	data, _, err := b.run(ctx, executable, "version", "--output=json")
	if err != nil {
		return "", fmt.Errorf("read installed version: %w", err)
	}
	var result struct {
		OK   bool `json:"ok"`
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("%w: invalid version envelope: %w", ErrBrewInspection, err)
	}
	if err := validateVersion(result.Data.Version); err != nil || !result.OK {
		return "", fmt.Errorf("%w: invalid installed version", ErrBrewInspection)
	}
	return result.Data.Version, nil
}

// runUpdateProcess keeps subprocesses off the command's output streams. A
// draining bounded writer prevents memory growth without stranding child pipes.
// WaitDelay bounds inherited pipes when a child exits or cancellation kills it.
func runUpdateProcess(ctx context.Context, program string, args ...string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	cmd := exec.CommandContext(ctx, program, args...) //nolint:gosec // locally resolved executable and fixed operation arguments; no shell
	configureUpdateProcess(cmd)
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1")
	cmd.WaitDelay = time.Second
	var stdout, stderr processBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, false, errors.Join(err, ctx.Err())
	}
	err := cmd.Wait()
	if err != nil {
		if detail := strings.TrimSpace(stderr.data.String()); detail != "" {
			err = fmt.Errorf("update subprocess: %s: %w", detail, err)
		}
	}
	if stdout.truncated || stderr.truncated {
		err = errors.Join(err, ErrProcessOutput)
	}
	return stdout.data.Bytes(), true, errors.Join(err, ctx.Err())
}

type processBuffer struct {
	// Do not embed Buffer: its promoted ReadFrom bypasses the Write bound
	// when os/exec drains a subprocess pipe through io.Copy.
	data      bytes.Buffer
	truncated bool
}

func (b *processBuffer) Write(data []byte) (int, error) {
	size := len(data)
	remaining := maxProcessOutput - b.data.Len()
	if size > remaining {
		b.truncated = true
		data = data[:remaining]
	}
	if _, err := b.data.Write(data); err != nil {
		return 0, err
	}
	return size, nil
}
