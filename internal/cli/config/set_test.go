package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
)

func TestConfigInputErrorsKeepClassificationAndNativeCauses(t *testing.T) {
	t.Parallel()
	native := &os.PathError{Op: "open", Path: "settings.toml", Err: os.ErrPermission}
	for _, tc := range []struct {
		name  string
		cause error
		code  string
	}{
		{name: "invalid_input", cause: fmt.Errorf("read refused: %w", cfg.ErrFileInvalid), code: cli.CodeConfigInvalid},
		{name: "native_failure", cause: native, code: cli.CodeLocalIO},
		{name: "cancellation", cause: context.Canceled, code: cli.CodeCanceled},
		{name: "deadline", cause: context.DeadlineExceeded, code: "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := localIO("read config", tc.cause)
			if got := cli.MapError(err); got.Code != tc.code || !errors.Is(err, tc.cause) {
				t.Fatalf("input failure lost its category or cause: classification=%+v err=%v", got, err)
			}
			if errors.Is(tc.cause, native) {
				var got *os.PathError
				if !errors.As(err, &got) || got != native {
					t.Fatalf("input failure erased native filesystem details: %v", err)
				}
			}
		})
	}
}

func TestSettingPublicationErrorsKeepActionableClassificationAndContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cause error
		code  string
	}{
		{cfg.ErrFileChanged, cli.CodeConfigChanged},
		{cfg.ErrFileInvalid, cli.CodeConfigInvalid},
		{context.Canceled, cli.CodeCanceled},
		{context.DeadlineExceeded, "timeout"},
	} {
		err := settingFileError(fmt.Errorf("publication refused: %w", tc.cause))
		if classified := cli.MapError(err); classified.Code != tc.code {
			t.Fatalf("publication failure lost its classification: %+v", classified)
		}
		if (errors.Is(tc.cause, context.Canceled) || errors.Is(tc.cause, context.DeadlineExceeded)) && !errors.Is(err, tc.cause) {
			t.Fatalf("publication failure erased its context cause: %v", err)
		}
	}
}
