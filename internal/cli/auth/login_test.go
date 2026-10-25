package auth

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestPipedTokenRejectsExpiredContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline bool
	}{
		{name: "cancel"},
		{name: "deadline", deadline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if tc.deadline {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			reader := &tokenContextReader{reader: strings.NewReader("synthetic-token\n")}
			_, token, err := gather(ctx, command.Invocation{Stdin: reader}, tokenContextDeps(), loginFlags{withToken: true})
			if !errors.Is(err, want) || token != "" || reader.reads != 0 {
				t.Fatalf("expired token input: error=%v, token accepted=%t, reads=%d", err, token != "", reader.reads)
			}
		})
	}
}

func TestPipedTokenCancelsBlockedInput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline bool
	}{
		{name: "cancel"},
		{name: "deadline", deadline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				input, writer := io.Pipe()
				defer input.Close()  //nolint:errcheck // test owns both pipe ends
				defer writer.Close() //nolint:errcheck // test owns both pipe ends
				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled
				if tc.deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					want = context.DeadlineExceeded
				}
				defer cancel()
				deps := tokenContextDeps()
				clientCalls, storeCalls := 0, 0
				deps.ClientFor = func(string, string, string, string) (core.JiraClient, error) {
					clientCalls++
					return nil, io.ErrUnexpectedEOF
				}
				deps.Credentials = func() (core.CredentialStore, error) {
					storeCalls++
					return nil, io.ErrUnexpectedEOF
				}
				finished := make(chan error, 1)
				go func() {
					_, err := runLogin(ctx, deps, command.Invocation{Stdin: input}, loginFlags{withToken: true})
					finished <- err
				}()
				synctest.Wait()
				if tc.deadline {
					time.Sleep(time.Second)
				} else {
					cancel()
				}
				synctest.Wait()
				select {
				case err := <-finished:
					if !errors.Is(err, want) {
						t.Fatalf("blocked token input error=%v, want %v", err, want)
					}
				default:
					_ = input.Close()
					cleanupErr := <-finished
					t.Fatalf("piped token ignored command context and required forced input cleanup: %v", cleanupErr)
				}
				if clientCalls != 0 || storeCalls != 0 {
					t.Fatalf("expired token input opened clients=%d, stores=%d", clientCalls, storeCalls)
				}
			})
		})
	}
}

func TestPipedTokenRejectsAnswerAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := &tokenContextReader{reader: strings.NewReader("synthetic-token\n"), cancel: cancel}
	_, token, err := gather(ctx, command.Invocation{Stdin: reader}, tokenContextDeps(), loginFlags{withToken: true})
	if !errors.Is(err, context.Canceled) || token != "" {
		t.Fatalf("expired token answer: error=%v, token accepted=%t", err, token != "")
	}
}

func TestReadTokenStopsAtItsByteLimit(t *testing.T) {
	reader := &boundedTokenSource{}
	_, err := readToken(t.Context(), command.Invocation{Stdin: reader})
	if err == nil || reader.total != maxTokenBytes+1 {
		t.Fatalf("token limit: error=%v, bytes read=%d, want %d", err, reader.total, maxTokenBytes+1)
	}
}

func tokenContextDeps() Deps {
	return Deps{Site: func(ctx context.Context) (SiteInfo, error) {
		return SiteInfo{BaseURL: "https://example.invalid", Email: "input-fixture@example.invalid"}, nil
	}}
}

type tokenContextReader struct {
	reader io.Reader
	cancel context.CancelFunc
	reads  int
}

func (r *tokenContextReader) Read(data []byte) (int, error) {
	r.reads++
	n, err := r.reader.Read(data)
	if r.cancel != nil {
		r.cancel()
	}
	return n, err
}

type boundedTokenSource struct {
	total int
}

func (r *boundedTokenSource) Read(data []byte) (int, error) {
	if r.total >= 2*maxTokenBytes {
		return 0, io.ErrUnexpectedEOF
	}
	for i := range data {
		data[i] = 'x'
	}
	r.total += len(data)
	return len(data), nil
}
