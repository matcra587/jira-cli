package config

import (
	"context"
	"fmt"
	"maps"

	koanftoml "github.com/knadh/koanf/parsers/toml"
)

// AliasFile is a validated file-only snapshot for an alias update. It never
// loads credentials or environment settings. Saves normalize TOML formatting;
// an unchanged alias map leaves the original bytes and metadata untouched.
// Reopen after a save: the snapshot continues to represent the original file.
type AliasFile struct {
	file    *File
	fields  map[string]any
	aliases map[string]string
}

// OpenAliasFile reads and strictly validates the selected config file without
// resolving effective settings. A missing file is an empty update target, even
// when explicitly selected, so the first alias can create it.
func OpenAliasFile(ctx context.Context, path string) (*AliasFile, error) {
	file, err := OpenFile(ctx, path)
	if err != nil {
		return nil, err
	}
	fields, decoded, err := decodeFile(file.original)
	if err != nil {
		return nil, err
	}
	return &AliasFile{file: file, fields: fields, aliases: maps.Clone(decoded.Aliases)}, nil
}

// Path is the resolved file that an update would replace.
func (f *AliasFile) Path() string { return f.file.Path() }

// Aliases returns a caller-owned copy; editing it cannot change the snapshot.
func (f *AliasFile) Aliases() map[string]string {
	if f.aliases == nil {
		return map[string]string{}
	}
	return maps.Clone(f.aliases)
}

// SaveAliases changes only the file's alias table. It preserves declared
// settings without adding defaults, environment values or keyring records.
// Snapshot refusal and atomic replacement follow File.Save's contract.
func (f *AliasFile) SaveAliases(ctx context.Context, aliases map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if maps.Equal(f.aliases, aliases) {
		return false, nil
	}
	fields := maps.Clone(f.fields)
	stored := make(map[string]string, len(aliases))
	maps.Copy(stored, aliases)
	fields["aliases"] = stored
	data, err := koanftoml.Parser().Marshal(fields)
	if err != nil {
		return false, fmt.Errorf("encode config: %w", err)
	}
	return f.file.Save(ctx, data)
}
