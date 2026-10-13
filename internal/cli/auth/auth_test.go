package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"charm.land/huh/v2"
	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// fakeStore is an in-memory credential store. Tests must never reach the
// developer's real keyring, which is the reason the seam exists.
type fakeStore struct {
	secrets map[string]string
	setCall int
	delCall int
	failSet error
}

func newFakeStore() *fakeStore { return &fakeStore{secrets: map[string]string{}} }

func (f *fakeStore) Get(ctx context.Context, account string) (string, error) {
	if s, ok := f.secrets[account]; ok {
		return s, nil
	}
	return "", core.ErrNoCredential
}

func (f *fakeStore) Set(ctx context.Context, account, secret string) error {
	f.setCall++
	if f.failSet != nil {
		return f.failSet
	}
	f.secrets[account] = secret
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, account string) error {
	f.delCall++
	delete(f.secrets, account)
	return nil
}

// fakeClient answers CurrentUser with a canned account, or an error.
type fakeClient struct {
	accountID   string
	displayName string
	err         error
	sawToken    string
}

func (f *fakeClient) BaseURL() string { return "" }

func (f *fakeClient) SearchUsers(context.Context, string, int, int) (core.UserSearchRead, error) {
	return core.UserSearchRead{}, errors.New("unexpected user search during authentication")
}

func (*fakeClient) DoAPI(context.Context, core.APIRequest) (*core.APIResponse, error) {
	return nil, errors.New("DoAPI is not used by auth tests")
}

func (f *fakeClient) GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error) {
	return core.IssueRead{}, errors.New("not used")
}

func (f *fakeClient) GetComments(context.Context, string, int, int, core.CommentOrder) (core.CommentRead, error) {
	return core.CommentRead{}, errors.New("not used")
}

func (*fakeClient) GetWorklogs(context.Context, string, int, int) (core.WorklogRead, error) {
	return core.WorklogRead{}, errors.New("unexpected GetWorklogs call")
}

func (*fakeClient) ParseJQL(context.Context, []string, core.JQLValidationMode) (core.JQLParseRead, error) {
	return core.JQLParseRead{}, errors.New("unexpected ParseJQL call")
}

func (*fakeClient) GetJQLReference(context.Context) (core.JQLReferenceRead, error) {
	return core.JQLReferenceRead{}, errors.New("unexpected GetJQLReference call")
}

func (*fakeClient) SubmitIssueMove(context.Context, jira.IssueBulkMovePayload) (core.IssueMoveSubmission, error) {
	return core.IssueMoveSubmission{}, errors.New("unexpected SubmitIssueMove call")
}

func (*fakeClient) GetBulkOperationProgress(context.Context, string) (core.BulkOperationRead, error) {
	return core.BulkOperationRead{}, errors.New("unexpected GetBulkOperationProgress call")
}

func (*fakeClient) AddWorklog(context.Context, string, jira.AddWorklogRequest, bool) (core.WorklogWrite, error) {
	return core.WorklogWrite{}, errors.New("unexpected AddWorklog call")
}

func (f *fakeClient) SearchIssues(context.Context, string, []string, string, int, ...string) (core.IssueSearchRead, error) {
	return core.IssueSearchRead{}, errors.New("not used")
}

func (f *fakeClient) GetWatchers(context.Context, string) (core.WatcherRead, error) {
	return core.WatcherRead{}, errors.New("not used")
}

// GetTransitions and DoTransition satisfy the transition half of
// core.JiraClient. This fake's command never touches the workflow, so
// they exist to keep the seam satisfied and fail loudly if a test ever
// reaches one by accident.
func (*fakeClient) GetTransitions(context.Context, string) (core.TransitionRead, error) {
	return core.TransitionRead{}, errors.New("not used")
}

func (*fakeClient) DoTransition(context.Context, string, jira.TransitionRequest) (core.TransitionWrite, error) {
	return core.TransitionWrite{}, errors.New("not used")
}

func (f *fakeClient) CountIssues(context.Context, string) (core.IssueCountRead, error) {
	return core.IssueCountRead{}, errors.New("not used")
}

func (*fakeClient) GetFilter(context.Context, string) (core.FilterRead, error) {
	return core.FilterRead{}, errors.New("not used")
}

func (*fakeClient) GetBoards(context.Context, string, int, int) (core.BoardRead, error) {
	return core.BoardRead{}, errors.New("not used")
}

