//go:build outputaudit

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/gechr/x/shell"
)

const (
	auditEditorReceiptEnv = "JIRA_AUDIT_EDITOR_RECEIPT"
	auditEditorLiteral    = "literal spaces 古い & $ ;"
	auditEditorLimit      = 1 << 20
)

type auditEditorReceipt struct {
	Argv   []string `json:"argv"`
	SHA256 string   `json:"sha256"`
}

// The test executable dispatches this fixture before testing's flag parser.
// It adds no shell dependency, compiler invocation or test-runner output.
func auditEditor(workdir string) (command, receipt string, err error) {
	binary, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	return shell.Quote(binary) + " --audit-editor " + shell.Quote(auditEditorLiteral), filepath.Join(workdir, "audit-editor-receipt.json"), nil
}

func runAuditEditor(args []string) (err error) {
	if len(args) != 2 || args[0] != auditEditorLiteral {
		return errors.New("audit editor requires its literal argument and one file")
	}
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(workdir, args[1])
	if err != nil || !filepath.IsLocal(relative) {
		return errors.New("audit editor input must belong to its working directory")
	}
	root, err := os.OpenRoot(workdir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, err := root.Open(relative)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.Join(errors.New("audit editor input must be a regular file"), err, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, auditEditorLimit+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return err
	}
	if len(data) > auditEditorLimit {
		return errors.New("audit editor input exceeds fixture bound")
	}
	receiptPath := os.Getenv(auditEditorReceiptEnv)
	if receiptPath != filepath.Join(workdir, "audit-editor-receipt.json") {
		return errors.New("audit editor receipt must belong to its working directory")
	}
	encoded, err := json.Marshal(auditEditorReceipt{Argv: slices.Clone(args), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	if err != nil {
		return err
	}
	receipt, err := root.OpenFile("audit-editor-receipt.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("audit editor receipt already exists: %w", err)
		}
		return err
	}
	_, writeErr := receipt.Write(encoded)
	return errors.Join(writeErr, receipt.Close())
}

func checkAuditEditorReceipt(path string, expected bool) error {
	encoded, err := os.ReadFile(path)
	if !expected {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect audit editor receipt: %w", err)
		}
		return errors.New("refusal or preview entered the audit editor")
	}
	if err != nil {
		return fmt.Errorf("audit editor did not publish its receipt: %w", err)
	}
	var receipt auditEditorReceipt
	if err := json.Unmarshal(encoded, &receipt); err != nil {
		return err
	}
	digest, err := hex.DecodeString(receipt.SHA256)
	if len(receipt.Argv) != 2 || receipt.Argv[0] != auditEditorLiteral || err != nil || len(digest) != sha256.Size {
		return errors.New("audit editor receipt has invalid arguments or digest")
	}
	relative, err := filepath.Rel(filepath.Dir(path), receipt.Argv[1])
	if err != nil || !filepath.IsLocal(relative) {
		return errors.New("audit editor receipt input escapes its working directory")
	}
	return nil
}
