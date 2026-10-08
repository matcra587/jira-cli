package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gechr/x/shell"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/providers/confmap"
	koanfenv "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// EnvPrefix scopes the environment layer. JIRA_BASE_URL becomes
// base_url; the prefix keeps a bare PATH or EMAIL from being read as
// configuration.
const EnvPrefix = "JIRA_"

// delim separates setting paths. Literal alias names remain keys inside the
// aliases map; changing this delimiter would silently re-key other settings.
const delim = "."

// Resolved is a loaded configuration together with the layer each value
// came from.
//
// Provenance is why the layers are loaded into separate koanf instances
// rather than one merged instance: koanf's merge keeps the winning
// value and discards where it came from, so `config current` could not
// answer "why is this the value?" — the question a user actually has
// when the answer surprises them.
type Resolved struct {
	// NamedProfiles retains document ownership even when no profiles exist.
	NamedProfiles  bool
	Profile        string
	DefaultProfile string
	// Config is the effective configuration, all layers applied.
	Config Config
	// Path is the config file consulted, whether or not it existed.
	Path string
	// FileExists reports whether Path was present. A missing file is
	// normal, not an error.
	FileExists bool

	// Warnings are non-fatal problems found while resolving — today, a
	// stored credential record that cannot be read. Carried rather than
	// returned as an error so a broken record degrades the CLI instead
	// of disabling it.
	Warnings []string

	// CredentialError retains a failed store read without stopping local
	// settings resolution or explicit environment credentials. Callers that
	// require authentication consult it when no usable token was resolved.
	// It is never included in public settings or warning text.
	CredentialError error `json:"-"`

	sources map[string]Layer
}

// Source returns the layer that set key, or LayerUnknown when no layer
// claimed it.
//
// Unattributed is reported as unknown rather than assumed to be a
// default: the defaults layer covers every key, so an unclaimed key means
// that layer did not load. Answering "default" there would describe a
// configuration that never happened.
func (r Resolved) Source(key string) Layer {
	if l, ok := r.sources[key]; ok {
		return l
	}
	return LayerUnknown
}

// FlagLayer carries the global flags that change how configuration
// resolves — the binding site config.go's LayerFlag constant was reserved
// for.
//
// It is PROCESS-SCOPED state, which this package otherwise avoids, and
// the reason is that the two things needing it cannot be reached by
// threading a parameter. Theme resolution loads config before the command
// tree exists (internal/cli's themeFromConfig, which runs during
// conductor setup so a terminal probe never costs five seconds), and the
// composition root's adapters load it from lazily-constructed closures
// during RunE. A path handed to one of those is invisible to the other,
// and a --config that silently governed the client but not the theme
// would be a flag that half works.
//
// Set ONCE, by the composition root, from the raw argv before anything
// loads. Nothing else may write it.
type FlagLayer struct {
	Profile    string
	ProfileSet bool
	// Path overrides the config file location — the --config flag. Not a
	// config VALUE but a choice of where values come from, so it selects
	// the file rather than entering the layer stack.
	Path string
	// Timeout bounds each HTTP request — the --timeout flag. A real value,
	// so it enters as LayerFlag at the top of precedence and `config
	// current` attributes it honestly.
	Timeout time.Duration
	// TimeoutSet distinguishes --timeout=0 (the caller asking for no
	// deadline) from the flag being absent. Without it the zero value
	// would erase a timeout set in the file on every invocation that did
	// not mention the flag.
	TimeoutSet bool
}

// flagLayer is the process's flag layer. Guarded because tests set it,
// and a test that sets it while another loads would otherwise race.
var (
	flagLayerMu sync.RWMutex
	flagLayer   FlagLayer
)

// SetFlagLayer records the global flags for this process. The composition
// root calls it once, before the first load.
func SetFlagLayer(l FlagLayer) {
	flagLayerMu.Lock()
	defer flagLayerMu.Unlock()
	flagLayer = l
}

// currentFlagLayer reads the process's flag layer.
func currentFlagLayer() FlagLayer {
	flagLayerMu.RLock()
	defer flagLayerMu.RUnlock()
	return flagLayer
}

// Options control a load. The zero value loads the real environment and
// the default path.
type Options struct {
	Profile string
	// Path overrides the config file location.
	Path string
	// Environ injects the environment; nil reads the process. A seam,
	// not a convenience: tests that mutate the real environment cannot
	// run in parallel and leak into each other.
	Environ func() []string
	// Credentials supplies the stored API token. nil skips the keyring
	// layer entirely, which is what every path that must not touch the
	// developer's real credential store wants — tests, and the config
	// commands that only report.
	Credentials core.CredentialStore
}

