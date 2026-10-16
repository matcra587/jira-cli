//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
)

type parityLane struct {
	Name    string `json:"name"`
	Binary  string `json:"binary"`
	Digest  string `json:"sha256"`
	Config  string `json:"config,omitempty"`
	Profile string `json:"profile,omitempty"`
}

type parityInvocation struct {
	Directory string   `json:"working_directory,omitempty"`
	Lane      string   `json:"lane"`
	Digest    string   `json:"sha256"`
	Argv      []string `json:"argv"`
	Sequence  int      `json:"receipt_sequence"`
	Exit      int      `json:"exit"`
}

type parityCreation struct {
	Lane     string    `json:"lane"`
	Summary  string    `json:"summary"`
	Intent   bool      `json:"intent"`
	Identity liveIssue `json:"identity"`
	Bound    bool      `json:"bound"`
}

type parityEvidence struct {
	Lanes       []parityLane               `json:"lanes"`
	Invocations []parityInvocation         `json:"invocations"`
	Creations   []parityCreation           `json:"creations"`
	Checks      map[string][]string        `json:"checks"`
	ProjectID   string                     `json:"project_id"`
	IssueTypeID string                     `json:"issue_type_id"`
	Preimages   map[string]json.RawMessage `json:"preimages,omitempty"`
	Comments    []parityCommentAttempt     `json:"comment_attempts,omitempty"`
	Worklogs    []parityWorklogAttempt     `json:"worklog_attempts,omitempty"`
	Weblinks    []parityWeblinkAttempt     `json:"weblink_attempts,omitempty"`
	Attachments []parityAttachmentAttempt  `json:"attachment_attempts,omitempty"`
	Links       []parityLinkAttempt        `json:"link_attempts,omitempty"`
	Ranks       []parityRankAttempt        `json:"rank_attempts,omitempty"`
	Epics       []parityEpicAttempt        `json:"epic_attempts,omitempty"`
	FieldMoves  []parityFieldMoveAttempt   `json:"field_move_attempts,omitempty"`
	EditFlags   []parityEditFlagsAttempt   `json:"edit_flag_attempts,omitempty"`
}

type parityRun struct {
	owner    *liveRun
	evidence parityEvidence
}

func parityFingerprint(path, expected string) error {
	if len(expected) != sha256.Size*2 {
		return fmt.Errorf("selected parity binary requires an explicit SHA-256")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read selected parity binary: %w", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
		return fmt.Errorf("selected parity binary fingerprint changed")
	}
	return nil
}

func (p *parityRun) save(t *testing.T) {
	t.Helper()
	data, err := json.MarshalIndent(p.evidence, "", "  ")
	if err == nil {
		err = xos.AtomicWrite(filepath.Join(p.owner.dir, "parity.json"), data, 0o600)
	}
	if err != nil {
		t.Fatal("save differential evidence: ", err)
	}
}

