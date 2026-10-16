//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type parityRankAttempt struct {
	Lane     string   `json:"lane"`
	Key      string   `json:"key"`
	Issues   []string `json:"issues"`
	Anchor   string   `json:"anchor"`
	Position string   `json:"position"`
	Observed []string `json:"observed_order,omitempty"`
	Bound    bool     `json:"bound"`
}

func parityRankData(data []byte, lane string, issues []string, anchor, position string, preview, refused bool) bool {
	var expected map[string]any
	switch lane {
	case "before":
		if refused {
			return parityAuthoringJSON(data, []byte(`null`))
		}
		expected = map[string]any{"anchor": anchor, "position": position, "order": issues, "chunks": (len(issues) + 49) / 50, "dry_run": preview}
	case "after":
		rows := make([]any, 0, len(issues))
		for i, key := range issues {
			row := map[string]any{"issue": map[string]any{"key": key}, "ok": !refused}
			if refused {
				row["error"] = map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: PUT /rest/agile/1.0/issue/rank", "hint": "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}
				if i >= 50 {
					row["error"] = map[string]any{"code": "rank_not_attempted", "message": "ranking not attempted because an earlier batch did not complete", "hint": "This move was not submitted because an earlier ranking batch did not complete. Inspect earlier outcomes and the backlog before choosing an anchor for the remaining moves."}
				}
			} else {
				row["data"] = map[string]any{}
			}
			rows = append(rows, row)
		}
		expected = map[string]any{"anchor": anchor, "position": position, "dry_run": preview, "results": rows}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func TestParityActualBinaryRankLocal(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string), Differences: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		for _, mode := range []string{"json", "compact"} {
			for _, position := range []string{"before", "after"} {
				for _, count := range []int{1, 2, 50, 51, 100, 101} {
					t.Run(fmt.Sprintf("%s/%s/%s/%d", lane.name, mode, position, count), func(t *testing.T) {
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
						defer server.Close()
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						before := parityLocalTree(t, home)
						issues := make([]string, count)
						for i := range issues {
							issues[i] = "PAIR-" + strconv.Itoa(i+1)
						}
						args := append(append([]string{"issue", "rank"}, issues...), "--"+position+"=PAIR-999", "--dry-run")
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						began := time.Now().UTC().Truncate(time.Second)
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						exit := 0
						if result.Exit != exit || exit == 0 && len(result.Stderr) != 0 || calls.Load() != 0 {
							t.Fatalf("rank local entry/effects changed: exit%d want%d HTTP%d stdout%s stderr%s", result.Exit, exit, calls.Load(), result.Stdout, result.Stderr)
						}
						if lane.name == "before" {
							// Legacy ranks touch completion history even during a preview.
							want := append(slices.Clone(issues), "PAIR-999")
							want = want[:min(50, len(want))]
							path := parityLocalHistory(t, home, lane.name, server.URL, want, began)
							parityOpenOnlyHistory(t, home, path, before)
						} else if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatal("current local rank preview or refusal changed its owned files")
						}
						raw := result.Stdout
						if mode == "json" || exit != 0 {
							parityAuthoringData(t, raw, lane.name, "issue.rank", exit)
							raw = parityOriginalFrameData(raw)
						}
						if !parityRankData(raw, lane.name, issues, "PAIR-999", position, true, false) {
							t.Fatalf("rank preview changed the complete requested ordering: %s", raw)
						}
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						check := fmt.Sprintf("I021.rank.local_%s_%d.%s", position, count, mode)
						evidence.Checks[check] = append(evidence.Checks[check], lane.name)
					})
				}
			}
		}
	}
	saveParityLocal(t, "rank", evidence)
}

func (p *parityRun) invokeRank(t *testing.T, lane parityLane, keys, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if !slices.Contains(p.evidence.Lanes, lane) {
		t.Fatal("rank invocation requires an admitted lane")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		t.Fatal(err)
	}
	if err := p.owner.checkRankMutation(keys, args); err != nil {
		t.Fatal(err)
	}
	return p.invoke(t, lane, extra, args...)
}

func TestLiveParityRank(t *testing.T) { runParityRank(t, 1) }

func TestLiveParityRankBatches(t *testing.T) { runParityRank(t, 51) }

