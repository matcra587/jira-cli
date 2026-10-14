// Package nativepath preserves literal filesystem identity across native path
// resolution. Callers retain domain-specific existence and publication policy.
package nativepath

import "errors"

// ErrMissing marks absence reported before a native object could be opened.
// Resolution or cleanup failures after opening an object never carry this marker.
var ErrMissing = errors.New("selected native path does not exist")
