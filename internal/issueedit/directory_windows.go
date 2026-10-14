package issueedit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/internal/core"
)

func draftEntry(parent *os.Root, name, path string) (os.FileInfo, error) {
	if literalDirectoryLeaf(name) {
		// Root.Lstat cleans its relative name through GetFullPathName, which
		// strips a final dot/space. Inspect this exact extended native name
		// only after checking its canonical parent against the rooted handle.
		if err := checkDirectory(parent, filepath.Dir(path)); err != nil {
			return nil, err
		}
		return os.Lstat(path)
	}
	return parent.Lstat(name)
}

func createDraftDirectory(ctx context.Context, parent *os.Root, name, path string) (root *os.Root, created bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !literalDirectoryLeaf(name) {
		if err := parent.Mkdir(name, 0o700); err != nil {
			return nil, false, err
		}
		root, err := parent.OpenRoot(name)
		return root, true, err
	}
	// os.Root normalizes relative names even when its own path is extended.
	// The NT call below creates exactly one literal leaf through its owned
	// parent handle; it never falls back to a pathname-based write.
	if name == "." || name == ".." || strings.ContainsAny(name, `\/:`+"\x00") || !utf8.ValidString(name) {
		return nil, false, core.ErrIssueEditDraftInvalid
	}
	defer func() {
		if err != nil && root != nil {
			err = errors.Join(err, root.Close())
			root = nil
		}
	}()
	parentFile, err := parent.Open(".")
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, parentFile.Close()) }()
	connection, err := parentFile.SyscallConn()
	if err != nil {
		return nil, false, err
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var handle windows.Handle
	var nativeErr error
	controlErr := connection.Control(func(parentHandle uintptr) {
		attributes := windows.OBJECT_ATTRIBUTES{
			RootDirectory: windows.Handle(parentHandle), ObjectName: objectName,
			Attributes: windows.OBJ_CASE_INSENSITIVE,
		}
		// The API requires its architecture-specific ABI size. Sizeof is a
		// compile-time constant; no unsafe pointer conversion is performed.
		attributes.Length = uint32(unsafe.Sizeof(attributes))
		nativeErr = windows.NtCreateFile(
			&handle,
			windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
			&attributes,
			&windows.IO_STATUS_BLOCK{},
			nil,
			windows.FILE_ATTRIBUTE_NORMAL,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			windows.FILE_CREATE,
			windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			0,
			0,
		)
		created = nativeErr == nil
	})
	if nativeErr != nil {
		var status windows.NTStatus
		if errors.As(nativeErr, &status) {
			nativeErr = errors.Join(status.Errno(), nativeErr)
		}
	}
	if err := errors.Join(controlErr, nativeErr); err != nil {
		if created {
			err = errors.Join(err, windows.CloseHandle(handle))
		}
		return nil, created, err
	}
	createdFile := os.NewFile(uintptr(handle), path)
	if createdFile == nil {
		return nil, true, errors.Join(os.ErrInvalid, windows.CloseHandle(handle))
	}
	defer func() { err = errors.Join(err, createdFile.Close()) }()
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	// There is no public os.Root constructor for an existing handle. Reopen
	// without writing, while the created handle prevents directory deletion,
	// and compare identities before any member can be published.
	root, err = os.OpenRoot(path)
	if err != nil {
		return nil, true, err
	}
	actual, statErr := createdFile.Stat()
	opened, rootErr := root.Stat(".")
	if statErr != nil || rootErr != nil || !os.SameFile(actual, opened) {
		return root, true, errors.Join(core.ErrIssueEditDraftChanged, statErr, rootErr)
	}
	return root, true, ctx.Err()
}

func literalDirectoryLeaf(name string) bool {
	return strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ")
}
