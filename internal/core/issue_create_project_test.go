package core_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestCreateProjectPreservesNativeIdentityAndOwnership(t *testing.T) {
	t.Parallel()
	const document = ` {"id":"10402","key":"KEY","future":{"n":9007199254740993,"text":"\u003c&"},"optional":null} `
	input := []byte(document)
	read, err := core.DecodeIssueCreateProject(input)
	if err != nil || read.ID != "10402" || read.Key != "KEY" || string(read.Native) != document {
		t.Fatalf("native project identity or future members lost: %+v %v", read, err)
	}
	clear(input)
	if string(read.Native) != document {
		t.Fatal("native project aliases the caller's mutable input")
	}
}

func TestCreateProjectRefusesUnestablishedIdentity(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"missing id": `{"key":"KEY"}`, "missing key": `{"id":"10402"}`,
		"null id": `{"id":null,"key":"KEY"}`, "numeric id": `{"id":10402,"key":"KEY"}`,
		"empty key": `{"id":"10402","key":""}`, "blank key": `{"id":"10402","key":" \t"}`,
		"padded id": `{"id":" 10402","key":"KEY"}`, "padded key": `{"id":"10402","key":"KEY "}`,
		"duplicate identity":      `{"id":"10402","id":"10403","key":"KEY"}`,
		"duplicate future member": `{"id":"10402","key":"KEY","future":{"x":1,"x":2}}`,
		"trailing document":       `{"id":"10402","key":"KEY"}{}`, "array": `[]`, "null": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := core.DecodeIssueCreateProject([]byte(raw))
			if !errors.Is(err, core.ErrIssueMetadataInvalid) || out.ID != "" || out.Key != "" || !bytes.Equal(out.Native, nil) {
				t.Fatalf("invalid project established usable facts: %+v %v", out, err)
			}
		})
	}
}