func (*fakeClient) GetBoardProjects(context.Context, int64, int, int) (core.BoardProjectRead, error) {
	panic("unexpected GetBoardProjects")
}

func (*fakeClient) GetBoardConfiguration(context.Context, int64) (core.BoardConfigurationRead, error) {
	return core.BoardConfigurationRead{}, errors.New("not used")
}

// The lifecycle half of core.JiraClient. This fake's command never calls
// them, so they exist to keep the seam satisfied and fail loudly if a
// test ever reaches one by accident.
func (*fakeClient) DeleteIssue(context.Context, string, bool) (core.IssueDeleteWrite, error) {
	return core.IssueDeleteWrite{}, errors.New("not used")
}

func (*fakeClient) RankIssues(context.Context, jira.RankRequest) (core.RankWrite, error) {
	return core.RankWrite{}, errors.New("not used")
}

// The attachment half of core.JiraClient. This fake's command never
// calls them, so they exist to keep the seam satisfied and fail loudly
// if a test ever reaches one by accident.
func (*fakeClient) AddAttachment(context.Context, string, []string) (core.AttachmentWrite, error) {
	return core.AttachmentWrite{}, errors.New("not used")
}

func (*fakeClient) GetAttachment(context.Context, string) (core.AttachmentRead, error) {
	return core.AttachmentRead{}, errors.New("not used")
}

func (*fakeClient) GetAttachmentContent(context.Context, string) (core.AttachmentContent, error) {
	return core.AttachmentContent{}, errors.New("not used")
}

func (*fakeClient) DeleteAttachment(context.Context, string) (core.AttachmentDeleteWrite, error) {
	return core.AttachmentDeleteWrite{}, errors.New("not used")
}

// The issue-link and remote-link half of core.JiraClient. This fake's
// command never calls them, so they exist to keep the seam satisfied and
// fail loudly if a test ever reaches one by accident.
func (*fakeClient) GetIssueLinkTypes(context.Context) (core.LinkTypeRead, error) {
	return core.LinkTypeRead{}, errors.New("not used")
}

func (*fakeClient) LinkIssues(context.Context, jira.LinkIssueRequest) (core.LinkWrite, error) {
	return core.LinkWrite{}, errors.New("not used")
}

func (*fakeClient) DeleteIssueLink(context.Context, string) (core.LinkDeleteWrite, error) {
	return core.LinkDeleteWrite{}, errors.New("not used")
}

func (*fakeClient) GetRemoteLinks(context.Context, string) (core.RemoteLinkRead, error) {
	return core.RemoteLinkRead{}, errors.New("not used")
}

func (*fakeClient) AddRemoteLink(context.Context, string, jira.RemoteLinkRequest) (core.RemoteLinkWrite, error) {
	return core.RemoteLinkWrite{}, errors.New("not used")
}

func (*fakeClient) DeleteRemoteLink(context.Context, string, string) (core.RemoteLinkDeleteWrite, error) {
	return core.RemoteLinkDeleteWrite{}, errors.New("not used")
}

// The watcher and comment WRITE half of core.JiraClient. This fake's
// command never calls them, so they exist to keep the seam satisfied and
// fail loudly if a test ever reaches one by accident.
func (*fakeClient) UpdateComment(context.Context, string, string, jira.UpdateCommentRequest, bool) (core.CommentWrite, error) {
	return core.CommentWrite{}, errors.New("not used")
}

func (*fakeClient) DeleteComment(context.Context, string, string) (core.CommentDeleteWrite, error) {
	return core.CommentDeleteWrite{}, errors.New("not used")
}

func (*fakeClient) AddWatcher(context.Context, string, string) (core.WatcherWrite, error) {
	return core.WatcherWrite{}, errors.New("not used")
}

func (*fakeClient) RemoveWatcher(context.Context, string, string) (core.WatcherWrite, error) {
	return core.WatcherWrite{}, errors.New("not used")
}

// AddComment satisfies the write half of core.JiraClient. This fake's
// command never posts a comment, so the method exists to keep the seam
// satisfied and fails loudly if a test ever reaches it by accident.
func (f *fakeClient) AddComment(context.Context, string, jira.AddCommentRequest) (core.CommentWrite, error) {
	return core.CommentWrite{}, errors.New("not used")
}

func (f *fakeClient) CurrentUser(context.Context) (core.UserRead, error) {
	if f.err != nil {
		return core.UserRead{}, f.err
	}
	return core.UserRead{User: &jira.UserDetails{
		AccountID:   jira.Nullable[string]{Value: f.accountID, Set: true},
		DisplayName: jira.Nullable[string]{Value: f.displayName, Set: true},
	}}, nil
}