func newParityRun(t *testing.T) *parityRun {
	t.Helper()
	if os.Getenv("JIRA_PARITY_ACCEPTANCE") != "1" {
		t.Skip("paired live execution requires explicit JIRA_PARITY_ACCEPTANCE=1")
	}
	before := parityLane{Name: "before", Binary: requiredEnv(t, "JIRA_PARITY_BEFORE_BINARY"), Digest: requiredEnv(t, "JIRA_PARITY_BEFORE_SHA256")}
	after := parityLane{Name: "after", Binary: requiredEnv(t, "JIRA_LIVE_BINARY"), Digest: requiredEnv(t, "JIRA_PARITY_AFTER_SHA256")}
	for _, lane := range []parityLane{before, after} {
		if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
			t.Fatal(err)
		}
	}
	r := newLiveRun(t)
	r.cacheDir = filepath.Join(r.dir, "cache-after")
	if r.manifest.BinarySHA256 != after.Digest {
		t.Fatal("current cleanup binary differs from the admitted parity binary")
	}
	// The profile name is public namespace metadata. Resolve only this selected
	// legacy profile; never inspect all of the user's stored profiles.
	profile := requiredEnv(t, "JIRA_PARITY_LEGACY_PROFILE")
	before.Profile = profile
	before.Config = filepath.Join(r.dir, "legacy-public.toml")
	public := fmt.Sprintf("default_profile = %q\n[[profiles]]\nname = %q\nbase_url = %q\nemail = %q\nsecret_backend = \"keyring\"\n", profile, profile, r.manifest.BaseURL, r.account)
	if err := os.WriteFile(before.Config, []byte(public), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &parityRun{owner: r, evidence: parityEvidence{Lanes: []parityLane{before, after}, Checks: make(map[string][]string)}}
	p.save(t)
	frame, stdout, exit := p.invoke(t, before, nil, "auth", "status", "--project", r.manifest.Project)
	var status struct {
		Data struct {
			ActiveProfile string `json:"active_profile"`
			Profiles      []struct {
				Profile string `json:"profile"`
				Source  string `json:"source"`
				Valid   bool   `json:"valid"`
				Remote  struct {
					Site string `json:"site"`
				} `json:"remote"`
			} `json:"profiles"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &status) != nil || exit != 0 || !frame.OK || status.Data.ActiveProfile != profile || len(status.Data.Profiles) != 1 || status.Data.Profiles[0].Profile != profile || !status.Data.Profiles[0].Valid || status.Data.Profiles[0].Source != "keyring" || strings.TrimRight(status.Data.Profiles[0].Remote.Site, "/") != strings.TrimRight(r.manifest.BaseURL, "/") {
		t.Fatal("legacy admission did not establish the selected profile, keyring and expected site")
	}
	var baselineID string
	for _, lane := range p.evidence.Lanes {
		frame, stdout, exit := p.invoke(t, lane, nil, "auth", "whoami")
		var auth struct {
			Data struct {
				AccountID string `json:"account_id"`
				User      struct {
					AccountID string `json:"accountId"`
				} `json:"user"`
			} `json:"data"`
		}
		if json.Unmarshal(stdout, &auth) != nil || exit != 0 || !frame.OK {
			t.Fatal("paired identity admission failed; inspect private receipts")
		}
		id := auth.Data.AccountID
		if lane.Name == "after" {
			id = auth.Data.User.AccountID
		}
		if id == "" || (baselineID != "" && id != baselineID) {
			t.Fatal("paired binaries did not establish the same native account identity")
		}
		baselineID = id
	}
	r.watcherAccountID = baselineID
	return p
}

func (p *parityRun) invoke(t *testing.T, lane parityLane, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	return p.invokeAt(t, lane, "", extra, args...)
}

func (p *parityRun) invokeAt(t *testing.T, lane parityLane, directory string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if !slices.Contains(p.evidence.Lanes, lane) {
		t.Fatal("invocation selected an unadmitted lane")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		t.Fatal(err)
	}
	directory, err := liveInvocationDirectory(p.owner.dir, directory)
	if err != nil {
		t.Fatal(err)
	}
	argv := slices.Clone(args)
	if lane.Config != "" {
		argv = append([]string{"--config", lane.Config, "--profile", lane.Profile}, argv...)
	}
	argv = append([]string{"--output=json", "--no-input", "--timeout=30s"}, argv...)
	var stdout []byte
	var exit int
	if lane.Name == "before" {
		environment := append(parityLegacyEnvironment(os.Environ()), "JIRA_READ_ONLY=false", "JIRA_NO_UPDATE_CHECK=1", "NO_COLOR=1", "TERM=dumb", "XDG_CACHE_HOME="+filepath.Join(p.owner.dir, "cache-before"))
		stdout, exit = p.owner.invokeEnvironmentRawAt(t, t.Context(), lane.Binary, directory, append(environment, extra...), argv...)
	} else {
		stdout, exit = p.owner.invokeSelectedRawAt(t, t.Context(), lane.Binary, directory, extra, argv...)
	}
	p.evidence.Invocations = append(p.evidence.Invocations, parityInvocation{Directory: directory, Lane: lane.Name, Digest: lane.Digest, Argv: argv, Sequence: p.owner.sequence, Exit: exit})
	p.save(t)
	var frame liveFrame
	if json.Unmarshal(stdout, &frame) != nil {
		t.Fatal("paired invocation did not return one complete JSON frame")
	}
	if err := parityValidateFrame(stdout, lane.Name, parityExpectedOperation(lane.Name, args, exit), exit); err != nil {
		// Retain the native identity even when the surrounding envelope fails.
		// A create caller must bind and register it before stopping.
		t.Errorf("invalid paired envelope: %v", err)
		frame.OK = false
	}
	return frame, stdout, exit
}

func parityExpectedOperation(lane string, args []string, exit int) string {
	if lane == "before" && exit == 0 && len(args) >= 2 && args[0] == "issue" && args[1] == "mine" {
		return "issue.list"
	}
	if lane == "before" && len(args) >= 3 && args[0] == "issue" && (args[1] == "weblink" || args[1] == "link") && liveKey.MatchString(args[2]) {
		return "issue." + args[1]
	}
	if lane == "before" && exit == 0 && len(args) == 3 && args[0] == "issue" && args[1] == "transition" && liveKey.MatchString(args[2]) {
		return "issue.transitions"
	}
	if lane == "before" && exit != 0 && len(args) >= 3 && args[0] == "issue" && (args[1] == "watch" || args[1] == "unwatch") {
		return strings.Join(args[:2], ".")
	}
	if lane == "before" && exit != 0 && len(args) >= 3 && args[0] == "issue" && args[1] == "comment" && liveKey.MatchString(args[2]) {
		return "issue.comment"
	}
	return parityOperation(args)
}

func parityOperation(args []string) string {
	if len(args) > 0 && (args[0] == "me" || args[0] == "open") {
		return args[0]
	}
	if len(args) < 2 {
		return ""
	}
	if len(args) >= 3 && args[0] == "adf" && args[1] == "edit" {
		switch args[2] {
		case "prepare", "apply", "merge":
			return strings.Join(args[:3], ".")
		}
	}
	if len(args) >= 3 && args[0] == "issue" {
		switch args[1] {
		case "watch":
			return "issue.watchers.add"
		case "unwatch":
			return "issue.watchers.remove"
		case "comment":
			if liveKey.MatchString(args[2]) {
				return "issue.comment.add"
			}
			return strings.Join(args[:3], ".")
		case "attachment", "link", "watchers", "weblink", "input":
			return strings.Join(args[:3], ".")
		case "transition":
			if args[2] == "list" {
				return "issue.transition.list"
			}
		case "edit":
			if slices.Contains([]string{"prepare", "draft", "inspect", "resume"}, args[2]) {
				return strings.Join(args[:3], ".")
			}
		case "move":
			if args[2] == "fields" || args[2] == "status" {
				return strings.Join(args[:3], ".")
			}
		}
	}
	return strings.Join(args[:2], ".")
}

func parityValidateFrame(stdout []byte, lane, operation string, exit int) error {
	return parityValidateFrameShape(stdout, lane, operation, exit, false)
}

func parityValidateLegacyCatalogFrame(stdout []byte, operation string) error {
	return parityValidateFrameShape(stdout, "before", operation, 0, true)
}

func parityValidateFrameShape(stdout []byte, lane, operation string, exit int, legacyArray bool) error {
	if legacyArray && (lane != "before" || (operation != "agent.fieldtypes" && operation != "agent.adf-matrix")) {
		return fmt.Errorf("legacy array admission requires an exact built-in catalog identity")
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(stdout, &members) != nil {
		return fmt.Errorf("invalid original envelope")
	}
	for _, name := range []string{"ok", "meta", "data", "errors", "warnings"} {
		if _, present := members[name]; !present {
			return fmt.Errorf("missing envelope member %s", name)
		}
	}
	for _, name := range []string{"errors", "warnings"} {
		var array []json.RawMessage
		if json.Unmarshal(members[name], &array) != nil || array == nil {
			return fmt.Errorf("envelope member %s must be an array", name)
		}
	}
	var frame struct {
		OK   *bool `json:"ok"`
		Meta struct {
			Command   string `json:"command"`
			Contract  string `json:"contract_version"`
			Timestamp string `json:"timestamp"`
			RequestID string `json:"request_id"`
		} `json:"meta"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(stdout, &frame) != nil || frame.OK == nil || frame.Meta.Command != operation || frame.Meta.RequestID == "" {
		return fmt.Errorf("missing or mismatched operation envelope")
	}
	if _, err := time.Parse(time.RFC3339, frame.Meta.Timestamp); err != nil {
		return fmt.Errorf("invalid operation timestamp")
	}
	if (lane == "after" && frame.Meta.Contract != "1") || (lane == "before" && frame.Meta.Contract != "") {
		return fmt.Errorf("unexpected lane contract version")
	}
	if *frame.OK != (exit == 0) || (*frame.OK && len(frame.Errors) != 0) || (!*frame.OK && len(frame.Errors) == 0) {
		return fmt.Errorf("inconsistent success, exit and errors")
	}
	if *frame.OK {
		if legacyArray {
			var data []json.RawMessage
			if json.Unmarshal(members["data"], &data) != nil || data == nil {
				return fmt.Errorf("legacy catalog lacks array data")
			}
		} else {
			var data map[string]json.RawMessage
			if json.Unmarshal(members["data"], &data) != nil || data == nil {
				return fmt.Errorf("successful operation lacks object data")
			}
		}
	}
	for _, failure := range frame.Errors {
		if failure.Code == "" {
			return fmt.Errorf("failure lacks a stable code")
		}
	}
	return nil
}

// Keep the operating-system transport available to the admitted keyring, but
// remove all profile, token, test-store and configuration environment overlays.
func parityLegacyEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "JIRA_") && !strings.EqualFold(key, "JIRA_KEYRING_SERVICE") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

const parityDescription = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Paired "},{"type":"text","text":"bold","marks":[{"type":"strong"}]},{"type":"text","text":" <&> Unicode café"}]}]}`

func (p *parityRun) success(t *testing.T, lane parityLane, args ...string) liveFrame {
	t.Helper()
	frame, _, exit := p.invoke(t, lane, nil, args...)
	if exit != 0 || !frame.OK {
		t.Fatalf("%s step %d failed: exit %d; inspect private receipts", lane.Name, p.owner.sequence, exit)
	}
	return frame
}

func (p *parityRun) checkedMutation(t *testing.T, lane parityLane, key string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if err := p.validateMutation(lane, key, args); err != nil {
		t.Fatal(err)
	}
	frame, stdout, exit := p.invoke(t, lane, nil, args...)
	if frame.OK && !parityWriteTarget(frame, lane.Name, key) {
		t.Errorf("paired write lost its lane-specific target receipt")
		frame.OK = false
	}
	return frame, stdout, exit
}

func (p *parityRun) validateMutation(lane parityLane, key string, args []string) error {
	// This first slice admits only identical edit/delete grammar. Historical
	// aliases need separately tested, parent-bound translation before admission.
	if len(args) < 3 || args[0] != "issue" || (args[1] != "edit" && args[1] != "delete") || args[2] != key {
		return fmt.Errorf("unsupported paired mutation recipe")
	}
	if !slices.Contains(p.evidence.Lanes, lane) {
		return fmt.Errorf("unadmitted mutation lane")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		return err
	}
	return p.owner.checkMutation([]string{key}, args)
}

func parityWriteTarget(frame liveFrame, lane, key string) bool {
	if lane == "before" {
		return frame.Data.Issue.Key == key
	}
	return len(frame.Data.Results) == 1 && frame.Data.Results[0].OK && frame.Data.Results[0].Issue.Key == key
}

func (p *parityRun) native(t *testing.T, key string) liveIssue {
	t.Helper()
	if !liveKey.MatchString(key) || !strings.HasPrefix(key, p.owner.manifest.Project+"-") {
		t.Fatal("native observation requires a returned dedicated-project key")
	}
	reply := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,description,labels,project,issuetype")
	var issue liveIssue
	if json.Unmarshal(reply.Response.JSON, &issue) != nil || issue.Key != key || issue.ID == "" {
		t.Fatal("native observation did not establish the exact issue identity")
	}
	return issue
}

func (p *parityRun) create(t *testing.T, lane parityLane) string {
	t.Helper()
	r := p.owner
	summary := fmt.Sprintf("%s paired %s %03d", r.manifest.RunID, lane.Name, len(p.evidence.Creations)+1)
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	index := len(p.evidence.Creations)
	p.evidence.Creations = append(p.evidence.Creations, parityCreation{Lane: lane.Name, Summary: summary, Intent: true})
	p.save(t)
	var request struct {
		Fields struct {
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
			IssueType struct {
				Name string `json:"name"`
			} `json:"issuetype"`
			Summary     string          `json:"summary"`
			Labels      []string        `json:"labels"`
			Description json.RawMessage `json:"description"`
		} `json:"fields"`
	}
	request.Fields.Project.Key = r.manifest.Project
	request.Fields.IssueType.Name = r.issueType
	request.Fields.Summary = summary
	request.Fields.Labels = []string{r.manifest.RunID}
	request.Fields.Description = json.RawMessage(parityDescription)
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(r.dir, fmt.Sprintf("create-%s-%03d.json", lane.Name, index+1))
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	frame, _, exit := p.invoke(t, lane, nil, "issue", "create", "--json-input", input, "--verify")
	issue := p.bindCreated(t, lane, index, frame, exit)
	var fields struct {
		Description json.RawMessage `json:"description"`
	}
	if json.Unmarshal(issue.Fields, &fields) != nil || !nativeMetadataMatches(fields.Description, json.RawMessage(parityDescription), nil) {
		t.Fatal("created native description differs from independently authored rich ADF")
	}
	return issue.Key
}

// Bind ownership before checking the remaining field or presentation assertions.
// A failed acknowledgement must still retain independently identified cleanup.
func (p *parityRun) bindCreated(t *testing.T, lane parityLane, index int, frame liveFrame, exit int) liveIssue {
	t.Helper()
	r := p.owner
	summary := p.evidence.Creations[index].Summary
	// Preserve a returned identity before assertions, including nonzero exits.
	p.evidence.Creations[index].Identity = frame.Data.Issue
	p.save(t)
	if frame.Data.Issue.Key == "" {
		t.Fatal("creation outcome unresolved: no returned identity; never replay")
	}
	issue := p.native(t, frame.Data.Issue.Key)
	var fields struct {
		Summary     string                    `json:"summary"`
		Description json.RawMessage           `json:"description"`
		Labels      []string                  `json:"labels"`
		Project     struct{ ID, Key string }  `json:"project"`
		IssueType   struct{ ID, Name string } `json:"issuetype"`
	}
	if json.Unmarshal(issue.Fields, &fields) != nil || fields.Summary != summary || fields.Project.ID == "" || fields.Project.Key != r.manifest.Project || fields.IssueType.ID == "" || fields.IssueType.Name != r.issueType || !slices.Contains(fields.Labels, r.manifest.RunID) || (frame.Data.Issue.ID != "" && frame.Data.Issue.ID != issue.ID) {
		t.Fatal("creation identity cannot be bound to exact run/lane markers; retain unresolved receipt")
	}
	r.register(t, issue)
	p.evidence.Creations[index].Identity = issue
	p.evidence.Creations[index].Bound = true
	p.save(t)
	if (p.evidence.ProjectID != "" && p.evidence.ProjectID != fields.Project.ID) || (p.evidence.IssueTypeID != "" && p.evidence.IssueTypeID != fields.IssueType.ID) {
		t.Fatal("paired native project/type preimages differ; owned cleanup retained")
	}
	p.evidence.ProjectID = fields.Project.ID
	p.evidence.IssueTypeID = fields.IssueType.ID
	p.save(t)
	if exit != 0 || !frame.OK {
		t.Fatal("creation reported failure after returning an independently bound issue; cleanup retained")
	}
	if lane.Name == "after" && !parityCurrentCreateAcknowledged(frame, issue) {
		t.Fatal("current creation lost its attempted/applied/native receipt contract; cleanup retained")
	}
	return issue
}

func parityCurrentCreateAcknowledged(frame liveFrame, issue liveIssue) bool {
	var receipt liveIssue
	return frame.Data.Attempted && frame.Data.Applied && json.Unmarshal(frame.Data.Receipt, &receipt) == nil && receipt.Key == issue.Key && receipt.ID == issue.ID
}

func parityViewMatches(issue liveIssue, id, key, summary string) bool {
	var fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
	}
	return issue.ID == id && issue.Key == key && json.Unmarshal(issue.Fields, &fields) == nil && fields.Summary == summary && nativeMetadataMatches(fields.Description, json.RawMessage(parityDescription), nil)
}

