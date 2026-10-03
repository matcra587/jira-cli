package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gechr/clive/version"
	xos "github.com/gechr/x/os"
	"github.com/google/go-github/v88/github"
	"golang.org/x/mod/semver"
)

const (
	maxReleaseMetadataBytes int64 = 1 << 20
	maxChecksumBytes        int64 = 1 << 20
	maxArchiveBytes         int64 = 256 << 20
	updateTimeout                 = 5 * time.Minute
)

var (
	// ErrReleaseInvalid means release metadata or its archive is unusable.
	ErrReleaseInvalid = errors.New("invalid update release")
	// ErrReleaseUnavailable means the repository has no published release.
	ErrReleaseUnavailable = errors.New("update release unavailable")
	// ErrIntegrity means a release asset did not match its checksum manifest.
	ErrIntegrity = errors.New("update integrity verification failed")
)

// Release is a value snapshot of a published release. Asset IDs and sizes stay
// private so Download uses the observed assets rather than resolving latest again.
type Release struct {
	ID      int64
	Version string
	assets  []releaseAsset
}

type releaseAsset struct {
	id    int64
	name  string
	size  int64
	state string
}

// GitHubSource reads this public repository anonymously, without constructing a
// Jira client or consulting gh credential files. Clive 0.4.9 exposes no bounded,
// quiet download seam and resolves latest again inside Update. Its selected
// go-github v88.0.0 SDK provides the API operations behind this narrow adapter;
// Clive still owns version comparison and the eventual notification contract.
type GitHubSource struct {
	api    *github.Client
	assets *http.Client
}

// NewGitHubSource constructs the release reader. An injected client's transport
// is trusted infrastructure; cookies and ambient GitHub authentication are not
// copied. Construction performs no network requests.
func NewGitHubSource(client *http.Client) (*GitHubSource, error) {
	transport := http.DefaultTransport
	timeout := updateTimeout
	if client != nil {
		if client.Transport != nil {
			transport = client.Transport
		}
		if client.Timeout > 0 {
			timeout = min(client.Timeout, updateTimeout)
		}
	}
	assets := &http.Client{
		Transport: releaseTransport{base: transport},
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) >= 10 {
				return fmt.Errorf("too many release asset redirects")
			}
			return validateReleaseURL(request.URL)
		},
	}
	apiClient := *assets
	apiClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	api, err := github.NewClient(github.WithHTTPClient(&apiClient))
	if err != nil {
		return nil, fmt.Errorf("construct release reader: %w", err)
	}
	return &GitHubSource{api: api, assets: assets}, nil
}

// Latest freezes the published stable release and its assets in one API read.
// A missing release or malformed version is an error, not an up-to-date result.
func (s *GitHubSource) Latest(ctx context.Context) (Release, error) {
	owner, repository := releaseRepository()
	native, response, err := s.api.Repositories.GetLatestRelease(ctx, owner, repository)
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return Release{}, errors.Join(ErrReleaseUnavailable, err)
		}
		return Release{}, fmt.Errorf("read latest release: %w", err)
	}
	if native == nil || native.GetID() <= 0 || native.GetDraft() || native.GetPrerelease() ||
		native.Draft == nil || native.Prerelease == nil || native.PublishedAt == nil || native.PublishedAt.IsZero() {
		return Release{}, fmt.Errorf("%w: expected a published stable release", ErrReleaseInvalid)
	}
	if err := validateVersion(native.GetTagName()); err != nil {
		return Release{}, err
	}
	// Clive deliberately strips -g suffixes for local development builds. A
	// release tag needs strict semver parsing so -gamma cannot become final.
	tag := version.AddPrefix(native.GetTagName())
	if !semver.IsValid(tag) || semver.Prerelease(tag) != "" || version.IsDev(native.GetTagName()) {
		return Release{}, fmt.Errorf("%w: stable release tag is not a final version", ErrReleaseInvalid)
	}
	out := Release{ID: native.GetID(), Version: native.GetTagName()}
	for _, asset := range native.Assets {
		if asset != nil {
			out.assets = append(out.assets, releaseAsset{
				id:    asset.GetID(),
				name:  asset.GetName(),
				size:  int64(asset.GetSize()),
				state: asset.GetState(),
			})
		}
	}
	return out, ctx.Err()
}

// Newer uses Clive's development-build ordering after validating both inputs.
// Invalid versions must never fabricate an already-current result.
func Newer(current, latest string) (bool, error) {
	if err := validateVersion(current); err != nil {
		return false, err
	}
	if err := validateVersion(latest); err != nil {
		return false, err
	}
	return version.CompareString(latest, current) > 0, nil
}

func validateVersion(value string) error {
	base := value
	if version.IsDev(base) {
		base = version.ExtractBase(base)
	}
	if value != strings.TrimSpace(value) || strings.ContainsAny(value, "/\\\x00\r\n") {
		return fmt.Errorf("%w: invalid version", ErrReleaseInvalid)
	}
	if _, err := version.Parse(base); err != nil {
		return fmt.Errorf("%w: invalid version: %w", ErrReleaseInvalid, err)
	}
	return nil
}