// harness wires a Deps whose every seam is inspectable.
type harness struct {
	site   SiteInfo
	store  *fakeStore
	client *fakeClient
	deps   Deps
}

func newHarness(t *testing.T, site SiteInfo) *harness {
	t.Helper()
	h := &harness{
		site:   site,
		store:  newFakeStore(),
		client: &fakeClient{accountID: "5b10a2844c20165700ede21g", displayName: "Test Account"},
	}
	h.deps = Deps{
		Credentials: func() (core.CredentialStore, error) { return h.store, nil },
		ClientFor: func(_, _, token string) (core.JiraClient, error) {
			h.client.sawToken = token
			return h.client, nil
		},
		Site: func(ctx context.Context) (SiteInfo, error) { return h.site, nil },
		// Nothing else supplies a token in these tests, so removing the
		// stored one really does log you out.
		SiteWithoutStore: func(ctx context.Context) (SiteInfo, error) {
			bare := h.site
			bare.Token, bare.TokenSource = "", ""
			return bare, nil
		},
	}
	return h
}

// identOf turns a SiteInfo into the identity login now takes directly.
func identOf(s SiteInfo) identity {
	return identity{site: s.BaseURL, email: s.Email}
}

// doLogout mirrors what runLogout resolves before calling logout, so a
// test exercises the same account resolution the command does.
func doLogout(t *testing.T, h *harness, dryRun bool) (envelope.AuthLogoutOutput, error) {
	t.Helper()
	site, err := h.deps.Site(t.Context())
	if err != nil {
		t.Fatalf("site: %v", err)
	}
	account, err := accountToRemove(t.Context(), h.deps, site.Email)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	return logout(t.Context(), h.deps, site, account, dryRun)
}

func configured() SiteInfo {
	return SiteInfo{
		BaseURL:     "https://example.atlassian.net",
		Email:       "person@example.com",
		TokenSource: string(envelope.TokenSourceKeyring),
	}
}

// TestLoginVerifiesBeforeStoring is the rule the ordering exists for. A
// token stored first and checked afterwards leaves a broken credential
// in the keyring on every typo, and the NEXT command — not this one —
// becomes the one that fails.
func TestLoginVerifiesBeforeStoring(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	h.client.err = errors.New("401 unauthorized")

	if _, err := login(t.Context(), h.deps, identOf(configured()), "a-bad-token", false); err == nil {
		t.Fatal("a token Jira rejected was accepted")
	}
	if h.store.setCall != 0 {
		t.Errorf("the credential store was written %d time(s) despite verification failing", h.store.setCall)
	}
	if _, err := credentials.Load(t.Context(), h.store); err == nil {
		t.Error("a rejected token was stored")
	}
}

// TestLoginStoresTheVerifiedToken is the happy path: the token that was
// checked is the token that lands, filed under the configured account.
func TestLoginStoresTheVerifiedToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())

	out, err := login(t.Context(), h.deps, identOf(configured()), "a-good-token", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// Decoded, not compared as a blob: the record is a contract, and the
	// identity must land with the secret or a later run cannot use it.
	rec, err := credentials.Load(t.Context(), h.store)
	if err != nil {
		t.Fatalf("load stored record: %v", err)
	}
	if rec.Token != "a-good-token" {
		t.Errorf("stored token %q, want the verified one", rec.Token)
	}
	if rec.Site != "https://example.atlassian.net" || rec.Email != "person@example.com" {
		t.Errorf("record identity = %q/%q, want the site and account it was verified against", rec.Site, rec.Email)
	}
	if rec.Version != credentials.Version {
		t.Errorf("record version = %d, want %d — an unversioned blob cannot be migrated", rec.Version, credentials.Version)
	}
	if h.client.sawToken != "a-good-token" {
		t.Errorf("verified %q, want the candidate token — verifying anything else proves nothing", h.client.sawToken)
	}
	// The identity comes from Jira's answer, not from what was typed.
	if out.AccountID != "5b10a2844c20165700ede21g" || out.DisplayName != "Test Account" {
		t.Errorf("output = %+v, want the account Jira answered with", out)
	}
}