// Load resolves configuration from defaults, then the file, then the
// environment — later layers winning — and records which layer supplied
// each value.
//
// A missing config file is NOT an error. The CLI must run on environment
// variables alone, which is exactly how the tracer phase supplies
// credentials, so treating absence as failure would make the common path
// the broken one.
func Load(opts Options) (*Resolved, error) {
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	variables := slices.Clone(environ())
	opts.Environ = func() []string { return slices.Clone(variables) }
	flags := currentFlagLayer()
	selection, err := selectedProfile(opts, flags, variables)
	if err != nil {
		return nil, err
	}
	path, err := resolvePath(opts.Path)
	if err != nil {
		return nil, err
	}

	var warnings []string
	var credentialError error
	// storedSite is the site the credential record belongs to, kept so
	// the tuple check below can tell whether it still applies.
	var storedSite string
	layers := map[Layer]*koanf.Koanf{}
	merged := koanf.New(delim)

	// Defaults.
	defaults := koanf.New(delim)
	if err := defaults.Load(confmap.Provider(Defaults(), delim), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}
	layers[LayerDefault] = defaults
	if err := merged.Merge(defaults); err != nil {
		return nil, fmt.Errorf("merge defaults: %w", err)
	}

	// File, if present.
	fileLayer := koanf.New(delim)
	exists := true
	// The previous file provider cleaned paths before opening them. Retain
	// that selection rule while reporting the caller's authored spelling.
	data, err := readFileInput(filepath.Clean(path))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("load config file %s: %w", path, err)
		}
		exists = false
	}
	if !exists && path == flags.Path && flags.Path != "" {
		// A file the CALLER named must be there. A missing DEFAULT config
		// file is the ordinary state and stays silent, but --config is an
		// explicit instruction to read one particular file, and silently
		// falling back to the environment would run the command under a
		// configuration the caller did not ask for — with a typo'd path
		// looking exactly like a file whose settings did not take effect.
		return nil, fmt.Errorf("config file %s does not exist", path)
	}
	doc, err := decodeProfileDocument(data, false)
	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	if doc.named {
		doc, err = decodeProfileDocument(data, true)
		if err != nil {
			return nil, fmt.Errorf("config file %s: %w", path, err)
		}
	}
	fields, profile, err := doc.selected(selection)
	if err != nil {
		return nil, err
	}
	if err := fileLayer.Load(confmap.Provider(fields, ""), nil); err != nil {
		return nil, err
	}
	if exists {
		// Decode the file ALONE before merging. Strictness has to be
		// applied here or it is bypassable: koanf replaces a whole
		// subtree when a later layer supplies a scalar at the same path,
		// so `[base_url] typo = "x"` in the file plus JIRA_BASE_URL in
		// the environment erases the bad key before ErrorUnused ever sees
		// it, and the typo the user needs told about vanishes silently.
		var fileOnly Config
		if err := unmarshalFile(fileLayer, &fileOnly); err != nil {
			return nil, fmt.Errorf("config file %s: %w", path, err)
		}
		if err := validateCredentialID(fileOnly.CredentialID); err != nil {
			return nil, err
		}
		layers[LayerFile] = fileLayer
		if err := merged.Merge(fileLayer); err != nil {
			return nil, fmt.Errorf("merge config file: %w", err)
		}
	}

	envLayer := koanf.New(delim)
	if err := envLayer.Load(envProvider(opts.Environ), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}
	if doc.named && merged.String("credential_id") == "" {
		opts.Credentials = nil
	}

	// Credential store, if one is wired and holds a record.
	//
	// It supplies the SITE and ACCOUNT as well as the token, because
	// `jira auth login` stores all three together — a login is one
	// command, and identity is useless apart from the secret it
	// authenticates with.
	if opts.Credentials != nil {
		rec, credErr := credentials.Load(credentials.Select(opts.Credentials, merged.String("credential_id")))
		switch {
		case credErr == nil && !rec.Complete():
			// A record missing its site or account cannot be used as
			// one. Contributing only the token would pair a verified
			// secret with identity from some OTHER layer — a tuple that
			// was never checked together, and that Jira may well accept
			// as somebody else.
			warnings = append(warnings, "the stored credentials are incomplete; run `jira auth login` again")
		case credErr == nil:
			if doc.named {
				mismatch := false
				for _, layer := range []*koanf.Koanf{fileLayer, envLayer} {
					if layer.Exists("base_url") && credentials.NormalizeSite(layer.String("base_url")) != credentials.NormalizeSite(rec.Site) {
						mismatch = true
					}
					if layer.Exists("email") && !strings.EqualFold(strings.TrimSpace(layer.String("email")), strings.TrimSpace(rec.Email)) {
						mismatch = true
					}
				}
				if mismatch {
					warnings = append(warnings, "stored credentials belong to a different profile identity; they were not used")
					break
				}
			}
			storedSite = rec.Site
			stored := map[string]any{
				"base_url":  rec.Site,
				"email":     rec.Email,
				"api_token": rec.Token,
			}
			{
				keyringLayer := koanf.New(delim)
				if err := keyringLayer.Load(confmap.Provider(stored, delim), nil); err != nil {
					return nil, fmt.Errorf("load keyring: %w", err)
				}
				layers[LayerKeyring] = keyringLayer
				if err := merged.Merge(keyringLayer); err != nil {
					return nil, fmt.Errorf("merge keyring: %w", err)
				}
			}
		case errors.Is(credErr, credentials.ErrNotStored):
			// Nothing stored is the ordinary state before login.
		case errors.Is(credErr, credentials.ErrUnreadable):
			// A record that cannot be read must not take the CLI down.
			// Every command resolves config, so failing here would break
			// the very command whose job is to clear it — and the only
			// remaining fix would be an OS keyring tool. Carry on
			// without the layer and surface it as a warning.
			warnings = append(warnings, credErr.Error())
		default:
			credentialError = credErr
			warnings = append(warnings, "the credential store could not be read; stored credentials were not used")
		}
	}

	// Environment.
	layers[LayerEnv] = envLayer
	if err := merged.Merge(envLayer); err != nil {
		return nil, fmt.Errorf("merge environment: %w", err)
	}

	// Flags, the top of the precedence order (config.go's Layers).
	if flags.TimeoutSet {
		flagValues := koanf.New(delim)
		if err := flagValues.Load(confmap.Provider(map[string]any{
			"timeout": flags.Timeout.String(),
		}, delim), nil); err != nil {
			return nil, fmt.Errorf("load flags: %w", err)
		}
		layers[LayerFlag] = flagValues
		if err := merged.Merge(flagValues); err != nil {
			return nil, fmt.Errorf("merge flags: %w", err)
		}
	}

	var cfg Config
	if err := unmarshalMerged(merged, &cfg); err != nil {
		return nil, err
	}
	if cfg.WorkdaySeconds <= 0 {
		return nil, errors.New("workday_seconds must be positive")
	}

	// The stored credential is a TUPLE, and it must not be taken apart
	// across a host boundary.
	//
	// The layers above merge key by key, so `JIRA_BASE_URL=other-host`
	// alone would override just the site and leave the stored email and
	// token in place — sending a credential to a host it was never
	// issued for, and never verified against. Nothing else in the layer
	// model can see that, because no single key is wrong.
	//
	// So: if the resolved site is not the one the credential was stored
	// for, the credential does not apply. The site stands (the caller
	// asked for it), the stored identity drops out, and resolution falls
	// through to whatever the environment or file supplies for that
	// host.
	// Both sides normalized: the stored site already is, and comparing a
	// normalized value against a raw one made "https://site/" read as a
	// different host from "https://site" — dropping a credential that
	// still applied, and warning about a host identical to itself.
	if _, ok := layers[LayerKeyring]; ok && storedSite != "" &&
		credentials.NormalizeSite(cfg.BaseURL) != storedSite {
		delete(layers, LayerKeyring)
		rebuilt, rebuildErr := reresolveWithout(opts, path, LayerKeyring)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		rebuilt.Warnings = append(rebuilt.Warnings, fmt.Sprintf(
			"stored credentials are for %s, but base_url resolved to %s — they were not used",
			storedSite, cfg.BaseURL,
		))
		return rebuilt, nil
	}

	return &Resolved{
		NamedProfiles:   doc.named,
		Profile:         profile,
		DefaultProfile:  doc.defaultProfile,
		Warnings:        warnings,
		CredentialError: credentialError,
		Config:          cfg,
		Path:            path,
		FileExists:      exists,
		sources:         provenance(layers),
	}, nil
}

