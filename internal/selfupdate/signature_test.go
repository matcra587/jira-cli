package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
)

func TestPublishedChecksumSignature(t *testing.T) {
	t.Parallel()
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile("testdata/signature/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	trusted, err := root.NewTrustedRootFromJSON(read("trusted_root.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, signature := read("checksums.txt"), read("checksums.txt.sigstore.json")
	for _, test := range []struct {
		name      string
		version   string
		manifest  []byte
		signature []byte
		valid     bool
	}{
		{name: "published", version: "v0.16.1", manifest: manifest, signature: signature, valid: true},
		{name: "changed manifest", version: "v0.16.1", manifest: append(bytes.Clone(manifest), '\n'), signature: signature},
		{name: "different tag", version: "v0.16.2", manifest: manifest, signature: signature},
		{name: "no bundle", version: "v0.16.1", manifest: manifest},
		{name: "invalid bundle", version: "v0.16.1", manifest: manifest, signature: []byte(`{}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := verifySignedManifest(trusted, test.version, test.manifest, test.signature)
			if (err == nil) != test.valid {
				t.Fatalf("signature accepted=%t, want %t: %v", err == nil, test.valid, err)
			}
		})
	}
}

func TestReleaseSignatureRefusalPrecedesArchiveDownload(t *testing.T) {
	fixture := newReleaseFixture(t, "linux", "amd64", tarFixture(t, archiveMember{name: "jira", body: "fixture"}))
	calls := 0
	fixture.source.verify = func(_ context.Context, version string, manifest, signature []byte) error {
		calls++
		if version != "v1.2.3" {
			t.Fatal("verification changed the frozen release version")
		}
		if !bytes.Equal(manifest, fixture.assets["11"]) || !bytes.Equal(signature, fixture.assets["12"]) {
			t.Fatal("verification did not bind the frozen release and exact manifest")
		}
		return ErrIntegrity
	}
	release, err := fixture.source.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := fixture.source.Download(t.Context(), release, "linux", "amd64")
	refused := candidate == nil && errors.Is(err, ErrIntegrity)
	if !refused || calls != 1 {
		t.Fatal("signature refusal was lost", err, calls)
	}
	fixture.assertRequestCount(t, "/assets/10", 0)
	fixture.assertPrivateCleanup(t)
}

func TestSignatureTrustHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	transport := trustTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "tuf-repo-cdn.sigstore.dev" || request.Header.Get("Authorization") != "" {
			t.Fatal("trust request escaped fixed anonymous scope")
		}
		cancel()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	_, err := releaseTrustedRoot(ctx, transport)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("trust request did not honor cancellation: calls=%d err=%v", calls, err)
	}
}