func runParityRank(t *testing.T, sourceCount int) {
	t.Helper()
	if sourceCount != 1 && sourceCount != 51 {
		t.Fatal("rank acceptance requires an explicit reviewed fixture size")
	}
	p := newParityRun(t)
	boardID, err := strconv.ParseUint(requiredEnv(t, "JIRA_LIVE_BOARD_ID"), 10, 64)
	if err != nil || boardID == 0 {
		t.Fatal("rank comparison requires a positive designated board")
	}
	metadata := rawRead(t, p.owner, "api", "get", "/rest/agile/1.0/board/"+strconv.FormatUint(boardID, 10)+"/configuration")
	var board struct {
		Filter  struct{ ID string }
		Ranking struct {
			Field int64 `json:"rankCustomFieldId"`
		}
	}
	if json.Unmarshal(metadata.Response.JSON, &board) != nil || board.Ranking.Field <= 0 {
		t.Fatal("designated board did not supply its public rank field")
	}
	filterID, err := strconv.ParseUint(board.Filter.ID, 10, 64)
	if err != nil || filterID == 0 {
		t.Fatal("designated board did not supply a public filter ID")
	}
	keys := make([]string, sourceCount+1)
	quoted := make([]string, len(keys))
	for i := range keys {
		keys[i] = p.create(t, p.evidence.Lanes[i%2])
		quoted[i] = strconv.Quote(keys[i])
	}
	query := "filter = " + strconv.FormatUint(filterID, 10) + " AND key in (" + strings.Join(quoted, ",") + ") ORDER BY cf[" + strconv.FormatInt(board.Ranking.Field, 10) + "] ASC"
	initial := waitRankOrder(t, p.owner, query, keys, nil)
	// Derive the source order and last anchor from the observed preimage.
	// The first direction changes it; the second restores it for the next lane.
	positions := []string{"after", "before"}
	issues, anchor := slices.Clone(initial[:sourceCount]), initial[sourceCount]
	declared := append(slices.Clone(issues), anchor)
	original := make([]liveIssue, len(keys))
	for i, key := range keys {
		original[i] = p.native(t, key)
	}
	prefix := "I021.rank."
	if sourceCount == 51 {
		prefix = "I021.rank.batch_51_"
	}
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			baseline := waitRankOrder(t, p.owner, query, keys, initial)
			for _, position := range positions {
				p.owner.allowRank(issues, position, anchor)
				args := rankArgs(issues, position, anchor)
				for _, step := range []string{"preview", "read_only", "native"} {
					call := slices.Clone(args)
					var extra []string
					switch step {
					case "preview":
						call = append(call, "--dry-run")
					case "read_only":
						extra = []string{"JIRA_READ_ONLY=true"}
					}
					attempt := -1
					if step == "native" {
						attempt = len(p.evidence.Ranks)
						p.evidence.Ranks = append(p.evidence.Ranks, parityRankAttempt{Lane: lane.Name, Key: issues[0], Issues: slices.Clone(issues), Anchor: anchor, Position: position})
						p.save(t)
					}
					frame, stdout, exit := p.invokeRank(t, lane, declared, extra, call...)
					if step == "read_only" {
						if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityRankData(parityOriginalFrameData(stdout), lane.Name, issues, anchor, position, false, true) {
							t.Fatal("rank read-only refusal lost exact authored context or invented success")
						}
					} else if exit != 0 || !frame.OK || !parityRankData(parityOriginalFrameData(stdout), lane.Name, issues, anchor, position, step == "preview", false) {
						t.Fatal("rank receipt lost its ordered request or acknowledgement; never replay")
					}
					want := baseline
					if step == "native" {
						want = append(slices.Clone(issues), anchor)
						if position == "after" {
							want = append([]string{anchor}, issues...)
						}
						if slices.Equal(want, baseline) {
							t.Fatal("live rank guard did not require an actual changed order")
						}
					}
					baseline = waitRankOrder(t, p.owner, query, keys, want)
					if attempt >= 0 {
						p.evidence.Ranks[attempt].Observed = slices.Clone(baseline)
						p.evidence.Ranks[attempt].Bound = true
						p.save(t)
					}
					p.check(t, lane, prefix+position+"_"+step)
				}
			}
			if !slices.Equal(baseline, initial) {
				t.Fatal("rank lane did not restore its equivalent initial ordering")
			}
			for i, key := range keys {
				if !nativeMetadataMatches(p.native(t, key).Fields, original[i].Fields, nil) {
					t.Fatal("ranking changed unrelated owned issue fields")
				}
			}
		})
		if t.Failed() {
			break
		}
	}
}

