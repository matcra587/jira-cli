package output

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type quittingScreen struct{}

func (quittingScreen) Init() tea.Cmd                         { return tea.Quit }
func (m quittingScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (quittingScreen) View() tea.View                        { return tea.NewView("screen content") }

type failedScreenWriter struct{}

func (failedScreenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestScreenRetainsDestinationFailureAndCancelsItsSession(t *testing.T) {
	var session context.Context
	err := RunScreen(t.Context(), strings.NewReader(""), failedScreenWriter{}, func(ctx context.Context) tea.Model { session = ctx; return quittingScreen{} })
	var writeErr *WriteError
	if !errors.As(err, &writeErr) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("destination failure was hidden: %v", err)
	}
	if session == nil || !errors.Is(session.Err(), context.Canceled) {
		t.Fatal("finished screen left its request scope active")
	}
}

func TestScreenCanceledBeforeAdmissionDoesNotConstructModel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := RunScreen(ctx, strings.NewReader(""), io.Discard, func(context.Context) tea.Model { t.Fatal("canceled screen constructed a model"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