// TestLoginRefusesWithoutASite pins that verification cannot be skipped
// for want of somewhere to call. Storing an unverifiable token would
// defeat the check entirely.
func TestLoginRefusesWithoutASite(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, field string
		site        SiteInfo
	}{
		{name: "no site", field: "site", site: SiteInfo{Email: "person@example.com"}},
		{name: "no email", field: "email", site: SiteInfo{BaseURL: "https://example.atlassian.net"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.site)

			_, err := login(t.Context(), h.deps, identOf(tt.site), "a-token", false)
			if err == nil {
				t.Fatal("login proceeded with nothing to verify against")
			}
			var input *cli.InputError
			if !errors.As(err, &input) || input.Field != tt.field {
				t.Errorf("error = %v, want an input error naming %q", err, tt.field)
			}
			if h.store.setCall != 0 {
				t.Error("a token was stored although it could not be verified")
			}
		})
	}
}

// TestReadTokenTrimsTrailingNewline is the `op read` case, and the one
// most likely to bite. Every password manager and every file ends with a
// newline; a token carrying one fails authentication in a way that looks
// like a bad credential rather than a bad read.
func TestReadTokenTrimsTrailingNewline(t *testing.T) {
	t.Parallel()
	// No command needed: the token comes off the invocation, so a test
	// supplies a reader rather than building a tree to hang one on.
	got, err := readToken(t.Context(), command.Invocation{Stdin: strings.NewReader("a-good-token\n")})
	if err != nil {
		t.Fatalf("readToken: %v", err)
	}
	if got != "a-good-token" {
		t.Errorf("token = %q, want it trimmed", got)
	}
}

// TestReadTokenRejectsUnusableInput pins the cases where the input is
// not a single token — each would otherwise be stored and fail later,
// against Jira, blamed on the credential.
func TestReadTokenRejectsUnusableInput(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, input string }{
		{name: "empty", input: ""},
		{name: "whitespace only", input: "   \n"},
		{name: "several lines", input: "token-one\ntoken-two\n"},
		{name: "larger than any token", input: strings.Repeat("x", maxTokenBytes+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readToken(t.Context(), command.Invocation{Stdin: strings.NewReader(tt.input)}); err == nil {
				t.Error("input that is not a single token was accepted")
			}
		})
	}
}

// TestStatusReportsWhatJiraAnswered pins that this command asks rather
// than reflects. Echoing the configured account back would report
// success for a token belonging to somebody else entirely.
func TestStatusReportsWhatJiraAnswered(t *testing.T) {
	t.Parallel()
	site := configured()
	site.Token = "a-good-token"
	h := newHarness(t, site)

	out, err := status(t.Context(), h.deps, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if out.AccountID != "5b10a2844c20165700ede21g" {
		t.Errorf("account_id = %q, want the value Jira returned", out.AccountID)
	}
	if h.client.sawToken != "a-good-token" {
		t.Error("status did not test the resolved token")
	}
	// The source is the field that explains a surprising result.
	if out.TokenSource != string(envelope.TokenSourceKeyring) {
		t.Errorf("token_source = %q, want %q", out.TokenSource, envelope.TokenSourceKeyring)
	}
}

// TestStatusFailsWhenNotAuthenticated pins the exit class. Not being
// logged in is an authentication state (exit 1), not a validation
// failure, and it must read that way to a script.
func TestStatusFailsWhenNotAuthenticated(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		site SiteInfo
	}{
		{name: "nothing configured", site: SiteInfo{}},
		{name: "configured but no token", site: configured()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.site)

			_, err := status(t.Context(), h.deps, false)
			if err == nil {
				t.Fatal("status reported success with no usable credential")
			}
			var coded *cli.CodedError
			if !errors.As(err, &coded) || coded.Code != cli.CodeAuthMissing {
				t.Fatalf("error = %v, want code %q", err, cli.CodeAuthMissing)
			}
			if got := cli.MapError(err).Exit; got != cli.ExitAuth {
				t.Errorf("exit = %d, want %d — not authenticated is an auth failure", got, cli.ExitAuth)
			}
		})
	}
}

// TestStatusNeedsNoCredentialStore pins that reading status is a
// resolution question, not a storage one.
//
// The previous version of this test installed a Credentials closure that
// called t.Error if invoked — which could never fire, because status()
// does not reference deps.Credentials at all. It passed no matter what
// the code did. Withholding the dependency entirely is the assertion
// that actually bites: if status ever reaches for the store, this panics
// instead of quietly passing.
func TestStatusNeedsNoCredentialStore(t *testing.T) {
	t.Parallel()
	site := configured()
	site.Token = "a-good-token"
	h := newHarness(t, site)
	h.deps.Credentials = nil

	out, err := status(t.Context(), h.deps, false)
	if err != nil {
		t.Fatalf("status with no credential store wired: %v", err)
	}
	if out.AccountID == "" {
		t.Error("status returned no account")
	}
}

