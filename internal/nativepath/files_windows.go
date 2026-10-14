package nativepath

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FilePath keeps the extended syntax needed to reopen a literal filename.
func FilePath(directory, name string) string {
	path := filepath.Join(directory, name)
	if literalFileLeaf(name) && !strings.HasPrefix(path, `\\?\`) {
		return extendedPath(path)
	}
	return path
}

// CreateFile opens a rooted file exclusively, or truncates it when requested.
// Literal native leaves use the retained directory handle without Win32 cleanup.
func CreateFile(root *os.Root, name string, overwrite bool, mode os.FileMode) (*os.File, error) {
	if !literalFileLeaf(name) {
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if overwrite {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		return root.OpenFile(name, flags, mode)
	}
	disposition := uint32(windows.FILE_CREATE)
	if overwrite {
		disposition = windows.FILE_OPEN_IF
	}
	attributes := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if mode&0o200 == 0 {
		attributes = windows.FILE_ATTRIBUTE_READONLY
	}
	file, err := openLiteralFile(root, name, windows.FILE_GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES,
		disposition, windows.FILE_NON_DIRECTORY_FILE, attributes, true)
	if err != nil {
		return nil, err
	}
	if overwrite {
		if err := file.Truncate(0); err != nil {
			file.Close() //nolint:errcheck // preserve the failed truncation
			return nil, err
		}
	}
	return file, nil
}

// Stat returns rooted metadata. Literal reparse entries refuse traversal.
func Stat(root *os.Root, name string) (os.FileInfo, error) {
	if !literalFileLeaf(name) {
		return root.Stat(name)
	}
	return statLiteralFile(root, name, true)
}

// Lstat returns rooted metadata without following the final link.
func Lstat(root *os.Root, name string) (os.FileInfo, error) {
	if !literalFileLeaf(name) {
		return root.Lstat(name)
	}
	return statLiteralFile(root, name, false)
}

func statLiteralFile(root *os.Root, name string, refuseReparse bool) (os.FileInfo, error) {
	file, err := openLiteralFile(root, name, windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, 0, 0, refuseReparse)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // metadata reads have no buffered writes
	return file.Stat()
}

// Remove removes one rooted file entry without normalizing its literal name.
func Remove(root *os.Root, name string) error {
	if !literalFileLeaf(name) {
		return root.Remove(name)
	}
	file, err := openLiteralFile(root, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, 0, false)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck // deletion is acknowledged by the native information call
	flags := make([]byte, 4)
	binary.LittleEndian.PutUint32(flags, windows.FILE_DISPOSITION_DELETE|windows.FILE_DISPOSITION_POSIX_SEMANTICS|windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE)
	err = windows.NtSetInformationFile(windows.Handle(file.Fd()), &windows.IO_STATUS_BLOCK{}, &flags[0], uint32(len(flags)), windows.FileDispositionInformationEx)
	if errors.Is(err, windows.STATUS_INVALID_INFO_CLASS) || errors.Is(err, windows.STATUS_INVALID_PARAMETER) || errors.Is(err, windows.STATUS_NOT_SUPPORTED) {
		flags[0] = 1
		err = windows.NtSetInformationFile(windows.Handle(file.Fd()), &windows.IO_STATUS_BLOCK{}, &flags[0], 1, windows.FileDispositionInformation)
	}
	return literalFileError("remove rooted file", name, err)
}

// Rename replaces a rooted entry without normalizing either literal filename.
func Rename(root *os.Root, oldname, newname string) error {
	if !literalFileLeaf(oldname) && !literalFileLeaf(newname) {
		return root.Rename(oldname, newname)
	}
	if err := validFileLeaf(newname); err != nil {
		return literalFileError("rename rooted file", newname, err)
	}
	file, err := openLiteralFile(root, oldname, windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, 0, true)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck // the staged file was synced and closed before publication
	encoded, err := windows.UTF16FromString(newname)
	if err != nil {
		return err
	}
	return withRootDirectory(root, func(parent windows.Handle) error {
		var layout struct {
			flags  uint32
			parent windows.Handle
			length uint32
			name   [1]uint16
		}
		// Only ABI offsets/sizes use unsafe. The variable-length buffer is
		// encoded explicitly; no pointer is cast into a fabricated array.
		offset := int(unsafe.Offsetof(layout.name))
		length := (len(encoded) - 1) * 2
		buffer := make([]byte, max(int(unsafe.Sizeof(layout)), offset+length))
		parentOffset := int(unsafe.Offsetof(layout.parent))
		if unsafe.Sizeof(parent) == 8 {
			binary.LittleEndian.PutUint64(buffer[parentOffset:], uint64(parent))
		} else {
			binary.LittleEndian.PutUint32(buffer[parentOffset:], uint32(parent))
		}
		binary.LittleEndian.PutUint32(buffer[int(unsafe.Offsetof(layout.length)):], uint32(length))
		for i, value := range encoded[:len(encoded)-1] {
			binary.LittleEndian.PutUint16(buffer[offset+i*2:], value)
		}
		binary.LittleEndian.PutUint32(buffer, windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS)
		const fileRenameInformationEx = 65
		err := windows.NtSetInformationFile(windows.Handle(file.Fd()), &windows.IO_STATUS_BLOCK{}, &buffer[0], uint32(len(buffer)), fileRenameInformationEx)
		if err != nil {
			// Match os.Root's fallback for filesystems without the extended
			// rename information class or POSIX replacement support.
			binary.LittleEndian.PutUint32(buffer, windows.FILE_RENAME_REPLACE_IF_EXISTS)
			err = windows.NtSetInformationFile(windows.Handle(file.Fd()), &windows.IO_STATUS_BLOCK{}, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
		}
		return literalFileError("rename rooted file", newname, err)
	})
}

func openLiteralFile(root *os.Root, name string, access, disposition, options, attributes uint32, refuseReparse bool) (*os.File, error) {
	if err := validFileLeaf(name); err != nil {
		return nil, literalFileError("open rooted file", name, err)
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	var handle windows.Handle
	err = withRootDirectory(root, func(parent windows.Handle) error {
		object := windows.OBJECT_ATTRIBUTES{
			RootDirectory: parent, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE,
		}
		if refuseReparse {
			object.Attributes |= windows.OBJ_DONT_REPARSE
		}
		object.Length = uint32(unsafe.Sizeof(object))
		return windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &object, &windows.IO_STATUS_BLOCK{},
			nil, attributes, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, disposition,
			options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	})
	if err != nil {
		return nil, literalFileError("open rooted file", name, err)
	}
	// OPEN_REPARSE_POINT opens the entry itself; OBJ_DONT_REPARSE does not
	// necessarily refuse that successful open. Inspect the actual handle
	// before truncation or treating its metadata as a followed regular file.
	// FILE_CREATE is exclusive, so it cannot admit an existing reparse entry.
	if refuseReparse && disposition != windows.FILE_CREATE {
		var info windows.ByHandleFileInformation
		checkErr := windows.GetFileInformationByHandle(handle, &info)
		if checkErr == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			checkErr = windows.ERROR_CANT_ACCESS_FILE
		}
		if checkErr != nil {
			return nil, literalFileError("inspect rooted file", name, errors.Join(checkErr, windows.CloseHandle(handle)))
		}
	}
	file := os.NewFile(uintptr(handle), FilePath(root.Name(), name))
	if file == nil {
		windows.CloseHandle(handle) //nolint:errcheck // invalid file wrapper; release the native handle
		return nil, os.ErrInvalid
	}
	return file, nil
}

func withRootDirectory(root *os.Root, operation func(windows.Handle) error) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close() //nolint:errcheck // read-only borrowed-root view has no buffered writes
	connection, err := directory.SyscallConn()
	if err != nil {
		return err
	}
	var nativeErr error
	controlErr := connection.Control(func(handle uintptr) { nativeErr = operation(windows.Handle(handle)) })
	return errors.Join(controlErr, nativeErr)
}

func literalFileLeaf(name string) bool {
	return strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ")
}

func validFileLeaf(name string) error {
	if name == "." || name == ".." || !filepath.IsLocal(name) || strings.ContainsAny(name, `\/:*?"<>|`+"\x00") || !utf8.ValidString(name) {
		return os.ErrInvalid
	}
	return nil
}

func literalFileError(operation, name string, err error) error {
	if err == nil {
		return nil
	}
	var status windows.NTStatus
	if errors.As(err, &status) {
		err = errors.Join(status.Errno(), err)
	}
	return &os.PathError{Op: operation, Path: name, Err: err}
}