func TestParityRankReceiptGuards(t *testing.T) {
	issues := []string{"PAIR-2", "PAIR-1"}
	for _, tc := range []struct {
		name, lane, raw string
		preview         bool
	}{
		{"before-preview", "before", `{"anchor":"PAIR-9","position":"before","order":["PAIR-2","PAIR-1"],"chunks":1,"dry_run":true}`, true},
		{"before-live", "before", `{"anchor":"PAIR-9","position":"before","order":["PAIR-2","PAIR-1"],"chunks":1,"dry_run":false}`, false},
		{"after-preview", "after", `{"anchor":"PAIR-9","position":"before","dry_run":true,"results":[{"issue":{"key":"PAIR-2"},"ok":true,"data":{}},{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]}`, true},
		{"after-live", "after", `{"anchor":"PAIR-9","position":"before","dry_run":false,"results":[{"issue":{"key":"PAIR-2"},"ok":true,"data":{}},{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !parityRankData([]byte(tc.raw), tc.lane, issues, "PAIR-9", "before", tc.preview, false) {
				t.Fatal("independent complete rank receipt refused")
			}
			for name, replacement := range map[string][2]string{
				"foreign-source":  {"PAIR-2", "PAIR-3"},
				"foreign-anchor":  {"PAIR-9", "PAIR-8"},
				"wrong-side":      {`"position":"before"`, `"position":"after"`},
				"missing-preview": {fmt.Sprintf(`"dry_run":%t`, tc.preview), `"unrelated":true`},
				"null-preview":    {fmt.Sprintf(`"dry_run":%t`, tc.preview), `"dry_run":null`},
				"invented-effect": {`{`, `{"ranked":true,`},
			} {
				t.Run(name, func(t *testing.T) {
					changed := strings.Replace(tc.raw, replacement[0], replacement[1], 1)
					if !json.Valid([]byte(changed)) {
						t.Fatal("negative fixture must remain complete JSON")
					}
					if parityRankData([]byte(changed), tc.lane, issues, "PAIR-9", "before", tc.preview, false) {
						t.Fatal("rank oracle admitted sparse, foreign or invented outcomes")
					}
				})
			}
			if parityRankData([]byte(tc.raw), tc.lane, []string{"PAIR-1", "PAIR-2"}, "PAIR-9", "before", tc.preview, false) {
				t.Fatal("rank oracle ignored authored source ordering")
			}
		})
	}
}

func parityRankRequest(reader io.Reader, expected []byte) bool {
	raw, err := io.ReadAll(io.LimitReader(reader, 8193))
	return err == nil && len(raw) <= 8192 && parityAuthoringJSON(raw, expected)
}

func TestParityRankRequestGuards(t *testing.T) {
	want := []byte(`{"issues":["PAIR-1"],"rankBeforeIssue":"PAIR-9"}`)
	padded := append(slices.Clone(want), bytes.Repeat([]byte(" "), 8192-len(want))...)
	if !parityRankRequest(bytes.NewReader(want), want) || !parityRankRequest(bytes.NewReader(padded), want) {
		t.Fatal("complete bounded native rank request refused")
	}
	for name, raw := range map[string][]byte{
		"extra-document":              append(slices.Clone(want), []byte(`{}`)...),
		"hidden-document-after-bound": append(slices.Clone(padded), []byte(`{}`)...),
		"foreign-anchor":              bytes.ReplaceAll(want, []byte("PAIR-9"), []byte("PAIR-8")),
		"duplicate-root":              []byte(`{"issues":[],"issues":["PAIR-1"],"rankBeforeIssue":"PAIR-9"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if parityRankRequest(bytes.NewReader(raw), want) {
				t.Fatal("rank wire oracle accepted hidden, ambiguous or changed request bytes")
			}
		})
	}
}

func TestParityActualBinaryRankBatchBoundary(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		for _, position := range []string{"before", "after"} {
			for _, count := range []int{51, 100, 101} {
				t.Run(fmt.Sprintf("%s/%s/%d", lane.name, position, count), func(t *testing.T) {
					issues := make([]string, count)
					for i := range issues {
						issues[i] = "PAIR-" + strconv.Itoa(i+1)
					}
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n := int(calls.Add(1))
						start := (n - 1) * 50
						if start >= count {
							t.Error("rank batch replayed or sent an unexpected extra request")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						user, token, ok := r.BasicAuth()
						want := map[string]any{"issues": issues[start:min(start+50, count)]}
						if n == 1 {
							want["rank"+strings.ToUpper(position[:1])+position[1:]+"Issue"] = "PAIR-999"
						} else {
							want["rankAfterIssue"] = issues[start-1]
						}
						expected, marshalErr := json.Marshal(want)
						if r.Method != http.MethodPut || r.URL.RequestURI() != "/rest/agile/1.0/issue/rank" || !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" || marshalErr != nil || !parityRankRequest(r.Body, expected) {
							t.Error("rank batch changed exact authentication, route, source order or chained anchor")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					tree := parityLocalTree(t, home)
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					began := time.Now().UTC().Truncate(time.Second)
					args := append(append([]string{"issue", "rank"}, issues...), "--"+position+"=PAIR-999")
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config}, nil, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					parityAuthoringData(t, result.Stdout, lane.name, "issue.rank", 0)
					if result.Exit != 0 || len(result.Stderr) != 0 || int(calls.Load()) != (count+49)/50 || !parityRankData(parityOriginalFrameData(result.Stdout), lane.name, issues, "PAIR-999", position, false, false) {
						t.Fatalf("rank chunk boundary was not actually executed: exit%d HTTP%d stdout%s stderr%s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
					}
					// Both live commands publish only their scoped, capped key history.
					path := parityLocalHistory(t, home, lane.name, server.URL, issues[:50], began)
					parityOpenOnlyHistory(t, home, path, tree)
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					check := fmt.Sprintf("I021.rank.batch_%d_%s", count, position)
					evidence.Checks[check] = append(evidence.Checks[check], lane.name)
				})
			}
		}
	}
	saveParityLocal(t, "rank-batch-fixture", evidence)
}
