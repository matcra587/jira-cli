package jira

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func rankTestClient(t *testing.T, handler http.HandlerFunc) RankService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := NewClient(WithBaseURL(srv.URL), WithBasicAuth("t@example.net", "token"))
	return NewRankService(client)
}

func TestRankSuccessNoContent(t *testing.T) {
	service := rankTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/rest/agile/1.0/issue/rank" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if _, err := service.Rank(context.Background(), []string{"PROJ-1", "PROJ-2"}, "PROJ-3", ""); err != nil {
		t.Fatalf("Rank() error = %v, want success on an empty 204", err)
	}
}

func TestRankMultiStatusPartialIsTyped(t *testing.T) {
	service := rankTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"entries":[
			{"issues":["PROJ-1"],"status":200},
			{"issues":["PROJ-2"],"errors":["Rank field not available"],"status":400}
		]}`))
	})
	_, err := service.Rank(context.Background(), []string{"PROJ-1", "PROJ-2"}, "", "PROJ-3")
	var partial *RankPartialError
	if !errors.As(err, &partial) {
		t.Fatalf("Rank() error = %v, want RankPartialError", err)
	}
	if len(partial.Failed) != 1 || partial.Failed[0].Issues[0] != "PROJ-2" {
		t.Fatalf("Failed = %+v, want only the 400 entry", partial.Failed)
	}
	if !strings.Contains(err.Error(), "Rank field not available") {
		t.Fatalf("partial error dropped Jira's reason: %s", err)
	}
}

func TestRankBadRequestIsTypedRejected(t *testing.T) {
	service := rankTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorMessages":["Rank field not found for board"]}`))
	})
	_, err := service.Rank(context.Background(), []string{"PROJ-1"}, "PROJ-3", "")
	if _, ok := errors.AsType[*RankRejectedError](err); !ok {
		t.Fatalf("Rank() error = %v, want RankRejectedError on a 400", err)
	}
}

func TestRankLocalGuards(t *testing.T) {
	service := rankTestClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("local guard failures must not reach the server")
	})
	ctx := context.Background()
	if _, err := service.Rank(ctx, nil, "PROJ-3", ""); err == nil {
		t.Fatal("empty key set did not error")
	}
	tooMany := make([]string, RankChunkLimit+1)
	for i := range tooMany {
		tooMany[i] = "PROJ-1"
	}
	if _, err := service.Rank(ctx, tooMany, "PROJ-3", ""); err == nil {
		t.Fatal("over-limit key set did not error")
	}
	if _, err := service.Rank(ctx, []string{"PROJ-1"}, "PROJ-2", "PROJ-3"); err == nil {
		t.Fatal("both anchors did not error")
	}
	if _, err := service.Rank(ctx, []string{"PROJ-1"}, "", ""); err == nil {
		t.Fatal("missing anchor did not error")
	}
}