// ArchiveName matches the release packaging contract, including unsupported
// targets being refused rather than selecting a vaguely matching asset.
func (r Release) ArchiveName(goos, arch string) (string, error) {
	if err := validateVersion(r.Version); err != nil {
		return "", err
	}
	if arch != xos.ArchAMD64 && arch != xos.ArchARM64 {
		return "", fmt.Errorf("%w: unsupported update architecture", ErrReleaseInvalid)
	}
	extension := ".tar.gz"
	switch goos {
	case xos.PlatformLinux:
	case xos.PlatformDarwin:
		if arch != xos.ArchARM64 {
			return "", fmt.Errorf("%w: unsupported macOS update architecture", ErrReleaseInvalid)
		}
	case xos.PlatformWindows:
		extension = ".zip"
	default:
		return "", fmt.Errorf("%w: unsupported update platform", ErrReleaseInvalid)
	}
	return "jira_" + version.RemovePrefix(r.Version) + "_" + goos + "_" + arch + extension, nil
}

func (r Release) asset(name string, limit int64) (releaseAsset, error) {
	var selected releaseAsset
	count := 0
	for _, asset := range r.assets {
		if asset.name == name {
			selected = asset
			count++
		}
	}
	if count != 1 || selected.id <= 0 || selected.size <= 0 || selected.size > limit || selected.state != "uploaded" {
		return releaseAsset{}, fmt.Errorf("%w: expected one bounded uploaded asset %q", ErrReleaseInvalid, name)
	}
	for _, asset := range r.assets {
		if asset.id == selected.id && asset.name != selected.name {
			return releaseAsset{}, fmt.Errorf("%w: duplicate release asset identity", ErrReleaseInvalid)
		}
	}
	return selected, nil
}

func releaseRepository() (string, string) {
	owner, repository, _ := strings.Cut(strings.TrimPrefix(Module, "github.com/"), "/")
	return owner, repository
}

func (s *GitHubSource) download(ctx context.Context, asset releaseAsset, destination io.Writer, limit int64) error {
	owner, repository := releaseRepository()
	body, redirect, err := s.api.Repositories.DownloadReleaseAsset(ctx, owner, repository, asset.id, nil)
	if err != nil {
		return fmt.Errorf("read release asset: %w", err)
	}
	if body == nil {
		location, err := url.Parse(redirect)
		if err != nil || validateReleaseURL(location) != nil {
			return fmt.Errorf("%w: unsafe release asset redirect", ErrReleaseInvalid)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, redirect, nil)
		if err != nil {
			return fmt.Errorf("build release asset request: %w", err)
		}
		response, err := s.assets.Do(request)
		if err != nil {
			return fmt.Errorf("download release asset: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close() //nolint:errcheck // discarded read-only error response
			return fmt.Errorf("download release asset: HTTP %d", response.StatusCode)
		}
		body = response.Body
	}
	count, copyErr := io.Copy(destination, io.LimitReader(readerWithContext(ctx, body), limit+1))
	closeErr := body.Close()
	if err := errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		return fmt.Errorf("read release asset bytes: %w", err)
	}
	if count != asset.size || count > limit {
		return fmt.Errorf("%w: downloaded asset size differs from release metadata", ErrReleaseInvalid)
	}
	return nil
}

func validateReleaseURL(location *url.URL) error {
	if location == nil || location.Scheme != "https" || location.User != nil || location.Fragment != "" || location.Port() != "" {
		return fmt.Errorf("%w: unsafe release URL", ErrReleaseInvalid)
	}
	switch location.Host {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("%w: unexpected release host", ErrReleaseInvalid)
	}
}

// releaseTransport supplies the bounds and complete-document check missing from
// go-github's streaming JSON decoder. Error/redirect bodies are consumed and
// closed here because the SDK replaces them before closing its response. The
// SDK still owns endpoint construction, error decoding and asset API redirects.
type releaseTransport struct{ base http.RoundTripper }

func (t releaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateReleaseURL(request.URL); err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && response.StatusCode != http.StatusOK {
		response.Body.Close() //nolint:errcheck // discarded read-only protocol failure
		return nil, fmt.Errorf("%w: unexpected release HTTP status %d", ErrReleaseInvalid, response.StatusCode)
	}
	if response.StatusCode == http.StatusOK && !strings.HasSuffix(request.URL.Path, "/releases/latest") {
		return response, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(readerWithContext(request.Context(), response.Body), maxReleaseMetadataBytes+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr, request.Context().Err()); err != nil {
		return nil, fmt.Errorf("read release metadata: %w", err)
	}
	if int64(len(data)) > maxReleaseMetadataBytes || (response.StatusCode == http.StatusOK && !json.Valid(data)) {
		return nil, fmt.Errorf("%w: malformed or oversized release metadata", ErrReleaseInvalid)
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
