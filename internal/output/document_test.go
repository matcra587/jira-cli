package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestDocumentOverflowIncludesWrappedWideAndStyledLines(t *testing.T) {
	for _, tc := range []struct {
		name          string
		content       string
		width, height int
		want          bool
	}{
		{name: "empty", width: 10, height: 1},
		{name: "unknown size", content: "one\ntwo\n", width: 0, height: 1},
		{name: "exact fit", content: "one\ntwo\n", width: 10, height: 2},
		{name: "blank row", content: "one\n\ntwo\n", width: 10, height: 2, want: true},
		{name: "wrapped", content: "12345678901\n", width: 5, height: 2, want: true},
		{name: "styled fit", content: "\x1b[31m12345\x1b[0m\n", width: 5, height: 1},
		{name: "wide characters", content: "界界界\n", width: 5, height: 1, want: true},
		{name: "wide characters cannot split", content: "界界界\n", width: 3, height: 2, want: true},
		{name: "wide characters odd width fit", content: "界界\n", width: 3, height: 2},
		{name: "combining characters fit", content: "e\u0301e\u0301e\u0301\n", width: 3, height: 1},
		{name: "tab adds display columns", content: "1234567\txxxxx\n", width: 12, height: 1, want: true},
		{name: "tab at narrow margin", content: "123\tx\n", width: 4, height: 1},
		{name: "trailing spaces consume rows", content: "           \n", width: 5, height: 2, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := documentOverflows(tc.content, tc.width, tc.height); got != tc.want {
				t.Fatalf("overflow=%v, want%v", got, tc.want)
			}
		})
	}
}

type unusedDocumentInput struct{ reads int }

func (r *unusedDocumentInput) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("document read borrowed input")
}

func TestDocumentPipesAndMachineModesNeverReadPagerInput(t *testing.T) {
	for _, format := range []Format{FormatHuman, FormatJSON, FormatCompact} {
		var destination bytes.Buffer
		input := &unusedDocumentInput{}
		res := Result{Envelope: envelope.Envelope{OK: true, Data: "document"}, Human: func(v *View, _ envelope.Envelope) error {
			v.Block("line one\n\nline two")
			return nil
		}}
		if err := RenderDocument(t.Context(), &destination, format, res, DocumentOptions{Input: input, Diagnostics: io.Discard}); err != nil {
			t.Fatal(err)
		}
		if input.reads != 0 || destination.Len() == 0 {
			t.Fatal("noninteractive document consumed input or disappeared")
		}
		if format == FormatHuman && destination.String() != "line one\n\nline two\n" {
			t.Fatalf("piped human document changed layout: %q", destination.String())
		}
	}
}

func TestDocumentCanceledBeforePublication(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var destination bytes.Buffer
	if err := RenderDocument(ctx, &destination, FormatJSON, Result{}, DocumentOptions{}); !errors.Is(err, context.Canceled) || destination.Len() != 0 {
		t.Fatalf("canceled document published output: %v", err)
	}
}

type shortDocumentWriter struct{}

func (shortDocumentWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestDocumentDeliveryFailure(t *testing.T) {
	err := writeDocument(shortDocumentWriter{}, "document")
	var writeErr *WriteError
	if !errors.As(err, &writeErr) || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short delivery reported success: %v", err)
	}
}

func TestBuiltinDocumentPagerNavigationAndResize(t *testing.T) {
	model := &documentPager{content: "first\nsecond\nthird\nlast"}
	model.Update(tea.WindowSizeMsg{Width: 20, Height: 3})
	if !model.ready || !model.View().AltScreen || !strings.Contains(model.View().Content, "first") {
		t.Fatal("pager did not initialize its document and alternate screen")
	}
	model.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if !strings.Contains(model.View().Content, "last") {
		t.Fatal("end key did not reach final document line")
	}
	model.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if !strings.Contains(model.View().Content, "first") {
		t.Fatal("home key did not restore first document line")
	}
	model.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	if model.view.Width() < 1 || model.view.Height() < 1 {
		t.Fatal("resize left invalid viewport dimensions")
	}
	_, command := model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if command == nil {
		t.Fatal("quit key did not terminate pager")
	}
}
