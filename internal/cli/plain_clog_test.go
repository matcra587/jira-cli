package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// The per-command human renderer must drop empty fields so a value
// irrelevant to the active backend never reaches the terminal, while a
// meaningful false such as valid=false is still printed.
func TestWriteCommandPlainOmitsEmptyFieldsKeepsFalse(t *testing.T) {
	buf := &bytes.Buffer{}
	data := map[string]any{
		"valid":               false,
		"source":              "keyring",
		"onepassword_account": "",
		"vault":               "",
		"error":               "",
	}
	if err := WriteCommandPlain(buf, "auth.token", data); err != nil {
		t.Fatalf("WriteCommandPlain: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "onepassword_account=") {
		t.Fatalf("empty onepassword_account must be omitted:\n%s", got)
	}
	if strings.Contains(got, "vault=") || strings.Contains(got, "error=") {
		t.Fatalf("empty fields must be omitted:\n%s", got)
	}
	if !strings.Contains(got, "valid=false") {
		t.Fatalf("meaningful valid=false must be kept:\n%s", got)
	}
}

// A command whose data fields already carry the result must not emit a
// message line that merely echoes the command name.
func TestWriteCommandPlainDropsCommandEchoMessage(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := WriteCommandPlain(buf, "auth.whoami", map[string]any{"account": "u@example.com"}); err != nil {
		t.Fatalf("WriteCommandPlain: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "auth whoami") {
		t.Fatalf("renderer must not echo the command name as a message:\n%s", got)
	}
	if !strings.Contains(got, "account=") {
		t.Fatalf("data field must still render:\n%s", got)
	}
}

func TestWriteCommandPlainReturnsWriterFailure(t *testing.T) {
	writers := []struct {
		name string
		new  func() io.Writer
	}{
		{name: "always fail", new: func() io.Writer { return &alwaysFailWriter{} }},
		{name: "fail after prefix", new: func() io.Writer { return &failAfterWriter{remaining: 4} }},
	}
	for _, writer := range writers {
		t.Run(writer.name, func(t *testing.T) {
			err := WriteCommandPlain(writer.new(), "auth.whoami", map[string]any{
				"account": "u@example.com",
			})
			if !errors.Is(err, errWriteSentinel) {
				t.Fatalf("WriteCommandPlain() error = %v, want sentinel", err)
			}
			if _, ok := errors.AsType[*OutputError](err); !ok {
				t.Fatalf("WriteCommandPlain() error type = %T, want *OutputError", err)
			}
		})
	}
}

func TestWriteCommandPlainReturnsShortWrite(t *testing.T) {
	err := WriteCommandPlain(shortWriter{}, "auth.whoami", map[string]any{
		"account": "u@example.com",
	})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteCommandPlain() error = %v, want io.ErrShortWrite", err)
	}
	if _, ok := errors.AsType[*OutputError](err); !ok {
		t.Fatalf("WriteCommandPlain() error type = %T, want *OutputError", err)
	}
}
