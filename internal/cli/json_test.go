package cli

import (
	"errors"
	"io"
	"testing"
)

func outputEnvelopeFixture() Envelope {
	return Envelope{
		OK:       true,
		Meta:     Meta{Command: "version"},
		Data:     map[string]any{"version": "test"},
		Errors:   []Error{},
		Warnings: []Warning{},
	}
}

func TestMachineRenderersReturnWriterFailure(t *testing.T) {
	renderers := []struct {
		name   string
		render func(io.Writer) error
	}{
		{
			name: "json",
			render: func(w io.Writer) error {
				return WriteEnvelope(w, outputEnvelopeFixture())
			},
		},
		{
			name: "compact",
			render: func(w io.Writer) error {
				return WriteCompact(w, map[string]any{"version": "test"})
			},
		},
	}
	writers := []struct {
		name string
		new  func() io.Writer
	}{
		{name: "always fail", new: func() io.Writer { return &alwaysFailWriter{} }},
		{name: "fail after prefix", new: func() io.Writer { return &failAfterWriter{remaining: 4} }},
	}

	for _, renderer := range renderers {
		for _, writer := range writers {
			t.Run(renderer.name+"/"+writer.name, func(t *testing.T) {
				err := renderer.render(writer.new())
				if !errors.Is(err, errWriteSentinel) {
					t.Fatalf("render error = %v, want sentinel", err)
				}
				if _, ok := errors.AsType[*OutputError](err); !ok {
					t.Fatalf("render error type = %T, want *OutputError", err)
				}
			})
		}
	}
}

func TestWriteEnvelopeReturnsShortWrite(t *testing.T) {
	err := WriteEnvelope(shortWriter{}, outputEnvelopeFixture())
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteEnvelope() error = %v, want io.ErrShortWrite", err)
	}
	if _, ok := errors.AsType[*OutputError](err); !ok {
		t.Fatalf("WriteEnvelope() error type = %T, want *OutputError", err)
	}
}