func (p *parityRun) check(t *testing.T, lane parityLane, name string) {
	t.Helper()
	p.evidence.Checks[name] = append(p.evidence.Checks[name], lane.Name)
	p.save(t)
	t.Logf("paired check %s: %s", name, lane.Name)
}

func TestLiveParityTracer(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			p.check(t, lane, "I011.create.native")
			before := p.native(t, key)
			for _, preview := range []bool{true, false} {
				args := []string{"issue", "edit", key, "--summary", p.owner.manifest.RunID + " must not land"}
				extra := []string{"JIRA_READ_ONLY=true"}
				name := "I013.edit.read_only"
				if preview {
					args = append(args, "--dry-run")
					extra = nil
					name = "I013.edit.preview"
				}
				if err := p.owner.checkMutation([]string{key}, args); err != nil {
					t.Fatal(err)
				}
				frame, _, exit := p.invoke(t, lane, extra, args...)
				if preview {
					if exit != 0 || !frame.OK || !frame.Data.DryRun {
						t.Fatal("paired edit preview did not identify its local candidate")
					}
				} else {
					code := "read_only_blocked"
					if lane.Name == "before" {
						code = "read_only"
					}
					if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code {
						t.Fatal("paired edit did not preserve its declared read-only refusal")
					}
				}
				after := p.native(t, key)
				if !nativeMetadataMatches(after.Fields, before.Fields, nil) {
					t.Fatal("preview or read-only edit changed independent native fields")
				}
				p.check(t, lane, name)
			}
			summary := p.owner.manifest.RunID + " edited " + lane.Name
			frame, _, exit := p.checkedMutation(t, lane, key, "issue", "edit", key, "--summary", summary, "--verify")
			if exit != 0 || !frame.OK {
				t.Fatal("paired edit failed; never replay")
			}
			native := p.native(t, key)
			var fields struct {
				Summary     string          `json:"summary"`
				Description json.RawMessage `json:"description"`
			}
			if json.Unmarshal(native.Fields, &fields) != nil || fields.Summary != summary || !nativeMetadataMatches(fields.Description, json.RawMessage(parityDescription), nil) || !parityUnchangedFields(native.Fields, before.Fields, "summary") {
				t.Fatal("paired edit changed the body or lost its authored summary")
			}
			p.check(t, lane, "I013.edit.native")
			view := p.success(t, lane, "issue", "view", key, "--fields=summary,description")
			if !parityViewMatches(view.Data.Issue, native.ID, key, summary) {
				t.Fatal("domain view disagrees with independent native fields")
			}
			p.check(t, lane, "I024.view.native")
			deleteArgs := []string{"issue", "delete", key, "--force"}
			if err := p.validateMutation(lane, key, deleteArgs); err != nil {
				t.Fatal(err)
			}
			p.owner.manifest.PendingDeletes = append(p.owner.manifest.PendingDeletes, key)
			p.owner.save(t)
			frame, _, exit = p.checkedMutation(t, lane, key, deleteArgs...)
			missing, _, missingExit := p.owner.invoke(t, t.Context(), nil, "issue", "view", key)
			if !p.owner.confirmParityDeletion(key, missing, missingExit) {
				t.Fatal("paired deletion lacks an independent not-found observation")
			}
			p.owner.save(t)
			if exit != 0 || !frame.OK {
				t.Fatal("paired delete failed, but independent absence is retained without replay")
			}
			p.check(t, lane, "I012.delete.confirmed")
		})
	}
}

