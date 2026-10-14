package nativepath

import "testing"

func TestWindowsPathSpellingPreservesIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, native, want string
	}{
		{"drive", `\\?\C:\Users\owned\settings.toml`, `C:\Users\owned\settings.toml`},
		{"drive root", `\\?\C:\`, `C:\`},
		{"UNC", `\\?\UNC\server\share\settings.toml`, `\\server\share\settings.toml`},
		{"UNC root", `\\?\UNC\server\share\`, `\\server\share\`},
		{"Unicode", `\\?\C:\古い\é.toml`, `C:\古い\é.toml`},
		{"volume GUID", `\\?\Volume{00000000-0000-0000-0000-000000000000}\settings.toml`, `\\?\Volume{00000000-0000-0000-0000-000000000000}\settings.toml`},
		{"trailing dot", `\\?\C:\literal.\settings.toml`, `\\?\C:\literal.\settings.toml`},
		{"trailing space", `\\?\C:\literal \settings.toml`, `\\?\C:\literal \settings.toml`},
		{"device name", `\\?\C:\NUL\settings.toml`, `\\?\C:\NUL\settings.toml`},
		{"data stream", `\\?\C:\settings.toml:stream`, `\\?\C:\settings.toml:stream`},
		{"ordinary", `C:\Users\owned\settings.toml`, `C:\Users\owned\settings.toml`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathSpelling(tc.native); got != tc.want {
				t.Fatalf("native path identity changed: got=%q want=%q", got, tc.want)
			}
		})
	}
}
