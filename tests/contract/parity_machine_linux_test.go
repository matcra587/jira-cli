//go:build linux && live

package contract

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"reflect"
	"time"
)

// These original-byte owners are shared by supplementary native acceptance.
func parityArchiveJSON(actual, expected []byte) bool {
	if !jsontext.Value(actual).IsValid() || !jsontext.Value(expected).IsValid() {
		return false
	}
	decode := func(raw []byte) any {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil
		}
		return value
	}
	return reflect.DeepEqual(decode(actual), decode(expected))
}

func parityMachineFrame(raw []byte, lane, operation string, exit int) (data, failures json.RawMessage, valid bool) {
	var root map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &root) != nil || len(root) != 5 {
		return nil, nil, false
	}
	for _, name := range []string{"ok", "meta", "data", "errors", "warnings"} {
		if _, present := root[name]; !present {
			return nil, nil, false
		}
	}
	var ok *bool
	var errors []json.RawMessage
	if json.Unmarshal(root["ok"], &ok) != nil || ok == nil || *ok != (exit == 0) || json.Unmarshal(root["errors"], &errors) != nil || errors == nil || !parityArchiveJSON(root["warnings"], []byte(`[]`)) {
		return nil, nil, false
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(root["meta"], &meta) != nil {
		return nil, nil, false
	}
	text := func(key string) string {
		var value string
		if json.Unmarshal(meta[key], &value) != nil {
			return ""
		}
		return value
	}
	metaFields := 3
	if lane == "after" {
		metaFields++
		if text("contract_version") != "1" {
			return nil, nil, false
		}
	} else if lane != "before" {
		return nil, nil, false
	}
	if exit != 0 {
		metaFields++
		var code *int
		if len(errors) != 1 || json.Unmarshal(meta["exit_code"], &code) != nil || code == nil || *code != exit {
			return nil, nil, false
		}
	} else if len(errors) != 0 || meta["exit_code"] != nil {
		return nil, nil, false
	}
	stamp, err := time.Parse(time.RFC3339, text("timestamp"))
	if len(meta) != metaFields || text("command") != operation || !requestIDRe.MatchString(text("request_id")) || err != nil || stamp.IsZero() {
		return nil, nil, false
	}
	return root["data"], root["errors"], true
}
