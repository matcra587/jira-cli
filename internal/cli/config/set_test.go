package config

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
)

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
