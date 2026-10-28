package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knadh/koanf/v2"
)

// PreviewDock chooses the preferred position of an enabled dashboard preview.
type PreviewDock string

const (
	// PreviewDockAuto prefers right on wide screens and bottom on narrower ones.
	PreviewDockAuto PreviewDock = "auto"
	// PreviewDockRight requests side-by-side panes when both fit.
	PreviewDockRight PreviewDock = "right"
	// PreviewDockBottom requests vertically stacked panes when both fit.
	PreviewDockBottom PreviewDock = "bottom"
	// DefaultTUIPreviewSize is the preview's preferred share of the split.
	DefaultTUIPreviewSize = 40
	// MinTUIPreviewSize is the smallest configured preview share.
	MinTUIPreviewSize = 25
	// MaxTUIPreviewSize is the largest configured preview share.
	MaxTUIPreviewSize = 75
)

// UnmarshalText validates the closed vocabulary before any configuration overlay.
func (d *PreviewDock) UnmarshalText(text []byte) error {
	value := PreviewDock(strings.ToLower(strings.TrimSpace(string(text))))
	switch value {
	case PreviewDockAuto, PreviewDockRight, PreviewDockBottom:
		*d = value
		return nil
	default:
		return fmt.Errorf("preview dock must be auto, right or bottom")
	}
}

func parseTUIPreviewSize(value string) (int64, error) {
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || size < MinTUIPreviewSize || size > MaxTUIPreviewSize {
		return 0, fmt.Errorf("tui_preview_size must be an integer percentage from %d to %d", MinTUIPreviewSize, MaxTUIPreviewSize)
	}
	return size, nil
}

// DefaultTUIRefreshInterval is the dashboard's timed read cadence in seconds.
const DefaultTUIRefreshInterval = 60

// MaxTUIRefreshInterval bounds both timer lifetime and duration conversion.
const MaxTUIRefreshInterval = 86400

func parseTUIRefreshInterval(value string) (int64, error) {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 || seconds > MaxTUIRefreshInterval {
		return 0, fmt.Errorf("tui_refresh_interval must be an integer from 0 to %d seconds; 0 disables timed refresh", MaxTUIRefreshInterval)
	}
	return seconds, nil
}

func prepareTUIPreferenceEnvironment(layer *koanf.Koanf) error {
	for _, preference := range []struct {
		key   string
		parse func(string) (int64, error)
	}{
		{key: "tui_preview_size", parse: parseTUIPreviewSize},
		{key: "tui_refresh_interval", parse: parseTUIRefreshInterval},
	} {
		if !layer.Exists(preference.key) {
			continue
		}
		value, err := preference.parse(layer.String(preference.key))
		if err != nil {
			return err
		}
		if err := layer.Set(preference.key, value); err != nil {
			return err
		}
	}
	return nil
}
