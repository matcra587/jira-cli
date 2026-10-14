//go:build windows && (nativefilesystem || nativeconsole)

// Package testfixture provides native fixtures shared by acceptance tests.
package testfixture

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// Junction creates an owned native directory junction and verifies its tag.
// The caller owns both paths; cleanup removes only the junction itself.
func Junction(t *testing.T, path, target string) os.FileInfo {
	t.Helper()
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	script := "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; " +
		"New-Item -ItemType Junction -Path " + quote(path) + " -Value " + quote(target) + " | Out-Null"
	units := utf16.Encode([]rune(script))
	encoded := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[2*i:], unit)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded)) //nolint:gosec // G204: fixed fixture command; owned path literals are quoted and escaped before encoding.
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native junction creation failed: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove owned junction: %v", err)
		}
	})
	nativePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(nativePath, &data)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.FindClose(handle); err != nil {
		t.Fatal(err)
	}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || data.Reserved0 != windows.IO_REPARSE_TAG_MOUNT_POINT {
		t.Fatalf("fixture is not a native junction: attributes=%x tag=%x", data.FileAttributes, data.Reserved0)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	SameJunction(t, path, target, info)
	return info
}

// SameJunction requires unchanged link identity and the expected target.
func SameJunction(t *testing.T, path, target string, before os.FileInfo) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("junction identity changed: %v", err)
	}
	linked, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := os.Stat(target)
	if err != nil || !os.SameFile(linked, wanted) {
		t.Fatalf("junction no longer reaches its owned target: %v", err)
	}
}