// TestLogoutRemovesTheStoredToken pins the end state.
func TestLogoutRemovesTheStoredToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	if err := credentials.Save(t.Context(), h.store, credentials.Record{
		Site: "https://example.atlassian.net", Email: "person@example.com", Token: "a-token",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := doLogout(t, h, false)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := credentials.Load(t.Context(), h.store); err == nil {
		t.Error("the token is still stored after logout")
	}
	// Removing a local copy is not revocation, and the payload has to
	// say so where a script can see it rather than only in prose.
	if !out.RevocationNeeded {
		t.Error("revocation_needed is false; logout does not revoke anything")
	}
}

// TestLogoutIsIdempotent pins that logging out twice is not an error.
// The command's contract is the end state, not the transition.
func TestLogoutIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())

	for range 2 {
		if _, err := doLogout(t, h, false); err != nil {
			t.Fatalf("logout with nothing stored: %v", err)
		}
	}
}

// TestLogoutSaysWhenTheEnvironmentStillWins is the honesty case: with
// JIRA_API_TOKEN set, commands keep working after logout, and a caller
// who is not told reads that as a failed logout.
func TestLogoutSaysWhenTheEnvironmentStillWins(t *testing.T) {
	t.Parallel()
	site := configured()
	site.TokenSource = string(envelope.TokenSourceEnv)
	h := newHarness(t, site)

	out, err := doLogout(t, h, false)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !out.EnvTokenRemains {
		t.Error("env_token_remains is false although the token came from the environment")
	}
	if out.RemainingSource != string(envelope.TokenSourceEnv) {
		t.Errorf("remaining_source = %q, want %q — the environment token outlives logout",
			out.RemainingSource, envelope.TokenSourceEnv)
	}
}

// TestTokenReturnsTheResolvedSecret pins the one command that emits a
// secret on purpose.
func TestTokenReturnsTheResolvedSecret(t *testing.T) {
	t.Parallel()
	site := configured()
	site.Token = "a-good-token"

	got, err := tokenValue(t.Context(), newHarness(t, site).deps)
	if err != nil {
		t.Fatalf("tokenValue: %v", err)
	}
	if got != "a-good-token" {
		t.Errorf("token = %q, want the resolved token", got)
	}
}

// TestTokenFailsWithNothingStored pins that the absence of a token is an
// auth failure, not an empty success — `token=$(jira auth token)` must
// not silently produce an empty string.
func TestTokenFailsWithNothingStored(t *testing.T) {
	t.Parallel()
	_, err := tokenValue(t.Context(), newHarness(t, configured()).deps)
	if err == nil {
		t.Fatal("an empty token was returned as success")
	}
	if got := cli.MapError(err).Exit; got != cli.ExitAuth {
		t.Errorf("exit = %d, want %d", got, cli.ExitAuth)
	}
}

// TestTokenIsEnvelopeFree pins the declared contract: the output is the
// secret, so a frame would carry it into logs and --jq output.
func TestTokenIsEnvelopeFree(t *testing.T) {
	t.Parallel()
	for _, sub := range New(newHarness(t, configured()).deps).Commands() {
		if sub.Name() != "token" {
			continue
		}
		if _, ok := envelope.Registered("auth.token"); ok {
			t.Error("auth.token registered an envelope shape; its output is a bare secret")
		}
		return
	}
	t.Fatal("auth token is not attached to the auth group")
}

// TestAuthCommandsAreRegistered guards against a command that exists in
// the source and not in the binary.
func TestAuthCommandsAreRegistered(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"login": false, "status": false, "logout": false, "token": false}
	for _, sub := range New(newHarness(t, configured()).deps).Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("auth %s is not attached to the group", name)
		}
	}
}

