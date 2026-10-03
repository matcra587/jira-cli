package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestEpicBinaryReadsShapesCountsAndJQ(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					JQL        string   `json:"jql"`
					Fields     []string `json:"fields"`
					MaxResults int      `json:"maxResults"`
				}
				if r.Method != "POST" || r.URL.Path != "/rest/api/3/search/jql" || json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("epic read used wrong endpoint or body")
					w.WriteHeader(400)
					return
				}
				switch request.JQL {
				case `issuetype = Epic AND project in ("KEY")`:
					if request.MaxResults != 50 || !reflect.DeepEqual(request.Fields, []string{"summary", "status"}) {
						t.Errorf("epic fields/limit changed: %+v", request)
					}
					_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"id":"1","key":"KEY-1","fields":{"summary":"Epic <&>","status":{"name":"In Progress"},"customfield_fixture":false}}]}`)
				case `parent = "KEY-1"`:
					if request.MaxResults != 100 || !reflect.DeepEqual(request.Fields, []string{"status"}) {
						t.Errorf("child fields/limit changed: %+v", request)
					}
					_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"KEY-2","fields":{"status":{"name":"Review"}}},{"key":"KEY-3","fields":{"status":{"name":"Done"}}}]}`)
				default:
					t.Errorf("epic scope changed: %q", request.JQL)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
			stdout, stderr, exit := runBinaryEnv(t, env, "epic", "list", "--project=KEY", "--output="+mode)
			var list envelope.EpicListOutput
			if json.Unmarshal(adfData(t, stdout, mode, "epic.list", true), &list) != nil || exit != 0 || stderr != "" || len(list.Epics) != 1 || list.Epics[0].Key != "KEY-1" || list.Epics[0].Fields.Summary.Value != "Epic <&>" || string(list.Epics[0].Fields.Extra["customfield_fixture"]) != "false" || !list.Pagination.IsLast || list.Pagination.Returned != 1 || list.Truncated {
				t.Fatalf("epic list contract changed: exit=%d output=%s stderr=%q", exit, stdout, stderr)
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "epic", "board", "--project=KEY", "--output="+mode)
			var board envelope.EpicBoardOutput
			if json.Unmarshal(adfData(t, stdout, mode, "epic.board", true), &board) != nil || exit != 0 || stderr != "" || !board.CountsComplete || len(board.Epics) != 1 || board.Epics[0].Counts["Review"] != 1 || board.Epics[0].Counts["Done"] != 1 || board.Totals["In Progress"] != 0 || len(board.Totals) != 4 || !board.Epics[0].Children.Complete || board.Epics[0].Children.Pagination.Returned != 2 {
				t.Fatalf("epic board contract changed: exit=%d output=%s stderr=%q", exit, stdout, stderr)
			}
			query := ".totals.Review"
			if mode == "json" {
				query = ".data.totals.Review"
			}
			stdout, _, exit = runBinaryEnv(t, env, "epic", "board", "--project=KEY", "--output="+mode, "--jq", query)
			if exit != 0 || stdout != "1\n" {
				t.Fatalf("board jq used wrong document: exit=%d output=%q", exit, stdout)
			}
		})
	}
}

func TestEpicBinaryRetainsFailureDataAndBypassesJQ(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "board"} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(verb+" "+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					if n == 1 {
						if verb == "list" {
							_, _ = fmt.Fprint(w, `{"isLast":false,"nextPageToken":"next","issues":[{"key":"KEY-1"}]}`)
						} else {
							_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"KEY-1"},{"key":"KEY-2"}]}`)
						}
						return
					}
					if verb == "board" && n == 2 {
						_, _ = fmt.Fprint(w, `{"isLast":false,"nextPageToken":"children","issues":[{"key":"KEY-3","fields":{"status":{"name":"Done"}}}]}`)
						return
					}
					_, _ = fmt.Fprint(w, `{"isLast":true,"issues":null}`)
				}))
				defer server.Close()
				stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "epic", verb, "--output="+mode, "--jq=.impossible")
				data := adfData(t, stdout, mode, "epic."+verb, false)
				if exit != 5 || !strings.Contains(stdout, "search_response_invalid") {
					t.Fatalf("protocol error was filtered or misclassified: exit=%d output=%s", exit, stdout)
				}
				if verb == "list" {
					var out envelope.EpicListOutput
					if json.Unmarshal(data, &out) != nil || len(out.Epics) != 1 || out.Epics[0].Key != "KEY-1" || out.Pagination.NextPageToken != "next" || calls.Load() != 2 {
						t.Fatalf("epic prefix lost: %s", stdout)
					}
				} else {
					var out envelope.EpicBoardOutput
					if json.Unmarshal(data, &out) != nil || len(out.Epics) != 2 || out.Totals["Done"] != 1 || out.CountsComplete || out.Epics[0].Children.Error == nil || out.Epics[0].Children.Error.Code != "search_response_invalid" || out.Epics[0].Children.Pagination.NextPageToken != "children" || out.Epics[1].Counts != nil || out.Epics[1].Children.Attempted || calls.Load() != 3 {
						t.Fatalf("board partial/pending counts lost: %s", stdout)
					}
				}
			})
		}
	}
}

func TestEpicBinaryLocalDiscoveryAndValidation(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "board"} {
		for _, flags := range [][]string{{"--help"}, {"--limit=0"}, {"--unbounded"}, {"--all", "--limit=1"}, {"--project=K\nEY"}} {
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL="}, append([]string{"epic", verb, "--output=json"}, flags...)...)
			if flags[0] == "--help" {
				if exit != 0 || !strings.Contains(stdout+stderr, "--next-page-token") || !strings.Contains(stdout+stderr, "--project") {
					t.Fatalf("epic help needs authentication or loses flags: exit=%d %s %s", exit, stdout, stderr)
				}
			} else if exit != 3 || !strings.Contains(stdout, "flag_") {
				t.Fatalf("invalid epic input was not refused locally: exit=%d %s", exit, stdout)
			}
		}
	}
}

func TestEpicBinaryDefaultChildBoundsAndEscape(t *testing.T) {
	t.Parallel()
	for _, unbounded := range []bool{false, true} {
		t.Run(strconv.FormatBool(unbounded), func(t *testing.T) {
			t.Parallel()
			var pages atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					JQL string `json:"jql"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid search")
					return
				}
				if request.JQL == "issuetype = Epic" {
					_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"KEY-1"}]}`)
					return
				}
				n := pages.Add(1)
				_, _ = fmt.Fprintf(w, `{"isLast":%t,"nextPageToken":"p%d","issues":[{"key":"KEY-%d","fields":{"status":{"name":"Done"}}}]}`, n == 51, n, n+1)
			}))
			defer server.Close()
			args := []string{"epic", "board", "--all", "--output=compact"}
			if unbounded {
				args = append(args, "--unbounded")
			}
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var out envelope.EpicBoardOutput
			want := 50
			if unbounded {
				want++
			}
			if exit != 0 || json.Unmarshal([]byte(stdout), &out) != nil || len(out.Epics) != 1 || out.Totals["Done"] != want || pages.Load() != int32(want) || out.CountsComplete != unbounded || out.Epics[0].Children.Truncated == unbounded || (!unbounded && out.Epics[0].Children.TruncatedReason != "max_pages") {
				t.Fatalf("documented child cap/escape changed: exit=%d pages=%d output=%s", exit, pages.Load(), stdout)
			}
		})
	}
}