// provenance walks the layers from lowest precedence to highest, so the
// last writer of a key is the one recorded — the same order the merge
// applied, derived from Layers() rather than restated here.
func provenance(layers map[Layer]*koanf.Koanf) map[string]Layer {
	sources := map[string]Layer{}
	for _, layer := range Layers() {
		k, ok := layers[layer]
		if !ok {
			continue
		}
		for _, key := range Keys() {
			if k.Exists(key) {
				sources[key] = layer
			}
		}
	}
	return sources
}

// envProvider maps JIRA_* variables onto config keys.
//
// An unknown JIRA_* variable is DROPPED rather than passed through: the
// decoder runs with ErrorUnused, so forwarding it would turn an
// unrelated variable in the user's shell — JIRA_HOME, something another
// tool set — into a hard failure of every command.
func envProvider(environ func() []string) *koanfenv.Env {
	known := map[string]bool{}
	for _, k := range Keys() {
		known[k] = true
	}
	return koanfenv.Provider(delim, koanfenv.Opt{
		Prefix:      EnvPrefix,
		EnvironFunc: environ,
		TransformFunc: func(k, v string) (string, any) {
			// Empty means UNSET, not "set to nothing".
			if v == "" {
				return "", nil
			}
			key := strings.ToLower(strings.TrimPrefix(k, EnvPrefix))
			if !known[key] || key == "aliases" || key == "credential_id" {
				return "", nil
			}
			return key, v
		},
	})
}