// TestStatusKeepsTheTokenOutOfTheEnvelopeUnlessAsked pins the rule that
// makes `auth status` safe to run anywhere, including in a script whose
// output is logged. The token rides the struct so the renderer can mask
// it, and `json:"-"` is the only thing keeping it off the wire.
func TestStatusKeepsTheTokenOutOfTheEnvelopeUnlessAsked(t *testing.T) {
	t.Parallel()
	const secret = "ATATT-a-very-real-looking-token"
	site := configured()
	site.Token = secret

	for _, tt := range []struct {
		name       string
		showToken  bool
		wantInJSON bool
	}{
		{name: "default", showToken: false, wantInJSON: false},
		{name: "explicitly asked", showToken: true, wantInJSON: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := status(t.Context(), newHarness(t, site).deps, tt.showToken)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(encoded), secret); got != tt.wantInJSON {
				t.Errorf("token in serialized payload = %v, want %v\n%s", got, tt.wantInJSON, encoded)
			}
		})
	}
}

// TestMaskTokenHidesAllButTheTail pins how much of a secret reaches the
// screen, and that the mask does not betray the token's length.
func TestMaskTokenHidesAllButTheTail(t *testing.T) {
	t.Parallel()
	const secret = "ATATT-abcdefghijklmnop-WXYZ"

	got := maskToken(secret)
	if strings.Contains(got, "abcdefghijklmnop") {
		t.Errorf("mask %q reveals the body of the token", got)
	}
	if !strings.HasSuffix(got, "WXYZ") {
		t.Errorf("mask %q does not end with the tail that identifies which token this is", got)
	}
	if strings.Contains(got, secret) {
		t.Errorf("mask %q contains the whole token", got)
	}
	// A short value is masked completely rather than half-revealed.
	if short := maskToken("abc"); strings.Contains(short, "abc") {
		t.Errorf("a short token was partly revealed: %q", short)
	}
	// Fixed width: two tokens of very different lengths mask the same.
	if a, b := maskToken(secret), maskToken(secret+strings.Repeat("x", 100)+"WXYZ"); len(a) != len(b) {
		t.Errorf("mask length varies with the token length (%d vs %d), leaking it", len(a), len(b))
	}
}

// TestDryRunNeverTouchesTheCredentialStore is the direct guard on the
// "--dry-run changes nothing" contract for the one piece of state the
// audit corpus cannot see.
//
// The corpus proves a preview writes no FILES by fingerprinting its
// sandbox. The OS credential store is not in that sandbox — it is a
// session service — so if the `if !dryRun` guard were dropped or
// inverted, a preview would write to the developer's real keyring and
// every existing test would stay green. This is the test that fails.
func TestDryRunNeverTouchesTheCredentialStore(t *testing.T) {
	t.Parallel()

	t.Run("login stores nothing", func(t *testing.T) {
		h := newHarness(t, configured())

		out, err := login(t.Context(), h.deps, identOf(configured()), "a-good-token", true)
		if err != nil {
			t.Fatalf("dry-run login: %v", err)
		}
		if h.store.setCall != 0 {
			t.Errorf("a preview wrote to the credential store %d time(s)", h.store.setCall)
		}
		if !out.DryRun {
			t.Error("dry_run is false on a preview; a caller cannot tell it from a real login")
		}
		// The verification still happened — that is the half of login a
		// preview is FOR, and a preview that skipped it would report a
		// token valid without ever asking.
		if h.client.sawToken != "a-good-token" {
			t.Error("a preview did not verify the token, so its answer means nothing")
		}
		if out.AccountID == "" {
			t.Error("a preview reported no account, so it proved nothing")
		}
	})

	t.Run("logout removes nothing", func(t *testing.T) {
		h := newHarness(t, configured())
		if err := credentials.Save(t.Context(), h.store, credentials.Record{
			Site: "https://example.atlassian.net", Email: "person@example.com", Token: "a-token",
		}); err != nil {
			t.Fatal(err)
		}

		out, err := doLogout(t, h, true)
		if err != nil {
			t.Fatalf("dry-run logout: %v", err)
		}
		if h.store.delCall != 0 {
			t.Errorf("a preview deleted from the credential store %d time(s)", h.store.delCall)
		}
		if _, err := credentials.Load(t.Context(), h.store); err != nil {
			t.Errorf("a preview removed the stored credential: %v", err)
		}
		if !out.DryRun {
			t.Error("dry_run is false on a preview")
		}
	})
}