func (r *liveRun) confirmParityDeletion(key string, observation liveFrame, exit int) bool {
	if exit != 2 || observation.OK || len(observation.Errors) != 1 || observation.Errors[0].Code != "jira_not_found" {
		return false
	}
	for i := range r.manifest.Issues {
		if r.manifest.Issues[i].Key == key {
			r.manifest.Issues[i].Deleted = true
			r.manifest.PendingDeletes = slices.DeleteFunc(r.manifest.PendingDeletes, func(pending string) bool { return pending == key })
			return true
		}
	}
	return false
}

func TestParityFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected")
	if err := os.WriteFile(path, []byte("independent binary fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("independent binary fixture")))
	if err := parityFingerprint(path, digest); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", strings.Repeat("0", 64)} {
		if parityFingerprint(path, invalid) == nil {
			t.Fatal("missing or mismatched binary digest was admitted")
		}
	}
	if err := os.WriteFile(path, []byte("changed independent binary fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if parityFingerprint(path, digest) == nil {
		t.Fatal("changed selected binary was admitted")
	}
}

func parityUnchangedFields(observed, original json.RawMessage, changed ...string) bool {
	var after, before map[string]json.RawMessage
	if json.Unmarshal(observed, &after) != nil || json.Unmarshal(original, &before) != nil || after == nil || before == nil {
		return false
	}
	for _, name := range changed {
		delete(after, name)
		delete(before, name)
	}
	a, errA := json.Marshal(after)
	b, errB := json.Marshal(before)
	return errA == nil && errB == nil && nativeMetadataMatches(a, b, nil)
}
