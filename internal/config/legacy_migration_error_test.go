package config

import (
	"errors"
	"strings"
	"testing"
)

func TestLegacyConfigFailureKeepsPublicErrorsSafeAndNativeCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("parser rejected synthetic-private-material")
	err := &legacyConfigFailure{cause: cause}
	if strings.Contains(err.Error(), "synthetic-private-material") || err.Error() != ErrLegacyConfig.Error() {
		t.Fatal("legacy parser details became a public error")
	}
	if !errors.Is(err, cause) || !errors.Is(err, ErrLegacyConfig) {
		t.Fatal("sanitization discarded native failure identity")
	}
}
