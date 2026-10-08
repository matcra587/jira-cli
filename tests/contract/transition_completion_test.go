package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestTransitionDiscoveryFeedsOfflineCompletionAndLocalLifecycle(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/DEMO-1/transitions" || r.URL.Query().Get("expand") != "transitions.fields" {
			t.Error("transition hint producer changed its native read")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(transitionListNative))
	}))
	t.Cleanup(server.Close)
	env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+t.TempDir(), "JIRA_READ_ONLY=true")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "transition", "list", "DEMO-1", "--output=json")
	var read struct {
		OK   bool                               `json:"ok"`
		Data envelope.IssueTransitionListOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &read) != nil || !read.OK || len(read.Data.Results) != 1 || !read.Data.Results[0].OK || read.Data.Results[0].Data == nil || calls.Load() != 1 {
		t.Fatalf("native hint producer exit=%d stderr=%q", exit, stderr)
	}
	if read.Data.Results[0].Data.Transitions.Value[0].Fields.Value["resolution"] == nil || read.Data.Results[0].Data.Extra["future"] == nil {
		t.Fatal("hint production removed native screen fields or extensions")
	}
	local := append(slices.Clone(env), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
	for _, kind := range []string{"cachetransition", "transitionissuekey"} {
		stdout, stderr, exit = runBinaryEnv(t, local, "--@complete="+kind, "--", "DEMO-1")
		if exit != 0 || stderr != "" || stdout != "31\tFinish (Done)\n11\tRestart (To Do)\n" || calls.Load() != 1 {
			t.Fatalf("offline callback %s exit=%d stdout=%q stderr=%q calls=%d", kind, exit, stdout, stderr, calls.Load())
		}
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "cache", "inspect", "transitions", "--output=compact")
	var inspected envelope.CacheInspectOutput
	if exit != 0 || json.Unmarshal([]byte(stdout), &inspected) != nil || !inspected.Complete || len(inspected.Resources) != 1 || inspected.Resources[0].IssueKey != "DEMO-1" || inspected.Resources[0].Count != 2 || inspected.Resources[0].CacheState != "fresh" || calls.Load() != 1 {
		t.Fatalf("local transition inspection exit=%d stderr=%q data=%#v", exit, stderr, inspected)
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "cache", "clear", "transitions", "--force", "--output=compact")
	var cleared envelope.CacheClearOutput
	if exit != 0 || json.Unmarshal([]byte(stdout), &cleared) != nil || cleared.Removed != 1 || calls.Load() != 1 {
		t.Fatalf("local transition clear exit=%d stderr=%q data=%#v", exit, stderr, cleared)
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "--@complete=cachetransition", "--", "DEMO-1")
	if exit != 0 || stdout != "" || stderr != "" || calls.Load() != 1 {
		t.Fatal("cleared transition completion fetched Jira or retained old hints")
	}
}
