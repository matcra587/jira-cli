//go:build outputaudit

package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransitionDiscoveryAuditRejectsLostScreenMetadata(t *testing.T) {
	t.Parallel()
	const workflow = `{"expand":"transitions.fields","transitions":[{"id":"31","hasScreen":true,"fields":{"resolution":{"required":true,"schema":{"type":"resolution"},"allowedValues":[{"id":"7"}],"defaultValue":null}}},{"id":"11","hasScreen":false,"fields":{}}]}`
	check := func(body string) bool {
		var native map[string]any
		if err := json.Unmarshal([]byte(body), &native); err != nil {
			t.Fatal(err)
		}
		data := map[string]any{"results": []any{map[string]any{"ok": true, "data": native}, map[string]any{"ok": true, "data": native}}}
		ok, _ := checkTransitionDiscoveryFields(data)
		return ok
	}
	if !check(workflow) {
		t.Fatal("valid independent workflow stimulus was refused")
	}
	for name, body := range map[string]string{
		"screen":           strings.Replace(workflow, `"hasScreen":true`, `"hasScreen":false`, 1),
		"required":         strings.Replace(workflow, `"required":true`, `"required":false`, 1),
		"schema":           strings.Replace(workflow, `"type":"resolution"`, `"type":"string"`, 1),
		"allowed-identity": strings.Replace(workflow, `"id":"7"`, `"id":"8"`, 1),
		"null-default":     strings.Replace(workflow, `,"defaultValue":null`, "", 1),
		"identity":         strings.Replace(workflow, `"id":"31"`, `"id":"11"`, 1),
		"expand":           strings.Replace(workflow, `transitions.fields`, `fields`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if check(body) {
				t.Fatal("workflow audit accepted changed or missing native metadata")
			}
		})
	}
}

func TestTransitionDiscoveryBoundaryKeepsCLIKeysStrict(t *testing.T) {
	t.Parallel()
	data := map[string]any{"results": []any{map[string]any{"issue": map[string]any{"key": "KEY-1"}, "data": map[string]any{"hasScreen": true, "futureCamelMeta": false}}}}
	if bad := firstNonSnakeKey("", data, passthroughBoundary["issue.transition.list"]...); bad != "" {
		t.Fatal("native workflow lost its passthrough boundary")
	}
	data["cliCamelField"] = true
	if bad := firstNonSnakeKey("", data, passthroughBoundary["issue.transition.list"]...); bad != "$.cliCamelField" {
		t.Fatal("workflow boundary weakened CLI naming checks")
	}
}

func TestTransitionNativeBoundariesKeepCLIKeysStrict(t *testing.T) {
	t.Parallel()
	for _, nested := range []bool{false, true} {
		payload := map[string]any{"historyMetadata": map[string]any{"futureCamelField": false}}
		child := map[string]any{"payload": payload, "attempted": false}
		doc := map[string]any{"payload": payload, "results": []any{map[string]any{"ok": true, "data": child}}}
		boundary := passthroughBoundary["issue.transition"]
		if bad := firstNonSnakeKey("", doc, boundary...); bad != "" {
			t.Fatalf("native transition boundary missing: %s", bad)
		}
		want := "$.cliCamelField"
		if nested {
			child["cliCamelField"] = false
			want = "$.results.data.cliCamelField"
		} else {
			doc["cliCamelField"] = false
		}
		if bad := firstNonSnakeKey("", doc, boundary...); bad != want {
			t.Fatalf("native transition boundary weakened CLI key guard: got %s, want %s", bad, want)
		}
	}
}

func TestTransitionNativePayloadAuditRejectsAuthoredDrift(t *testing.T) {
	t.Parallel()
	valid := `{"transition":{"id":"31"},"fields":{},"update":{"comment":[{"add":{"body":` + corpusCommentBody + `}}]},"historyMetadata":{"extraData":{"sentinel":"native transition value"}},"futureCamelField":{"raw":"<>&\u2028","nullable":null,"large":9007199254740993}}`
	check := func(body string) bool {
		var data map[string]any
		if err := json.Unmarshal([]byte(body), &data); err != nil {
			t.Fatal(err)
		}
		ok, _ := checkTransitionNativePayload(data, "id", "31", true)
		return ok
	}
	if !check(valid) {
		t.Fatal("native transition audit refused its authored request")
	}
	for name, changed := range map[string]string{
		"target":       strings.Replace(valid, `"id":"31"`, `"id":"41"`, 1),
		"empty fields": strings.Replace(valid, `"fields":{}`, `"fields":null`, 1),
		"operation":    strings.Replace(valid, `"add":`, `"remove":`, 1),
		"body":         strings.Replace(valid, `"body":`, `"other":`, 1),
		"ADF":          strings.Replace(valid, `"version":1`, `"version":2`, 1),
		"metadata":     strings.Replace(valid, "native transition value", "changed value", 1),
		"null":         strings.Replace(valid, `,"nullable":null`, "", 1),
		"number":       strings.Replace(valid, "9007199254740993", "1", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if check(changed) {
				t.Fatal("native transition audit accepted changed authored request content")
			}
		})
	}
}
