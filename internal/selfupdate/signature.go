package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

const maxSignatureBytes int64 = 1 << 20

// trustTransport binds the context-free TUF fetcher to this invocation. Neither
// gechr/x/http nor Sigstore's fetcher provides a context-aware transport adapter.
type trustTransport func(*http.Request) (*http.Response, error)

func (transport trustTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func releaseTrustedRoot(ctx context.Context, transport http.RoundTripper) (*root.TrustedRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: updateTimeout,
		Transport: trustTransport(func(request *http.Request) (*http.Response, error) {
			// Root metadata comes only from the fixed Sigstore TUF repository.
			// The library validates TUF signatures and metadata/target sizes.
			if request.URL.Scheme != "https" || request.URL.Host != "tuf-repo-cdn.sigstore.dev" || request.URL.User != nil {
				return nil, fmt.Errorf("invalid signature trust repository URL")
			}
			bound := request.Clone(ctx)
			bound.Header.Del("Authorization")
			bound.Header.Del("Cookie")
			return transport.RoundTrip(bound)
		}),
	}
	downloader := fetcher.NewDefaultFetcher()
	downloader.SetHTTPClient(client)
	options := tuf.DefaultOptions().WithDisableLocalCache().WithFetcher(downloader)
	trusted, err := root.FetchTrustedRootWithOptions(options)
	if err != nil {
		return nil, err
	}
	return trusted, ctx.Err()
}

func verifyReleaseSignature(
	ctx context.Context,
	transport http.RoundTripper,
	version string,
	manifest, signature []byte,
) error {
	trusted, err := releaseTrustedRoot(ctx, transport)
	if err != nil {
		return fmt.Errorf("%w: load signature trust: %w", ErrIntegrity, err)
	}
	if err := verifySignedManifest(trusted, version, manifest, signature); err != nil {
		return fmt.Errorf("%w: verify checksum signature: %w", ErrIntegrity, err)
	}
	return ctx.Err()
}

func verifySignedManifest(trusted root.TrustedMaterial, version string, manifest, signature []byte) error {
	var signed bundle.Bundle
	if err := json.Unmarshal(signature, &signed); err != nil {
		return err
	}
	verifier, err := verify.NewVerifier(
		trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return err
	}
	identity, err := verify.NewShortCertificateIdentity(
		"https://token.actions.githubusercontent.com",
		"",
		"https://"+Module+"/.github/workflows/release.yml@refs/tags/"+version,
		"",
	)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(manifest)
	_, err = verifier.Verify(&signed, verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digest[:]),
		verify.WithCertificateIdentity(identity),
	))
	return err
}