// TestLogoutSaysWhenAnotherSourceTakesOver pins the most misleading
// outcome this command has: reporting a token removed while the next
// command still works.
//
// Removing the keyring record does not remove a token in the config
// file or the environment — those simply become effective. Logout said
// so only for the environment, so deleting the stored record with a
// file token behind it reported success and left you authenticated.
func TestLogoutSaysWhenAnotherSourceTakesOver(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	// A token from the file layer waits behind the stored one.
	h.deps.SiteWithoutStore = func(ctx context.Context) (SiteInfo, error) {
		behind := h.site
		behind.Token = "a-file-token"
		behind.TokenSource = string(envelope.TokenSourceFile)
		return behind, nil
	}

	out, err := doLogout(t, h, false)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if out.RemainingSource != string(envelope.TokenSourceFile) {
		t.Errorf("remaining_source = %q, want %q — logout reported success while a file token keeps working",
			out.RemainingSource, envelope.TokenSourceFile)
	}
}

// TestLogoutIsSilentWhenItReallyLogsYouOut is the other half: the
// warning must not fire when nothing remains, or it becomes noise
// nobody reads.
func TestLogoutIsSilentWhenItReallyLogsYouOut(t *testing.T) {
	t.Parallel()
	out, err := doLogout(t, newHarness(t, configured()), false)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if out.RemainingSource != "" {
		t.Errorf("remaining_source = %q, want empty when nothing else authenticates", out.RemainingSource)
	}
}

