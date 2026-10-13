package keyring

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

const (
	windowsCredentialBlobLimit = 2560
	recoveryChunkBytes         = 1536 // Base64 occupies 2,048 native blob bytes.
	maxChunkedRecoveryBytes    = 1 << 20
)

type chunkedRecovery struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Bytes   int    `json:"bytes"`
	Parts   int    `json:"parts"`
	SHA256  string `json:"sha256"`
}

func recoveryChunkName(name string, index int) string {
	return fmt.Sprintf("%s.chunk-%03d", name, index)
}

// Windows admits each primary record independently but caps one credential blob.
// A recovery preimage/postimage pair can exceed that cap. Keep the existing v2
// payload intact inside bounded provider entries; publish its descriptor last.
// Neither native keyring nor gechr/x provides a multipart provider transaction.
func (s *Store) writeRecoveryBackup(ctx context.Context, name string, raw []byte, limit int) (bool, error) {
	if limit == 0 || len(raw) <= limit {
		err, attempted := s.set(ctx, name, string(raw))
		return attempted, err
	}
	if !core.ValidCredentialBackupID(name) || limit < base64.StdEncoding.EncodedLen(recoveryChunkBytes) || len(raw) > maxChunkedRecoveryBytes {
		return false, core.ErrCredentialRecoveryInvalid
	}
	parts := (len(raw) + recoveryChunkBytes - 1) / recoveryChunkBytes
	for i := range parts {
		_, err := s.Get(ctx, recoveryChunkName(name, i))
		if err == nil {
			return false, core.ErrCredentialBackupExists
		}
		if !errors.Is(err, core.ErrNoCredential) {
			return false, err
		}
	}
	digest := sha256.Sum256(raw)
	descriptor, err := jsonv2.Marshal(chunkedRecovery{Version: 3, Name: name, Bytes: len(raw), Parts: parts, SHA256: hex.EncodeToString(digest[:])})
	if err != nil || len(descriptor) > limit {
		return false, core.ErrCredentialRecoveryInvalid
	}
	entered := false
	for i := range parts {
		part := raw[i*recoveryChunkBytes : min((i+1)*recoveryChunkBytes, len(raw))]
		err, attempted := s.set(ctx, recoveryChunkName(name, i), base64.StdEncoding.EncodeToString(part))
		entered = entered || attempted
		if err != nil {
			return entered, err
		}
	}
	err, attempted := s.set(ctx, name, string(descriptor))
	return entered || attempted, err
}

func nativeRecoveryBlobLimit() int {
	if xos.IsWindows() {
		return windowsCredentialBlobLimit
	}
	return 0
}

func (s *Store) readRecoveryBackup(ctx context.Context, name, raw string) ([]byte, error) {
	var header struct {
		Version int `json:"version"`
	}
	if err := jsonv2.Unmarshal([]byte(raw), &header); err != nil {
		if header.Version == 3 {
			return nil, errors.Join(core.ErrCredentialRecoveryInvalid, err)
		}
		return nil, err
	}
	if header.Version != 3 {
		return []byte(raw), nil
	}
	var descriptor chunkedRecovery
	if err := jsonv2.Unmarshal([]byte(raw), &descriptor, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, errors.Join(core.ErrCredentialRecoveryInvalid, err)
	}
	if !core.ValidCredentialBackupID(name) || descriptor.Name != name || len(raw) > windowsCredentialBlobLimit || descriptor.Bytes <= windowsCredentialBlobLimit || descriptor.Bytes > maxChunkedRecoveryBytes || descriptor.Parts != (descriptor.Bytes+recoveryChunkBytes-1)/recoveryChunkBytes || len(descriptor.SHA256) != sha256.Size*2 {
		return nil, core.ErrCredentialRecoveryInvalid
	}
	payload := make([]byte, 0, descriptor.Bytes)
	for i := range descriptor.Parts {
		raw, err := s.Get(ctx, recoveryChunkName(name, i))
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, errors.Join(err, contextErr)
		}
		if err != nil {
			if errors.Is(err, core.ErrNoCredential) {
				return nil, errors.Join(core.ErrCredentialRecoveryInvalid, err)
			}
			return nil, err
		}
		want := min(recoveryChunkBytes, descriptor.Bytes-len(payload))
		if len(raw) != base64.StdEncoding.EncodedLen(want) || strings.ContainsAny(raw, "\r\n") {
			return nil, core.ErrCredentialRecoveryInvalid
		}
		part, err := base64.StdEncoding.Strict().DecodeString(raw)
		if err != nil || len(part) != want {
			return nil, core.ErrCredentialRecoveryInvalid
		}
		payload = append(payload, part...)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != descriptor.SHA256 {
		return nil, core.ErrCredentialRecoveryInvalid
	}
	return payload, nil
}
