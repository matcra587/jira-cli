//go:build linux && live

package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type parityArchiveArtifact struct {
	Lane, Binary, Digest, Version string
}

type parityArchiveReceipt struct {
	Lane            string         `json:"lane"`
	Scenario        string         `json:"scenario"`
	Output          string         `json:"output"`
	BinarySHA256    string         `json:"binary_sha256"`
	InstalledSHA256 string         `json:"installed_sha256"`
	Argv            []string       `json:"argv"`
	Exit            int            `json:"exit"`
	Stdout          string         `json:"stdout"`
	Stderr          string         `json:"stderr"`
	Requests        map[string]int `json:"requests"`
}

func parityArchiveFrame(raw []byte, lane string, exit int) (data, failures json.RawMessage, valid bool) {
	return parityMachineFrame(raw, lane, "update", exit)
}

func parityArchiveOutput(raw []byte, lane, version, scenario, candidateDigest string, candidateBytes int) bool {
	if lane != "before" && lane != "after" {
		return false
	}
	failed := scenario == "confirmation" || scenario == "integrity" || scenario == "renamed" && lane == "before"
	if failed && lane == "before" {
		return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	want := map[string]any{"channel": "github-archive", "current": version, "managed": false, "updated": scenario == "apply" || scenario == "renamed", "dry_run": scenario == "preview"}
	if scenario != "confirmation" {
		latest := "v1.2.3"
		if scenario == "no_newer" {
			latest = "v0.1.0"
		}
		if lane == "before" {
			latest = strings.TrimPrefix(latest, "v")
		}
		want["latest"], want["update_available"] = latest, scenario != "no_newer"
	}
	if lane == "after" {
		want["attempted"], want["uncertain"] = scenario == "apply" || scenario == "renamed", false
		if scenario == "apply" || scenario == "renamed" {
			want["publication"] = map[string]any{"published": true, "durable": true, "uncertain": false, "bytes": candidateBytes, "sha256": candidateDigest}
		}
	}
	expected, _ := json.Marshal(want)
	return parityArchiveJSON(raw, expected)
}

func parityArchiveErrors(raw []byte, lane, scenario, archiveDigest string) bool {
	want := []any{}
	message, code, hint := "", "", ""
	switch lane {
	case "before":
		switch scenario {
		case "confirmation":
			message = "update requires --force in headless / agent / --no-input mode"
		case "integrity":
			message = fmt.Sprintf("updating jira-cli: failed validating asset content %q: expected %q, found %q: sha256 validation failed", "jira_1.2.3_linux_"+runtime.GOARCH+".tar.gz", strings.Repeat("0", 64), archiveDigest)
		case "renamed":
			message = `updating jira-cli: executable not found in tar: "jira-owned-installed"`
		}
		if message != "" {
			want = []any{map[string]any{"type": "validation", "code": "validation_failed", "message": message, "hint": "Check the values you passed and try again.", "retryable": false}}
		}
	case "after":
		switch scenario {
		case "confirmation":
			message, code = "updating this executable without an interactive terminal requires --force", "flag_required"
			hint = "Add the flag the command needs — run it with `--help` to see which."
		case "integrity":
			message, code = "jira-cli update failed", "update_integrity"
			hint = "Do not install the rejected download. Inspect the published release assets and checksums before retrying."
		}
		if message != "" {
			row := map[string]any{"code": code, "message": message, "hint": "Inspect retained data and verify mutation outcomes before retrying. " + hint}
			if scenario == "confirmation" {
				row["field"] = "force"
			}
			want = []any{row}
		}
	default:
		return false
	}
	expected, _ := json.Marshal(want)
	return parityArchiveJSON(raw, expected)
}

func parityArchiveRequests(lane, scenario string, requests map[string]int) bool {
	if lane != "before" && lane != "after" {
		return false
	}
	wantRequests := map[string]int{}
	if scenario != "confirmation" {
		metadataPath := "/repos/matcra587/jira-cli/releases/latest"
		reads := 1
		if lane == "before" {
			metadataPath = "/repos/matcra587/jira-cli/releases"
			if scenario != "preview" && scenario != "no_newer" {
				reads = 2
			}
		}
		wantRequests[metadataPath] = reads
		if scenario == "apply" || scenario == "renamed" || scenario == "integrity" {
			wantRequests["/repos/matcra587/jira-cli/releases/assets/10"] = 1
			wantRequests["/repos/matcra587/jira-cli/releases/assets/11"] = 1
		}
	}
	return reflect.DeepEqual(requests, wantRequests)
}

func parityArchiveFingerprint(t *testing.T, artifact parityArchiveArtifact) []byte {
	t.Helper()
	raw, err := os.ReadFile(artifact.Binary)
	if err != nil || len(artifact.Digest) != 64 || fmt.Sprintf("%x", sha256.Sum256(raw)) != artifact.Digest {
		t.Fatal("selected archive artifact missing or changed")
	}
	return raw
}

func parityArchiveOpenEvidence(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("archive acceptance requires an explicit absolute private evidence directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.Mkdir(path, 0o700)
}

func parityArchiveWriteReceipts(path string, receipts []parityArchiveReceipt) error {
	raw, err := json.MarshalIndent(receipts, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(path, "receipts.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(raw, '\n'))
	return errors.Join(err, file.Close())
}

func TestParityArchiveUpdateActualBinaries(t *testing.T) {
	if os.Getenv("JIRA_PARITY_UPDATE_BEFORE_BINARY") == "" && os.Getenv("JIRA_PARITY_UPDATE_AFTER_BINARY") == "" {
		t.Skip("requires explicit frozen legacy and supplementary archive-stamped current artifacts")
	}
	artifacts := []parityArchiveArtifact{
		{Lane: "before", Binary: os.Getenv("JIRA_PARITY_UPDATE_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_UPDATE_BEFORE_SHA256"), Version: "v0.15.2"},
		{Lane: "after", Binary: os.Getenv("JIRA_PARITY_UPDATE_AFTER_BINARY"), Digest: os.Getenv("JIRA_PARITY_UPDATE_AFTER_SHA256"), Version: "v0.16.0-dev"},
	}
	replacement := parityArchiveArtifact{Lane: "replacement", Binary: os.Getenv("JIRA_PARITY_UPDATE_REPLACEMENT_BINARY"), Digest: os.Getenv("JIRA_PARITY_UPDATE_REPLACEMENT_SHA256"), Version: "v1.2.3"}
	candidate := parityArchiveFingerprint(t, replacement)
	for _, artifact := range append(append([]parityArchiveArtifact{}, artifacts...), replacement) {
		parityArchiveFingerprint(t, artifact)
		t.Cleanup(func() { parityArchiveFingerprint(t, artifact) })
	}
	evidence := os.Getenv("JIRA_PARITY_UPDATE_ARCHIVE_EVIDENCE_DIR")
	if err := parityArchiveOpenEvidence(evidence); err != nil {
		t.Fatal("archive evidence directory must be unused; retain earlier attempts: ", err)
	}
	var receipts []parityArchiveReceipt
	defer func() {
		if err := parityArchiveWriteReceipts(evidence, receipts); err != nil {
			t.Error("retain supplementary archive receipts: ", err)
		}
	}()
	for _, mode := range []string{"json", "compact"} {
		for _, scenario := range []string{"preview", "confirmation", "apply", "renamed", "integrity", "no_newer"} {
			for _, artifact := range artifacts {
				if !t.Run(mode+"/"+scenario+"/"+artifact.Lane, func(t *testing.T) {
					home, err := os.MkdirTemp(evidence, artifact.Lane+"-"+scenario+"-")
					if err != nil {
						t.Fatal(err)
					}
					installation := filepath.Join(home, "installation")
					tools := filepath.Join(home, "empty-tools")
					temporary := filepath.Join(home, "temporary")
					for _, directory := range []string{installation, tools, temporary} {
						if err := os.Mkdir(directory, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					basename := "jira"
					if scenario == "renamed" {
						basename = "jira-owned-installed"
					}
					installed := filepath.Join(installation, basename)
					original := parityArchiveFingerprint(t, artifact)
					if err := os.WriteFile(installed, original, 0o700); err != nil {
						t.Fatal(err)
					}
					fixture := nativeReleaseServer(t, candidate)
					fixture.mu.Lock()
					fixture.listMetadata = append(append([]byte{'['}, fixture.metadata...), ']')
					switch scenario {
					case "integrity":
						fixture.checksum = bytes.Replace(fixture.checksum, fixture.checksum[:64], bytes.Repeat([]byte("0"), 64), 1)
					case "no_newer":
						fixture.metadata = bytes.ReplaceAll(fixture.metadata, []byte("1.2.3"), []byte("0.1.0"))
						fixture.listMetadata = append(append([]byte{'['}, fixture.metadata...), ']')
					}
					archiveDigest := fmt.Sprintf("%x", sha256.Sum256(fixture.archive))
					fixture.mu.Unlock()
					environment := append([]string{}, fixture.env...)
					environment = append(environment, "PATH="+tools, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home, "GH_CONFIG_DIR="+filepath.Join(home, "gh"), "TMPDIR="+temporary, "TMP="+temporary, "TEMP="+temporary)
					if nativeVersion(t, installed, home, environment) != artifact.Version || nativeVersion(t, replacement.Binary, home, environment) != replacement.Version {
						t.Fatal("original or candidate version command did not execute correctly")
					}
					args := []string{"up", "--no-input", "--output=" + mode}
					if scenario == "preview" {
						args = append(args, "--dry-run")
					} else if scenario != "confirmation" {
						args = append(args, "--force")
					}
					stdout, stderr, exit := runExecutableIOInDirStarted(t, installed, home, environment, nil, nil, nil, args...)
					receipts = append(receipts, parityArchiveReceipt{Lane: artifact.Lane, Scenario: scenario, Output: mode, BinarySHA256: artifact.Digest, Argv: args, Exit: exit, Stdout: stdout, Stderr: stderr})
					fixture.mu.Lock()
					requests := make(map[string]int, len(fixture.targets))
					for target, count := range fixture.targets {
						requests[target] = count
					}
					fixture.mu.Unlock()
					receipts[len(receipts)-1].Requests = requests
					actual, err := os.ReadFile(installed)
					if err != nil {
						t.Fatal("inspect disposable installed executable: ", err)
					}
					receipts[len(receipts)-1].InstalledSHA256 = fmt.Sprintf("%x", sha256.Sum256(actual))
					failed := scenario == "confirmation" || scenario == "integrity" || scenario == "renamed" && artifact.Lane == "before"
					wantExit := 0
					if failed {
						wantExit = 3
						if artifact.Lane == "after" && scenario == "integrity" {
							wantExit = 5
						}
					}
					if exit != wantExit || artifact.Lane == "after" && exit == 0 && stderr != "" {
						t.Fatalf("archive %s/%s exit%d want%d stdout%s stderr%s", artifact.Lane, scenario, exit, wantExit, stdout, stderr)
					}
					data := []byte(stdout)
					if mode == "json" || failed {
						original, errors, valid := parityArchiveFrame([]byte(stdout), artifact.Lane, exit)
						if !valid || !parityArchiveErrors(errors, artifact.Lane, scenario, archiveDigest) {
							t.Fatalf("archive envelope lost complete outcome/error: %s", stdout)
						}
						data = original
					}
					if !parityArchiveOutput(data, artifact.Lane, artifact.Version, scenario, replacement.Digest, len(candidate)) {
						t.Fatalf("archive lost complete independent publication facts: %s", data)
					}
					published := !failed && (scenario == "apply" || scenario == "renamed")
					wantBytes, wantVersion := original, artifact.Version
					if published {
						wantBytes, wantVersion = candidate, replacement.Version
					}
					if !bytes.Equal(actual, wantBytes) || nativeVersion(t, installed, home, environment) != wantVersion {
						t.Fatal("native installed bytes/version contradict acknowledged or refused update")
					}
					if !parityArchiveRequests(artifact.Lane, scenario, requests) {
						t.Fatalf("archive wire requests differ or were replayed: %v", requests)
					}
					members, err := os.ReadDir(installation)
					if err != nil || len(members) != 1 || members[0].Name() != basename || !members[0].Type().IsRegular() {
						t.Fatal("archive installation left unexpected members; retained for inspection")
					}
					stages, err := os.ReadDir(temporary)
					if err != nil || len(stages) != 0 {
						t.Fatal("archive update left temporary download stages; retained for inspection")
					}
				}) {
					return
				}
			}
		}
	}
}

func TestParityArchiveUpdatePublicationRequiresNativeFacts(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	valid := `{"channel":"github-archive","current":"v0.16.0-dev","latest":"v1.2.3","update_available":true,"managed":false,"updated":true,"dry_run":false,"attempted":true,"uncertain":false,"publication":{"published":true,"durable":true,"uncertain":false,"bytes":100,"sha256":"` + digest + `"}}`
	if !parityArchiveOutput([]byte(valid), "after", "v0.16.0-dev", "renamed", digest, 100) {
		t.Fatal("complete independent archive publication refused")
	}
	for name, invalid := range map[string]string{"wrong_current": strings.ReplaceAll(valid, "v0.16.0-dev", "v0.15.2"), "wrong_latest": strings.ReplaceAll(valid, "v1.2.3", "v9.9.9"), "missing_durability": strings.ReplaceAll(valid, `"durable":true,`, ""), "false_ack": strings.ReplaceAll(valid, `"published":true`, `"published":false`), "wrong_bytes": strings.ReplaceAll(valid, `"bytes":100`, `"bytes":101`), "wrong_digest": strings.ReplaceAll(valid, digest, strings.Repeat("b", 64)), "invented_backup": strings.ReplaceAll(valid, `"published":true`, `"published":true,"backup":"unseen"`), "missing_attempt": strings.ReplaceAll(valid, `"attempted":true,`, ""), "duplicate": strings.ReplaceAll(valid, `"updated":true`, `"updated":true,"updated":true`)} {
		t.Run(name, func(t *testing.T) {
			if parityArchiveOutput([]byte(invalid), "after", "v0.16.0-dev", "renamed", digest, 100) {
				t.Fatal("archive admitted incomplete or invented native publication")
			}
		})
	}
}

func TestParityArchiveUpdateFrameRequiresCompleteContract(t *testing.T) {
	t.Parallel()
	valid := `{"ok":false,"meta":{"command":"update","contract_version":"1","timestamp":"2026-01-01T00:00:00Z","request_id":"0123456789abcdef0123456789abcdef","exit_code":5},"data":{},"errors":[{"code":"update_integrity"}],"warnings":[]}`
	if _, _, ok := parityArchiveFrame([]byte(valid), "after", 5); !ok {
		t.Fatal("complete independent failure envelope refused")
	}
	for name, invalid := range map[string]string{"missing_ok": strings.ReplaceAll(valid, `"ok":false,`, ""), "null_ok": strings.ReplaceAll(valid, `"ok":false`, `"ok":null`), "missing_warnings": strings.ReplaceAll(valid, `,"warnings":[]`, ""), "null_warnings": strings.ReplaceAll(valid, `"warnings":[]`, `"warnings":null`), "missing_request_id": strings.ReplaceAll(valid, `"request_id":"0123456789abcdef0123456789abcdef",`, ""), "wrong_request_id": strings.ReplaceAll(valid, "0123456789abcdef0123456789abcdef", "unknown"), "missing_timestamp": strings.ReplaceAll(valid, `"timestamp":"2026-01-01T00:00:00Z",`, ""), "wrong_timestamp": strings.ReplaceAll(valid, "2026-01-01T00:00:00Z", "yesterday"), "wrong_contract": strings.ReplaceAll(valid, `"contract_version":"1"`, `"contract_version":"2"`), "wrong_exit": strings.ReplaceAll(valid, `"exit_code":5`, `"exit_code":3`), "null_exit": strings.ReplaceAll(valid, `"exit_code":5`, `"exit_code":null`), "empty_errors": strings.ReplaceAll(valid, `[{"code":"update_integrity"}]`, `[]`), "missing_data": strings.ReplaceAll(valid, `"data":{},`, ""), "extra_member": strings.ReplaceAll(valid, `"ok":false`, `"ok":false,"extra":true`)} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := parityArchiveFrame([]byte(invalid), "after", 5); ok {
				t.Fatal("archive admitted incomplete or inconsistent failure framing")
			}
		})
	}
	success := `{"ok":true,"meta":{"command":"update","contract_version":"1","timestamp":"2026-01-01T00:00:00Z","request_id":"0123456789abcdef0123456789abcdef"},"data":{},"errors":[],"warnings":[]}`
	if _, _, ok := parityArchiveFrame([]byte(success), "after", 0); !ok {
		t.Fatal("complete independent success envelope refused")
	}
	if _, _, ok := parityArchiveFrame([]byte(strings.ReplaceAll(success, `"command":"update"`, `"command":"update","exit_code":0`)), "after", 0); ok {
		t.Fatal("archive success retained an exit code")
	}
	legacy := strings.ReplaceAll(valid, `"contract_version":"1",`, "")
	if _, _, ok := parityArchiveFrame([]byte(legacy), "before", 5); !ok {
		t.Fatal("complete independent legacy envelope refused")
	}
}

func TestParityArchiveUpdateRefusalRequiresExactReason(t *testing.T) {
	t.Parallel()
	valid := `[{"code":"update_integrity","message":"jira-cli update failed","hint":"Inspect retained data and verify mutation outcomes before retrying. Do not install the rejected download. Inspect the published release assets and checksums before retrying."}]`
	if !parityArchiveErrors([]byte(valid), "after", "integrity", "") {
		t.Fatal("complete independent archive integrity refusal refused")
	}
	for name, invalid := range map[string]string{"wrong_code": strings.ReplaceAll(valid, "update_integrity", "update_failed"), "wrong_message": strings.ReplaceAll(valid, "jira-cli update failed", "could not authenticate"), "wrong_hint": strings.ReplaceAll(valid, "Do not install", "Install"), "missing": `[]`, "null": `null`, "duplicate": strings.ReplaceAll(valid, `"code":"update_integrity"`, `"code":"update_integrity","code":"update_integrity"`)} {
		t.Run(name, func(t *testing.T) {
			if parityArchiveErrors([]byte(invalid), "after", "integrity", "") {
				t.Fatal("archive admitted unrelated or incomplete refusal")
			}
		})
	}
}

func TestParityArchiveUpdateRequestsBindDiscoveryAndAssets(t *testing.T) {
	t.Parallel()
	valid := map[string]int{"/repos/matcra587/jira-cli/releases/latest": 1, "/repos/matcra587/jira-cli/releases/assets/10": 1, "/repos/matcra587/jira-cli/releases/assets/11": 1}
	if !parityArchiveRequests("after", "apply", valid) {
		t.Fatal("complete independent current request walk refused")
	}
	for name, invalid := range map[string]map[string]int{
		"repeated_discovery": {"/repos/matcra587/jira-cli/releases/latest": 2, "/repos/matcra587/jira-cli/releases/assets/10": 1, "/repos/matcra587/jira-cli/releases/assets/11": 1},
		"missing_checksum":   {"/repos/matcra587/jira-cli/releases/latest": 1, "/repos/matcra587/jira-cli/releases/assets/10": 1},
		"wrong_archive":      {"/repos/matcra587/jira-cli/releases/latest": 1, "/repos/matcra587/jira-cli/releases/assets/99": 1, "/repos/matcra587/jira-cli/releases/assets/11": 1},
		"unexpected_query":   {"/repos/matcra587/jira-cli/releases/latest?extra=1": 1, "/repos/matcra587/jira-cli/releases/assets/10": 1, "/repos/matcra587/jira-cli/releases/assets/11": 1},
	} {
		t.Run(name, func(t *testing.T) {
			if parityArchiveRequests("after", "apply", invalid) {
				t.Fatal("archive admitted repeated, missing or unrelated requests")
			}
		})
	}
	if parityArchiveRequests("after", "preview", valid) || !parityArchiveRequests("before", "apply", map[string]int{"/repos/matcra587/jira-cli/releases": 2, "/repos/matcra587/jira-cli/releases/assets/10": 1, "/repos/matcra587/jira-cli/releases/assets/11": 1}) {
		t.Fatal("archive confused preview downloads or legacy discovery behavior")
	}
}

func TestParityArchiveUpdateEvidenceRequiresUnusedDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "unused")
	if err := parityArchiveOpenEvidence(path); err != nil {
		t.Fatal("unused evidence target refused: ", err)
	}
	if err := parityArchiveOpenEvidence(path); err == nil {
		t.Fatal("archive acceptance admitted an earlier evidence directory")
	}
	if err := parityArchiveOpenEvidence("relative-evidence"); err == nil {
		t.Fatal("archive acceptance admitted a relative evidence directory")
	}
}

func TestParityArchiveUpdateEvidenceReceiptsDoNotClobber(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	before := []byte("independent earlier receipt\n")
	if err := os.WriteFile(filepath.Join(path, "receipts.json"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := parityArchiveWriteReceipts(path, []parityArchiveReceipt{}); err == nil {
		t.Fatal("archive acceptance replaced an earlier receipt")
	}
	after, err := os.ReadFile(filepath.Join(path, "receipts.json"))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("archive acceptance changed an earlier receipt preimage")
	}
	fresh := t.TempDir()
	if err := parityArchiveWriteReceipts(fresh, []parityArchiveReceipt{}); err != nil {
		t.Fatal("fresh receipt refused: ", err)
	}
	raw, err := os.ReadFile(filepath.Join(fresh, "receipts.json"))
	if err != nil || !bytes.Equal(raw, []byte("[]\n")) {
		t.Fatal("fresh empty receipt was not published")
	}
}
