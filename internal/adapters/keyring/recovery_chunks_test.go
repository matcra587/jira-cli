package keyring

import (
	"bytes"
	"context"
	"encoding/base64"
	jsonv2 "encoding/json/v2"
	"errors"
	"strings"
	"testing"

	native "github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/core"
)

func largeBackupFixture(t *testing.T) (*Store, *nativeRecoveryFixture, string, []byte, []string) {
	t.Helper()
	store, fixture := isolatedRecoveryStore()
	fixture.values["default"] = strings.Repeat("synthetic prior <&>世\u2028", 90)
	before, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := store.Replacement(before, strings.Repeat("synthetic current <&>界", 90))
	if err != nil {
		t.Fatal(err)
	}
	name := core.NewCredentialBackupID()
	old, next, err := store.snapshotPair(before, expected)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jsonv2.Marshal(recoveryBackup{Version: 2, Name: name, Before: old, Expected: next, ProofKey: before.(*snapshot).proofKey})
	if err != nil || len(raw) <= windowsCredentialBlobLimit {
		t.Fatal("fixture did not exceed the native backup blob limit")
	}
	var writes []string
	set := store.native.set
	store.native.set = func(service, account, value string) error {
		writes = append(writes, account)
		if len(value) > windowsCredentialBlobLimit {
			return native.ErrSetDataTooBig
		}
		return set(service, account, value)
	}
	if entered, err := store.writeRecoveryBackup(t.Context(), name, raw, windowsCredentialBlobLimit); err != nil || !entered {
		t.Fatal("bounded provider backup publication failed")
	}
	return store, fixture, name, raw, writes
}

func TestChunkedKeyringBackupPublishesDescriptorLastAndRoundTrips(t *testing.T) {
	t.Parallel()
	store, fixture, name, raw, writes := largeBackupFixture(t)
	if len(writes) < 3 || writes[len(writes)-1] != name {
		t.Fatal("recovery descriptor preceded complete provider pieces")
	}
	for i, account := range writes[:len(writes)-1] {
		if account != recoveryChunkName(name, i) || len(fixture.values[account]) > windowsCredentialBlobLimit {
			t.Fatal("provider piece escaped its parent identity or native blob bound")
		}
	}
	got, err := store.readRecoveryBackup(t.Context(), name, fixture.values[name])
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("chunk transport changed private native payload bytes")
	}
	before, expected, err := store.ReadBackup(t.Context(), name)
	if err != nil || before.Commitment() == "" || expected.Commitment() == "" {
		t.Fatal("provider snapshots could not reopen the retained v2 payload")
	}
	original := fixture.values["default"]
	if attempted, err := store.Apply(t.Context(), "default", name); !attempted || err != nil {
		t.Fatal("chunked apply did not publish the replacement")
	}
	want, present := expected.Secret()
	if !present || fixture.values["default"] != want {
		t.Fatal("chunked apply changed replacement bytes")
	}
	if attempted, err := store.Restore(t.Context(), "default", name); !attempted || err != nil || fixture.values["default"] != original {
		t.Fatal("chunked restore changed prior bytes")
	}
	if _, _, err := store.ReadBackup(t.Context(), name); err != nil {
		t.Fatal("restore discarded its multipart provider backup")
	}
}

