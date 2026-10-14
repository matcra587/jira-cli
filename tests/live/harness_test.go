//go:build live

package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/output"
)

type liveIssue struct {
	Key     string          `json:"key"`
	ID      string          `json:"id"`
	Fields  json.RawMessage `json:"fields"`
	Details json.RawMessage `json:"details"`
}

type liveResult struct {
	Issue liveIssue       `json:"issue"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
}

type liveFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ContractVersion string `json:"contract_version"`
	} `json:"meta"`
	Data struct {
		Issue           liveIssue         `json:"issue"`
		Issues          []liveIssue       `json:"issues"`
		SearchPages     []json.RawMessage `json:"search_pages"`
		Results         []liveResult      `json:"results"`
		DryRun          bool              `json:"dry_run"`
		Attempted       bool              `json:"attempted"`
		Applied         bool              `json:"applied"`
		Receipt         json.RawMessage   `json:"receipt"`
		Truncated       bool              `json:"truncated"`
		TruncatedReason string            `json:"truncated_reason"`
		Comments        []struct {
			ID   string          `json:"id"`
			Body json.RawMessage `json:"body"`
		} `json:"comments"`
		Pagination struct {
			IsLast        bool   `json:"is_last"`
			NextPageToken string `json:"next_page_token"`
			Returned      int    `json:"returned"`
		} `json:"pagination"`
	} `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

type ownedIssue struct {
	Key         string            `json:"key"`
	ID          string            `json:"id"`
	Parent      string            `json:"parent,omitempty"`
	Deleted     bool              `json:"deleted"`
	Comments    []ownedComment    `json:"comments,omitempty"`
	Attachments []ownedAttachment `json:"attachments,omitempty"`
	Weblinks    []ownedWeblink    `json:"weblinks,omitempty"`
}

type ownedComment struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type liveManifest struct {
	RunID        string          `json:"run_id"`
	BaseURL      string          `json:"base_url"`
	Project      string          `json:"project"`
	Attempts     []string        `json:"create_attempts"`
	Issues       []ownedIssue    `json:"issues"`
	BinarySHA256 string          `json:"binary_sha256"`
	StartedAt    string          `json:"started_at"`
	BulkTasks    []ownedBulkTask `json:"bulk_tasks,omitempty"`
	Links        []ownedLink     `json:"links,omitempty"`
}

type liveRun struct {
	binary           string
	dir              string
	issueType        string
	account          string
	manifest         liveManifest
	sequence         int
	verified         bool
	deleteChildren   map[string][]string
	subtaskTypes     map[string]bool
	adfBodies        map[string]bool
	rawInputs        map[string]rawInput
	issueInputs      map[string]nativeIssueInput
	transitionInputs map[string]nativeTransitionInput
	worklogInputs    map[string]nativeWorklogInput
	moveInputs       map[string]nativeMoveInput
	moveProjectID    string
	moveTypes        map[string]bool
	attachmentInputs map[string][sha256.Size]byte
	watcherAccountID string
	watcherQuery     string
	commentInputs    map[string]nativeCommentInput
	commentRole      string
	linkTypes        map[string]storedLinkType
	linkInputs       map[string]nativeLinkInput
	weblinkInputs    []ownedWeblinkInput
	rankInputs       []ownedRankInput
}

func newLiveRun(t *testing.T) *liveRun {
	t.Helper()
	if os.Getenv("JIRA_LIVE_ACCEPTANCE") != "1" {
		t.Skip("live writes require explicit JIRA_LIVE_ACCEPTANCE=1 and dedicated fixture settings")
	}
	r := &liveRun{
		binary:    requiredEnv(t, "JIRA_LIVE_BINARY"),
		issueType: requiredEnv(t, "JIRA_LIVE_ISSUE_TYPE"),
		manifest: liveManifest{
			RunID:     "v016-acceptance-" + output.NewRequestID(),
			BaseURL:   requiredEnv(t, "JIRA_LIVE_BASE_URL"),
			Project:   requiredEnv(t, "JIRA_LIVE_PROJECT"),
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
	binary, err := os.Open(r.binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, binary)
	closeErr := binary.Close()
	if hashErr != nil || closeErr != nil {
		t.Fatal("cannot fingerprint the selected live binary")
	}
	r.manifest.BinarySHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	r.dir, err = os.MkdirTemp(os.Getenv("JIRA_LIVE_EVIDENCE_DIR"), "jira-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("local live evidence: %s", r.dir)
	r.save(t)
	t.Cleanup(func() { r.cleanup(t) })
	version := r.success(t, "version")
	if version.Meta.ContractVersion != "1" {
		t.Fatal("selected live binary does not expose the rebuilt contract")
	}
	// Resolve the existing keyring configuration before pinning the expected
	// host. Never send credentials to an environment-supplied host to discover
	// whether that host was the configured one.
	frame, stdout, exit := r.invoke(t, t.Context(), nil, "auth", "status")
	var auth struct {
		Data struct {
			BaseURL     string `json:"base_url"`
			Email       string `json:"email"`
			TokenSource string `json:"token_source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout, &auth); err != nil || exit != 0 || !frame.OK || auth.Data.BaseURL != r.manifest.BaseURL || auth.Data.TokenSource != "keyring" || strings.TrimSpace(auth.Data.Email) == "" {
		t.Fatal("live preflight must authenticate through the existing keyring on the expected site")
	}
	r.account = auth.Data.Email
	r.verified = true
	return r
}

func requiredEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("required live setting %s is empty", key)
	}
	return value
}

// invoke never replays a command. Raw stdout and stderr stay in local evidence,
// including ambiguous failures; public test output reports only codes and steps.
func (r *liveRun) invoke(t *testing.T, ctx context.Context, extraEnv []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	flagsAt := slices.Index(args, "--")
	if flagsAt < 0 {
		flagsAt = len(args)
	}
	args = slices.Insert(slices.Clone(args), flagsAt, "--output=json", "--no-input", "--timeout=30s")
	stdout, exit := r.invokeRaw(t, ctx, extraEnv, args...)
	var frame liveFrame
	if err := json.Unmarshal(stdout, &frame); err != nil {
		t.Errorf("step %d: stdout is not one JSON frame: %v", r.sequence, err)
	}
	return frame, stdout, exit
}

// invokeRaw retains process receipts for completion callbacks, whose stdout is
// a candidate list rather than a JSON frame. Domain invocations keep their
// machine, timeout and headless flags in invoke.
func (r *liveRun) invokeRaw(t *testing.T, ctx context.Context, extraEnv []string, args ...string) ([]byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Env = append(os.Environ(), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "NO_COLOR=1", "TERM=dumb")
	if r.verified {
		cmd.Env = append(cmd.Env, "JIRA_BASE_URL="+r.manifest.BaseURL, "JIRA_READ_ONLY=false")
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			t.Fatalf("live binary invocation failed: %v", err)
		}
		exit = exited.ExitCode()
	}
	r.sequence++
	for suffix, data := range map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes()} {
		path := filepath.Join(r.dir, fmt.Sprintf("%03d-%s.txt", r.sequence, suffix))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Errorf("save live receipt: %v", err)
		}
	}
	return stdout.Bytes(), exit
}

func (r *liveRun) success(t *testing.T, args ...string) liveFrame {
	t.Helper()
	f, _, exit := r.invoke(t, t.Context(), nil, args...)
	if exit != 0 || !f.OK {
		t.Fatalf("step %d failed: exit %d, errors %+v; inspect local receipts", r.sequence, exit, f.Errors)
	}
	return f
}

func (r *liveRun) save(t *testing.T) {
	t.Helper()
	if err := r.writeManifest(); err != nil {
		t.Fatalf("save ownership manifest: %v", err)
	}
}

func (r *liveRun) writeManifest() error {
	data, err := json.MarshalIndent(r.manifest, "", "  ")
	if err != nil {
		return err
	}
	return xos.AtomicWrite(filepath.Join(r.dir, "manifest.json"), data, 0o600)
}

func (r *liveRun) register(t *testing.T, issue liveIssue) {
	t.Helper()
	if !strings.HasPrefix(issue.Key, r.manifest.Project+"-") || issue.ID == "" {
		t.Fatal("created issue receipt lacks the expected project identity; inspect the recorded create attempt, never replay it blindly")
	}
	for _, owned := range r.manifest.Issues {
		if owned.Key == issue.Key {
			t.Fatal("creation returned an already registered identity")
		}
	}
	r.manifest.Issues = append(r.manifest.Issues, ownedIssue{Key: issue.Key, ID: issue.ID})
	r.save(t)
}

func (r *liveRun) owns(key string) bool {
	for _, owned := range r.manifest.Issues {
		if owned.Key == key {
			return true
		}
	}
	return false
}

func (r *liveRun) mutate(t *testing.T, keys []string, args ...string) liveFrame {
	t.Helper()
	if err := r.checkMutation(keys, args); err != nil {
		t.Fatal(err)
	}
	return r.success(t, args...)
}

var liveKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`)

func (r *liveRun) checkMutation(keys, args []string) error {
	if !r.verified || len(keys) == 0 {
		return errors.New("live mutation requires verified credentials and explicit owned targets")
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "transition" {
		for _, arg := range args[3:] {
			flag, _, _ := strings.Cut(arg, "=")
			if flag == "--json-input" || flag == "--from-json" || flag == "--body-adf" {
				return r.checkNativeTransitionMutation(keys, args)
			}
		}
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "delete" {
		return r.checkIssueDeleteMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "api" {
		return r.checkRawMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "epic" {
		return r.checkEpicMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "worklog" {
		return r.checkWorklogMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "move" {
		return r.checkMoveMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "attachment" {
		return r.checkAttachmentMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "watchers" {
		return r.checkWatcherMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "comment" && (args[2] == "edit" || args[2] == "delete") {
		return r.checkCommentMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "link" {
		return r.checkLinkMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "weblink" {
		return r.checkWeblinkMutation(keys, args)
	}
	if len(args) >= 3 && args[0] == "issue" && args[1] == "rank" {
		return r.checkRankMutation(keys, args)
	}
	if len(args) < 3 || args[0] != "issue" {
		return errors.New("unsupported live mutation grammar")
	}
	firstKey := 2
	switch args[1] {
	case "edit", "delete", "clone", "transition":
	case "comment":
		firstKey = 3
		if args[2] != "add" {
			return errors.New("unsupported live mutation grammar")
		}
	default:
		return errors.New("unsupported live mutation grammar")
	}
	if len(args) <= firstKey || !r.owns(args[firstKey]) {
		return errors.New("live mutation must start with a confirmed owned target")
	}
	for _, key := range keys {
		if !r.owns(key) {
			return errors.New("refusing mutation of an issue absent from this run's ownership manifest")
		}
	}
	if args[1] == "edit" && len(args) >= 4 && args[3] == "--json-input" {
		return r.checkNativeIssueMutation(keys, args)
	}
	for i, arg := range args {
		value := arg
		if _, flagValue, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(arg, "--") {
			value = flagValue
		}
		if liveKey.MatchString(value) && !r.owns(value) {
			return errors.New("mutation arguments contain an unowned issue")
		}
		flag, _, _ := strings.Cut(arg, "=")
		switch flag {
		case "--jql", "--filter", "--from-file":
			return errors.New("live harness refuses indirect mutation targets")
		case "--json-input", "--from-json", "--body-adf":
			// On comment add, native JSON is only the comment's ADF body,
			// never an issue payload or selector. Permit only files authored
			// and registered by this run; other mutation payloads stay closed.
			bodyPath := value
			if arg == flag && i+1 < len(args) {
				bodyPath = args[i+1]
			}
			isCommentAdd := args[1] == "comment" && args[2] == "add"
			if !isCommentAdd || !r.adfBodies[bodyPath] {
				return errors.New("live harness refuses indirect mutation targets")
			}
		}
	}
	return nil
}

func (r *liveRun) nativeBody(t *testing.T, data []byte) string {
	t.Helper()
	if err := adf.Validate(data); err != nil {
		t.Fatal("authored live body does not conform to the stable ADF schema")
	}
	path := filepath.Join(r.dir, "body-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.adfBodies == nil {
		r.adfBodies = make(map[string]bool)
	}
	r.adfBodies[path] = true
	return path
}

func (r *liveRun) create(t *testing.T, suffix string) string {
	t.Helper()
	summary := r.manifest.RunID + " " + suffix
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	f := r.success(t, "issue", "create", "--project", r.manifest.Project,
		"--type", r.issueType, "--summary", summary, "--label", r.manifest.RunID,
		"--markdown", "Acceptance **bold** <&> \u2028 end", "--verify")
	r.register(t, f.Data.Issue)
	var receipt liveIssue
	if !f.Data.Attempted || !f.Data.Applied || json.Unmarshal(f.Data.Receipt, &receipt) != nil || receipt.ID != f.Data.Issue.ID || receipt.Key != f.Data.Issue.Key {
		t.Fatal("creation lost its native acknowledgement or assigned identity")
	}
	return f.Data.Issue.Key
}

func (r *liveRun) cleanup(t *testing.T) {
	if !r.finishMoveTasks(t) {
		return
	}
	for i := len(r.manifest.Issues) - 1; i >= 0; i-- {
		owned := &r.manifest.Issues[i]
		if owned.Deleted {
			continue
		}
		// Cleanup is a fresh invocation and never includes an inferred key.
		// testing.T.Context is canceled before cleanup begins. A fresh,
		// bounded context must still let cleanup remove owned fixtures.
		f, _, exit := r.invoke(t, context.Background(), nil, "issue", "delete", owned.Key, "--force")
		if exit == 0 && f.OK && len(f.Data.Results) == 1 && f.Data.Results[0].OK && f.Data.Results[0].Issue.Key == owned.Key {
			missing, _, missingExit := r.invoke(t, context.Background(), nil, "issue", "view", owned.Key)
			if missingExit == 2 && !missing.OK && len(missing.Errors) == 1 && missing.Errors[0].Code == "jira_not_found" {
				owned.Deleted = true
				for j := range owned.Comments {
					owned.Comments[j].Deleted = true
				}
				for j := range owned.Attachments {
					owned.Attachments[j].Deleted = true
				}
			} else {
				t.Errorf("owned fixture deletion was not confirmed by a not-found read")
			}
		} else {
			t.Errorf("cleanup of owned fixture failed; inspect manifest and receipts")
		}
		if err := r.writeManifest(); err != nil {
			t.Errorf("save cleanup progress: %v", err)
		}
	}
	r.finishOwnedLinks(t)
	r.finishOwnedWeblinks(t)
}