// TestLoginSaysWhatItReplaced pins the cost of there being ONE stored
// record: logging into a second site overwrites the first, and the
// displaced credential cannot be recovered from this command. Silence
// there means finding out later, on the tenant you did not log into.
func TestLoginSaysWhatItReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	if err := credentials.Save(t.Context(), h.store, credentials.Record{
		Site: "https://other.atlassian.net", Email: "someone@other.example", Token: "old-token",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := login(t.Context(), h.deps, identOf(configured()), "a-good-token", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if out.Replaced == "" {
		t.Error("login silently destroyed the credential for another site")
	}
	if !strings.Contains(out.Replaced, "other.atlassian.net") {
		t.Errorf("replaced = %q, want it to name the displaced site", out.Replaced)
	}
}

// TestLoginIsSilentWhenReplacingItself pins the other half: re-logging
// into the SAME account is a refresh, not a displacement, and a warning
// there is noise that trains people to ignore the real one.
func TestLoginIsSilentWhenReplacingItself(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	if err := credentials.Save(t.Context(), h.store, credentials.Record{
		Site: "https://example.atlassian.net", Email: "person@example.com", Token: "old-token",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := login(t.Context(), h.deps, identOf(configured()), "a-new-token", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if out.Replaced != "" {
		t.Errorf("replaced = %q, want empty when refreshing the same account", out.Replaced)
	}
}

// TestLogoutReportsAFileTokenTakingOver covers the case the takeover
// warning missed: when the effective token already comes from the file,
// the guard returned empty and logout claimed a clean logout while
// every command kept working.
func TestLogoutReportsAFileTokenTakingOver(t *testing.T) {
	t.Parallel()
	site := configured()
	site.Token = "a-file-token"
	site.TokenSource = string(envelope.TokenSourceFile)
	h := newHarness(t, site)

	out, err := doLogout(t, h, false)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if out.RemainingSource != string(envelope.TokenSourceFile) {
		t.Errorf("remaining_source = %q, want %q", out.RemainingSource, envelope.TokenSourceFile)
	}
}

// TestLogoutActuallyRemovesASeededCredential closes a gap in the two
// earlier logout tests: neither seeded anything, so neither could tell
// removal from a no-op.
func TestLogoutActuallyRemovesASeededCredential(t *testing.T) {
	t.Parallel()
	h := newHarness(t, configured())
	if err := credentials.Save(t.Context(), h.store, credentials.Record{
		Site: "https://example.atlassian.net", Email: "person@example.com", Token: "a-token",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := doLogout(t, h, false); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := credentials.Load(t.Context(), h.store); err == nil {
		t.Error("the seeded credential is still stored after logout")
	}
	if h.store.delCall == 0 {
		t.Error("logout never reached the credential store")
	}
}

// TestRenderedIdentityCannotDriveTheTerminal pins that server-controlled
// text is inert on screen. Both displayName and email come from Jira,
// and an escape sequence in either would be executed by the terminal —
// able to rewrite the line, hide text, or forge the output around it.
func TestRenderedIdentityCannotDriveTheTerminal(t *testing.T) {
	t.Parallel()
	hostile := "Real Name\x1b[2K\rSUDO PASSWORD:"

	t.Run("hostile displayName", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		writeIdentity(output.NewView(&buf, clog.ColorNever), "Logged in to", hostile, "person@example.com", "id", "https://example.atlassian.net")
		if strings.ContainsRune(buf.String(), '\x1b') {
			t.Errorf("an escape sequence from the server reached the terminal: %q", buf.String())
		}
		if strings.ContainsRune(buf.String(), '\r') {
			t.Errorf("a carriage return from the server reached the terminal: %q", buf.String())
		}
	})

	// email is Jira-sourced exactly like displayName, and reaches the
	// terminal in BOTH branches writeIdentity's account line can take —
	// with a displayName present, and (here) without one, where account
	// is email alone.
	t.Run("hostile email, no displayName", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		writeIdentity(output.NewView(&buf, clog.ColorNever), "Logged in to", "", hostile, "id", "https://example.atlassian.net")
		if strings.ContainsRune(buf.String(), '\x1b') {
			t.Errorf("an escape sequence from the server reached the terminal: %q", buf.String())
		}
		if strings.ContainsRune(buf.String(), '\r') {
			t.Errorf("a carriage return from the server reached the terminal: %q", buf.String())
		}
	})

	// email alongside a (clean) displayName exercises the OTHER branch,
	// where account is built from both — the branch the H4 review found
	// leaving email raw.
	t.Run("hostile email, with displayName", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		writeIdentity(output.NewView(&buf, clog.ColorNever), "Logged in to", "Real Name", hostile, "id", "https://example.atlassian.net")
		if strings.ContainsRune(buf.String(), '\x1b') {
			t.Errorf("an escape sequence from the server reached the terminal: %q", buf.String())
		}
		if strings.ContainsRune(buf.String(), '\r') {
			t.Errorf("a carriage return from the server reached the terminal: %q", buf.String())
		}
	})
}

// TestConfirmLogoutMapsTheAnswer pins the branch that decides whether a
// credential is deleted.
//
// It had no test at all until the dialog became a dependency: reaching
// the terminal directly meant "Remove" and "Keep" could only be
// exercised by a person, on the one input in this command with
// irreversible consequences.
func TestConfirmLogoutMapsTheAnswer(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		answer  bool
		err     error
		want    bool
		wantErr error
	}{
		{name: "removed", answer: true, want: true},
		{name: "kept", answer: false, want: false},
		// Closing the form is a choice, not a failure, and must not
		// read as one to the caller.
		{name: "canceled", err: huh.ErrUserAborted, wantErr: errAborted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ask := func(_ context.Context, _ prompt.Confirm) (bool, error) { return tt.answer, tt.err }

			got, err := confirmLogout(t.Context(), ask, "person@example.com", "https://example.atlassian.net")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("answer = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfirmLogoutNamesTheAccountAndSite pins that the question is
// answerable. "Are you sure?" cannot be checked against anything; the
// account and site can, and naming the wrong one is how somebody
// confirms a logout they did not mean.
func TestConfirmLogoutNamesTheAccountAndSite(t *testing.T) {
	t.Parallel()
	var asked prompt.Confirm
	ask := func(_ context.Context, c prompt.Confirm) (bool, error) {
		asked = c
		return true, nil
	}

	if _, err := confirmLogout(t.Context(), ask, "person@example.com", "https://example.atlassian.net"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"person@example.com", "example.atlassian.net"} {
		if !strings.Contains(asked.Question, want) {
			t.Errorf("the question does not name %q: %s", want, asked.Question)
		}
	}
	// And it says what removing the token does NOT do.
	if !strings.Contains(asked.Caveat, "not revoked") {
		t.Errorf("the caveat does not say the token survives revocation-wise: %s", asked.Caveat)
	}
	if asked.Affirmative != "Remove" || asked.Negative != "Keep" {
		t.Errorf("buttons are %q/%q, want the acts named", asked.Affirmative, asked.Negative)
	}
}

// CreateIssue and EditIssue satisfy the issue-write half of
// core.JiraClient. This fake's command never creates or edits an issue,
// so they exist to keep the seam satisfied and fail loudly if a test ever
// reaches one by accident.
func (*fakeClient) CreateIssue(context.Context, jira.IssueWriteRequest) (core.IssueCreateWrite, error) {
	return core.IssueCreateWrite{}, errors.New("not used")
}

func (*fakeClient) EditIssue(context.Context, string, jira.IssueWriteRequest, bool) (core.IssueEditWrite, error) {
	return core.IssueEditWrite{}, errors.New("not used")
}