func TestChunkedKeyringBackupRejectsCorruptionBeforePrimaryWrites(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "overlong", "invalid base64", "changed payload", "wrong name", "wrong size", "wrong count", "unknown member", "duplicate version"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			store, fixture, name, raw, _ := largeBackupFixture(t)
			piece := recoveryChunkName(name, 0)
			switch mode {
			case "missing":
				delete(fixture.values, piece)
			case "overlong":
				fixture.values[piece] += "AAAA"
			case "invalid base64":
				fixture.values[piece] = strings.Repeat("!", len(fixture.values[piece]))
			case "changed payload":
				changed := bytes.Replace(raw, []byte("synthetic prior"), []byte("corrupted prior"), 1)
				if bytes.Equal(changed, raw) {
					t.Fatal("corruption stimulus did not change the private payload")
				}
				fixture.values[piece] = base64.StdEncoding.EncodeToString(changed[:recoveryChunkBytes])
			default:
				var descriptor map[string]any
				if err := jsonv2.Unmarshal([]byte(fixture.values[name]), &descriptor); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "wrong name":
					descriptor["name"] = core.NewCredentialBackupID()
				case "wrong size":
					descriptor["bytes"] = maxChunkedRecoveryBytes + 1
				case "wrong count":
					descriptor["parts"] = -1
				case "unknown member":
					descriptor["unexpected"] = "synthetic private data"
				}
				encoded, err := jsonv2.Marshal(descriptor)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "duplicate version" {
					encoded = append([]byte(`{"version":3,`), encoded[1:]...)
				}
				fixture.values[name] = string(encoded)
			}
			original := fixture.values["default"]
			fixture.calls = nil
			for _, operation := range []func(context.Context, string, string) (bool, error){store.Apply, store.Restore} {
				if attempted, err := operation(t.Context(), "default", name); attempted || !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
					t.Fatal("corrupt provider pieces reached a primary mutation")
				}
			}
			if fixture.values["default"] != original || strings.Contains(strings.Join(fixture.calls, ","), "set") {
				t.Fatal("corrupt recovery changed primary native state")
			}
		})
	}
}

func TestChunkedKeyringBackupRetainsPartialAdmissionAndCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"piece failure", "canceled after piece", "descriptor failure", "existing piece"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			store, fixture := isolatedRecoveryStore()
			name := core.NewCredentialBackupID()
			raw := []byte(strings.Repeat("synthetic", 500))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("synthetic native write refusal")
			sets := 0
			fixture.hook = func(operation string) error {
				if operation != "set" {
					return nil
				}
				sets++
				switch mode {
				case "piece failure":
					return failure
				case "canceled after piece":
					cancel()
				case "descriptor failure":
					if _, exists := fixture.values[name]; exists {
						return failure
					}
				}
				return nil
			}
			if mode == "existing piece" {
				fixture.values[recoveryChunkName(name, 1)] = "original owned piece"
			}
			entered, err := store.writeRecoveryBackup(ctx, name, raw, windowsCredentialBlobLimit)
			if err == nil || fixture.values["default"] != "synthetic-before" {
				t.Fatal("failed backup changed primary state or concealed its error")
			}
			if mode == "existing piece" {
				if entered || sets != 0 || !errors.Is(err, core.ErrCredentialBackupExists) || fixture.values[recoveryChunkName(name, 1)] != "original owned piece" {
					t.Fatal("existing piece was replaced before backup admission")
				}
			} else if !entered || sets == 0 || fixture.values[recoveryChunkName(name, 0)] == "" {
				t.Fatal("native admission or partial private pieces were discarded")
			}
			if mode == "canceled after piece" && (!errors.Is(err, context.Canceled) || sets != 1 || fixture.values[name] != "") {
				t.Fatal("canceled multipart backup published readiness or entered another write")
			}
		})
	}
}

func TestChunkedKeyringBackupCanceledFinalPieceReadRetainsCancellation(t *testing.T) {
	t.Parallel()
	store, fixture, name, _, _ := largeBackupFixture(t)
	var descriptor chunkedRecovery
	if err := jsonv2.Unmarshal([]byte(fixture.values[name]), &descriptor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reads := 0
	fixture.hook = func(operation string) error {
		if operation == "get" {
			reads++
			if reads == descriptor.Parts+1 {
				cancel()
			}
		}
		return nil
	}
	if before, expected, err := store.ReadBackup(ctx, name); before != nil || expected != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("completed final native piece read lost cancellation")
	}
}

func TestChunkedKeyringBackupProviderReadFailureKeepsCauseAndClassification(t *testing.T) {
	t.Parallel()
	store, fixture, name, _, _ := largeBackupFixture(t)
	failure := errors.New("synthetic native provider unavailable")
	reads := 0
	fixture.hook = func(operation string) error {
		if operation == "get" {
			reads++
			if reads == 2 {
				return failure
			}
		}
		return nil
	}
	before, expected, err := store.ReadBackup(t.Context(), name)
	var classified *core.CredentialStoreError
	if before != nil || expected != nil || !errors.Is(err, failure) || errors.Is(err, core.ErrCredentialRecoveryInvalid) || !errors.As(err, &classified) || classified.Attempted {
		t.Fatal("native piece read failure became corrupt data or lost its provider cause")
	}
}