// unmarshalFile decodes the file layer, holding TOML to the types it
// declares.
//
// TOML carries real types, so `read_only = 1` is a mistake the format
// can express and the user meant something by. Weak conversion turns it
// into true and silently enables a mode that refuses every write — a
// surprising place to arrive at from a typo.
func unmarshalFile(k *koanf.Koanf, cfg *Config) error {
	if err := unmarshal(k, cfg, false); err != nil {
		return err
	}
	if k.Exists("workday_seconds") {
		// mapstructure converts TOML floats to integers even with weak
		// conversion disabled. Reject that truncation before merging layers.
		if _, ok := k.Get("workday_seconds").(int64); !ok {
			return errors.New("workday_seconds must be an integer")
		}
		if cfg.WorkdaySeconds <= 0 {
			return errors.New("workday_seconds must be positive")
		}
	}
	return nil
}

// unmarshalMerged decodes the merged view, converting between types.
//
// The environment layer is all strings by nature, so `JIRA_READ_ONLY=true`
// has to be converted to reach a bool field — which is why exactly one of
// the two decodes is lenient.
func unmarshalMerged(k *koanf.Koanf, cfg *Config) error {
	return unmarshal(k, cfg, true)
}

// unmarshal decodes into cfg and REJECTS any key that maps to no field,
// whichever layer it came from.
//
// A key nobody claims would otherwise be dropped in silence, and the
// user's typo — read_ony, base_uri — looks exactly like a setting that
// did not take effect, with nothing anywhere saying why. Type conversion
// is the one axis the two callers above differ on.
func unmarshal(k *koanf.Koanf, cfg *Config, weak bool) error {
	err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.TextUnmarshallerHookFunc(),
			),
			WeaklyTypedInput: weak,
			ErrorUnused:      true,
			Result:           cfg,
		},
	})
	if err != nil {
		return fmt.Errorf("decode configuration: %w", err)
	}
	return nil
}

// DefaultPath is the config file location.
//
// Resolved through the shared XDG helper rather than os.UserConfigDir,
// which honors XDG_CONFIG_HOME only on Linux — on macOS it answers
// ~/Library/Application Support, so the same environment variable would
// move the file on one machine and be ignored on another. A developer
// tool should sit where the developer put their other tools, which is
// what every CLI in this space does.
func DefaultPath() (string, error) {
	path, err := shell.ConfigDir("jira", "config.toml")
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	return path, nil
}

// An explicit caller path beats --config, which beats the default location.
// Local updates and layered reads use the same selection without sharing
// credentials or persisting environment-derived values.
func resolvePath(path string) (string, error) {
	if path == "" {
		path = currentFlagLayer().Path
	}
	if path == "" {
		return DefaultPath()
	}
	return path, nil
}

// reresolveWithout repeats the load with one layer withheld.
//
// A second pass rather than surgery on the merged instance: koanf
// merges destructively, so a layer cannot be un-merged, and rebuilding
// is the only way to get provenance that matches the values.
func reresolveWithout(opts Options, path string, skip Layer) (*Resolved, error) {
	if skip != LayerKeyring {
		return nil, fmt.Errorf("cannot re-resolve without layer %q", skip)
	}
	stripped := opts
	stripped.Path = path
	stripped.Credentials = nil
	return Load(stripped)
}
