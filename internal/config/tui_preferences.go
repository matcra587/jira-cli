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

func prepareTUIPreferenceEnvironment(layer *koanf.Koanf) error {
	if !layer.Exists("tui_preview_size") {
		return nil
	}
	size, err := parseTUIPreviewSize(layer.String("tui_preview_size"))
	if err != nil {
		return err
	}
	return layer.Set("tui_preview_size", size)
}
