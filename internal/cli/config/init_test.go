package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestConfigInitRejectsInvalidUTF8BeforeFilesystemOnAllPlatforms(t *testing.T) {
	for _, tc := range []struct {
		name, site, email, field string
	}{
		{name: "email", site: "https://example.invalid", email: "\xff", field: "email"},
		{name: "site", site: "https://example.invalid/\xff", email: "person@example.invalid", field: "base-url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "unstarted")
			cfg.SetFlagLayer(cfg.FlagLayer{Path: filepath.Join(parent, "settings.toml")})
			t.Cleanup(func() { cfg.SetFlagLayer(cfg.FlagLayer{}) })
			created := false
			cmd := newInitWithCreator(func(context.Context, *cfg.SettingsUpdate) (bool, error) {
				created = true
				return false, errors.New("invalid initialization reached publication")
			})
			if err := cmd.ParseFlags([]string{"--base-url=" + tc.site, "--email=" + tc.email}); err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(t.Context())
			err := cmd.RunE(cmd, nil)
			var input *cli.InputError
			if created || !errors.As(err, &input) || input.Field != tc.field || cli.MapError(err).Code != cli.CodeArgValueInvalid {
				t.Fatalf("invalid raw bytes lost pre-I/O refusal: creator=%v error=%v", created, err)
			}
			if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid raw bytes touched the destination", err)
			}
		})
	}
}

func TestConfigInitPublicationErrorRetainsOnlyCompletedReceipt(t *testing.T) {
	for _, publish := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		cfg.SetFlagLayer(cfg.FlagLayer{Path: path})
		t.Cleanup(func() { cfg.SetFlagLayer(cfg.FlagLayer{}) })
		cause := errors.New("injected publication durability error")
		cmd := newInitWithCreator(func(ctx context.Context, update *cfg.SettingsUpdate) (bool, error) {
			if !publish {
				return false, cause
			}
			created, err := update.Create(ctx)
			if err != nil || !created {
				t.Fatalf("owned publication did not finish before simulated durability failure: %v %v", created, err)
			}
			return created, cause
		})
		if err := cmd.ParseFlags([]string{"--base-url=https://example.invalid", "--email=person@example.invalid"}); err != nil {
			t.Fatal(err)
		}
		cmd.SetContext(t.Context())
		err := cmd.RunE(cmd, nil)
		if !errors.Is(err, cause) || cli.MapError(err).Code != cli.CodeLocalIO {
			t.Fatalf("initialization rebuilt the cause or lost its storage taxonomy: %v", err)
		}
		var retained *cli.ResultError
		if errors.As(err, &retained) != publish {
			t.Fatalf("failure receipt does not match actual publication: publish=%v err=%v", publish, err)
		}
		if !publish {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pre-publication refusal created its destination: %v", err)
			}
			continue
		}
		out, ok := retained.Data.(envelope.ConfigInitOutput)
		if !ok || !out.Created || !out.WouldCreate || out.DryRun || out.Path != path || out.BaseURL != "https://example.invalid" || out.Email != "person@example.invalid" || retained.Op != "config.init" || !retained.Mutation || retained.Human == nil {
			t.Fatalf("published failure lost its typed receipt: %+v", retained)
		}
		mapped := cli.MapError(err)
		result := output.Assemble(output.Inputs{
			Op: retained.Op, Data: retained.Data, Human: retained.Human, ColorMode: clog.ColorNever,
			Errors:   []envelope.Error{{Code: mapped.Code, Message: mapped.Message, Hint: mapped.Hint}},
			ExitCode: mapped.Exit, Now: time.Unix(0, 0), RequestID: "00000000000000000000000000000000",
		})
		for _, mode := range []output.Format{output.FormatJSON, output.FormatCompact, output.FormatHuman} {
			var stdout bytes.Buffer
			var err error
			if mode == output.FormatHuman {
				err = output.RenderFailureReceipt(t.Context(), &stdout, result)
			} else {
				err = output.Render(t.Context(), &stdout, mode, result)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == output.FormatHuman {
				if !strings.Contains(stdout.String(), "created config") || !strings.Contains(stdout.String(), path) {
					t.Fatalf("human failure hid its completed publication: %q", stdout.String())
				}
				continue
			}
			var frame struct {
				OK   bool                      `json:"ok"`
				Data envelope.ConfigInitOutput `json:"data"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || frame.OK || !frame.Data.Created || frame.Data.Path != path {
				t.Fatalf("machine failure discarded completed publication: %q", stdout.String())
			}
		}
		file, err := cfg.OpenFile(t.Context(), path)
		if err != nil || !file.Exists() {
			t.Fatalf("error receipt names no actual published file: %v", err)
		}
	}
}
