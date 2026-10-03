// Package config resolves the CLI's settings from layered sources and
// records where each value came from.
//
// Layers apply lowest to highest: built-in defaults, the TOML config
// file, the JIRA_* environment, and eventually flags. Provenance is
// kept because "why is this the value?" is the question a surprising
// setting actually raises, and a merged map cannot answer it.
//
// Loading never validates. A half-configured state must be inspectable,
// and a command needing no Jira client must not fail for want of
// credentials — so Config.Validate runs where the values are used, not
// where they are read.
//
// The optional [aliases] table contains literal command expansion strings.
// It is file-only: JIRA_ALIASES does not overlay it. AliasFile provides a
// validated snapshot for local updates, preserving declared settings without
// adding resolved defaults, environment settings or stored credentials.
// Saves follow existing config symlinks, normalize TOML formatting and refuse
// a detected stale snapshot; they are not compare-and-swap filesystem writes.
package config
