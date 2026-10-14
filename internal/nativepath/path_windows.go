package nativepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Abs returns an absolute path without reinterpreting extended literal names.
func Abs(path string) (string, error) {
	// GetFullPathName (used by filepath.Abs) strips literal trailing dots and
	// spaces even from an extended absolute name. Keep its selected identity.
	if strings.HasPrefix(path, `\\?\`) && filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(path)
}

// ResolveExisting resolves an existing absolute object through a metadata handle.
// It follows junctions as well as symlinks, preserves literal native names and
// returns missing-path errors to the domain that owns creation policy.
func ResolveExisting(path string) (resolved string, err error) {
	openPath := path
	// Match Go's long-path boundary without depending on machine policy. The
	// shared owner already supplied an absolute, cleaned path.
	if len(openPath) >= windows.MAX_PATH-12 && !strings.HasPrefix(openPath, `\\?\`) && !strings.HasPrefix(openPath, `\??\`) {
		openPath = extendedPath(openPath)
	}
	nativePath, err := windows.UTF16PtrFromString(openPath)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(nativePath, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return "", errors.Join(ErrMissing, &os.PathError{Op: "open path metadata", Path: path, Err: err})
		}
		return "", &os.PathError{Op: "open path metadata", Path: path, Err: err}
	}
	defer func() {
		if closeErr := windows.CloseHandle(handle); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close path metadata: %w", closeErr))
		}
	}()
	buffer := make([]uint16, windows.MAX_PATH+1)
	var flags uint32
	for {
		size, finalErr := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), flags)
		if errors.Is(finalErr, windows.ERROR_PATH_NOT_FOUND) && flags == 0 {
			// A mounted volume need not have a DOS drive letter. Preserve a
			// usable volume-GUID path instead of rejecting that existing object.
			flags = 0x1 // VOLUME_NAME_GUID
			continue
		}
		if finalErr != nil {
			return "", &os.PathError{Op: "resolve path metadata", Path: path, Err: finalErr}
		}
		if size < uint32(len(buffer)) {
			return pathSpelling(windows.UTF16ToString(buffer[:size])), nil
		}
		if size >= 1<<15 {
			return "", windows.ERROR_FILENAME_EXCED_RANGE
		}
		buffer = make([]uint16, size+1)
	}
}

// JoinLeaf appends the selected final component to its resolved parent,
// preserving required extended syntax and stable ordinary spelling.
func JoinLeaf(parent, path string) string {
	if strings.HasPrefix(path, `\\?\`) && !strings.HasPrefix(parent, `\\?\`) {
		parent = extendedPath(parent)
	}
	return pathSpelling(filepath.Join(parent, filepath.Base(path)))
}

func extendedPath(path string) string {
	if unc, ok := strings.CutPrefix(path, `\\`); ok {
		return `\\?\UNC\` + unc
	}
	return `\\?\` + path
}

// Ordinary DOS/UNC names retain their public spelling. Keep extended syntax
// where removing it would reinterpret a literal name, device or data stream.
func pathSpelling(path string) string {
	plain, extended := strings.CutPrefix(path, `\\?\`)
	if !extended {
		return path
	}
	if unc, ok := strings.CutPrefix(plain, `UNC\`); ok {
		plain = `\\` + unc
	} else if len(plain) < 3 || plain[1] != ':' || plain[2] != '\\' {
		return path
	}
	relative := strings.TrimLeft(strings.TrimPrefix(plain, filepath.VolumeName(plain)), `\`)
	if relative != "" && !filepath.IsLocal(relative) {
		return path
	}
	for component := range strings.SplitSeq(plain, `\`) {
		if strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
			return path
		}
	}
	return plain
}
