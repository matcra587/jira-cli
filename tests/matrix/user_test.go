//go:build flagmatrix

package matrix

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
)

func TestUserSearchFlagEffects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		flag  string
		args  []string
		want  []int
		limit string
	}{
		{flag: "start", args: []string{"--start=17"}, want: []int{17}, limit: "50"},
		{flag: "limit", args: []string{"--limit=3"}, want: []int{0}, limit: "3"},
		{flag: "all", args: []string{"--all"}, want: []int{0, 1}, limit: "50"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()
			var offsets []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start, err := strconv.Atoi(r.URL.Query().Get("startAt"))
				if err != nil || r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" || r.URL.Query().Get("query") != "sam" || r.URL.Query().Get("maxResults") != tc.limit {
					t.Errorf("flag changed wrong request: %s", r.URL)
				}
				offsets = append(offsets, start)
				if tc.flag == "all" && start == 1 {
					_, _ = fmt.Fprint(w, `[]`)
					return
				}
				_, _ = fmt.Fprint(w, `[{"accountId":"fixture-id","active":true}]`)
			}))
			defer server.Close()
			r := fwExec(t, server.URL, nil, "", append([]string{"user", "search", "sam"}, tc.args...)...)
			out := fwParse(t, r)
			if r.Exit != 0 || !reflect.DeepEqual(offsets, tc.want) || fwAt(t, out.Data, "count") != float64(1) || fwAt(t, out.Data, "pagination", "complete") != (tc.flag == "all") {
				t.Fatalf("flag ignored: %s exit=%d offsets=%v", tc.flag, r.Exit, offsets)
			}
			Prove(t, "jira user search", tc.flag, "server offset/page size and empty-page completion verified through the real binary")
		})
	}
}
