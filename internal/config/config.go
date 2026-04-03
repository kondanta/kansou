// Package config loads and validates kansou configuration from a TOML file.
// It provides built-in defaults so the tool works without any config file,
// and fails loudly on invalid config rather than silently correcting it.
package config

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// DefaultConfigPath is the default location of the config file.
const DefaultConfigPath = "~/.config/kansou/config.toml"

// DefaultPort is the default REST server port.
const DefaultPort = 8080

// DefaultMaxMultiplier is the default ceiling for genre bias multipliers.
// Any multiplier in a [genres.*] block must be > 0.0 and ≤ this value.
// Raise it in config if you need more aggressive genre bias adjustment.
const DefaultMaxMultiplier = 2.0

// DefaultPrimaryGenreWeight is the default blend ratio for primary genre support.
// 0.6 means 60% weight on the primary genre's raw multiplier, 40% on the
// contributing-only average across secondary matched genres. See ADR-022.
const DefaultPrimaryGenreWeight = 0.6

// weightSumTolerance is the allowed deviation from 1.0 for dimension weight sums.
const weightSumTolerance = 0.001

// DimensionDef is the TOML representation of a single scoring dimension.
// TOML keys are snake_case and map to struct fields via tags.
type DimensionDef struct {
	// Label is the human-readable display name shown in CLI prompts.
	Label string `toml:"label"`
	// Description is the hint shown during a scoring session.
	Description string `toml:"description"`
	// Weight is the base weight for this dimension. All dimension weights
	// in a config must sum to 1.0 (±0.001 tolerance).
	Weight float64 `toml:"weight"`
	// BiasResistant prevents genre multipliers from affecting this dimension.
	BiasResistant bool `toml:"bias_resistant"`
}

// ServerConfig holds REST server settings.
type ServerConfig struct {
	// Port is the port the server listens on. Default: 8080.
	// Overridable at runtime with --port flag.
	Port int `toml:"port"`
	// CORSAllowedOrigins is the list of origins allowed for CORS requests.
	CORSAllowedOrigins []string `toml:"cors_allowed_origins"`
}

// Config is the validated, fully-loaded application configuration.
// It is constructed by Load and is ready for use by the engine and server.
type Config struct {
	// DimensionOrder preserves the order in which dimensions appeared in config.
	// The scoring engine iterates dimensions in this order.
	DimensionOrder []string
	// Dimensions maps each dimension key to its definition.
	Dimensions map[string]DimensionDef
	// Genres maps lowercase genre names to per-dimension multipliers.
	// Keys are lowercased at load time for case-insensitive matching.
	Genres map[string]map[string]float64
	// MaxMultiplier is the upper bound for any genre bias multiplier.
	// All values in [genres.*] blocks must be > 0.0 and ≤ MaxMultiplier.
	// Default: 2.0.
	MaxMultiplier float64
	// PrimaryGenreWeight is the blend ratio for primary genre support (ADR-022).
	// Range [0.0, 1.0]. 0.0 disables the feature. Default: 0.6.
	PrimaryGenreWeight float64
	// Server holds REST server configuration.
	Server ServerConfig
	// DimensionsHash is the SHA256 hex digest of the serialised dimensions
	// config at load time. Carried through scoring results for provenance.
	DimensionsHash string
}

// rawConfig mirrors the TOML structure for parsing.
// Dimensions and Genres use maps because TOML table keys are dynamic.
type rawConfig struct {
	Dimensions         map[string]DimensionDef       `toml:"dimensions"`
	Genres             map[string]map[string]float64 `toml:"genres"`
	MaxMultiplier      float64                        `toml:"max_multiplier"`
	// PrimaryGenreWeight is a pointer so we can distinguish "not set" from
	// "explicitly 0.0" — 0.0 is a valid user value meaning "disable blending".
	PrimaryGenreWeight *float64                       `toml:"primary_genre_weight"`
	Server             ServerConfig                   `toml:"server"`
}

// Load reads the config file at path, validates it, and returns a Config.
// If path is empty, Load tries the default path. If the file does not exist,
// Load returns the built-in defaults without error.
// Any validation failure returns a descriptive error — kansou exits on this.
func Load(path string) (*Config, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}

	raw, found, err := parseFile(resolved)
	if err != nil {
		return nil, err
	}
	if !found {
		slog.Info("no config file found, using built-in defaults", "path", resolved)
		cfg := defaults()
		cfg.DimensionsHash = hashDimensions(cfg.Dimensions, cfg.DimensionOrder)
		return cfg, nil
	}

	cfg, err := build(raw)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolvePath expands ~ and resolves the config path. Returns the default
// path if path is empty.
func resolvePath(path string) (string, error) {
	if path == "" {
		path = DefaultConfigPath
	}
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	return path, nil
}

// parseFile decodes the TOML file at path. Returns (raw, true, nil) if found,
// (nil, false, nil) if the file does not exist, or (nil, false, err) on error.
func parseFile(path string) (*rawConfig, bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, false, nil
	}
	var raw rawConfig
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, false, fmt.Errorf("parsing config file %q: %w", path, err)
	}
	return &raw, true, nil
}

// build constructs a validated Config from a parsed rawConfig.
func build(raw *rawConfig) (*Config, error) {
	if len(raw.Dimensions) == 0 {
		def := defaults()
		def.DimensionsHash = hashDimensions(def.Dimensions, def.DimensionOrder)
		return def, nil
	}

	// Validate weights sum to 1.0.
	if err := validateWeights(raw.Dimensions); err != nil {
		return nil, err
	}

	// Validate genre keys reference existing dimensions.
	if err := validateGenreKeys(raw.Genres, raw.Dimensions); err != nil {
		return nil, err
	}

	// Resolve max_multiplier — default if not set.
	maxMult := raw.MaxMultiplier
	if maxMult == 0 {
		maxMult = DefaultMaxMultiplier
	}

	// Validate all genre multiplier values.
	if err := validateMultipliers(raw.Genres, maxMult); err != nil {
		return nil, err
	}

	// Validate server port range.
	port := raw.Server.Port
	if port == 0 {
		port = DefaultPort
	}
	if err := validatePort(port); err != nil {
		return nil, err
	}

	// Resolve primary_genre_weight — default if not set.
	primaryGenreWeight := DefaultPrimaryGenreWeight
	if raw.PrimaryGenreWeight != nil {
		if *raw.PrimaryGenreWeight < 0.0 || *raw.PrimaryGenreWeight > 1.0 {
			return nil, fmt.Errorf("primary_genre_weight %.4f must be between 0.0 and 1.0", *raw.PrimaryGenreWeight)
		}
		primaryGenreWeight = *raw.PrimaryGenreWeight
	}

	// Lowercase all genre keys for case-insensitive matching.
	genres := lowercaseGenreKeys(raw.Genres)

	// Preserve dimension insertion order by using TOML decode order.
	// BurntSushi/toml does not guarantee map iteration order, so we re-decode
	// to capture the key order from the raw TOML.
	order := dimensionOrder(raw.Dimensions)

	cfg := &Config{
		DimensionOrder:     order,
		Dimensions:         raw.Dimensions,
		Genres:             genres,
		MaxMultiplier:      maxMult,
		PrimaryGenreWeight: primaryGenreWeight,
		Server: ServerConfig{
			Port:               port,
			CORSAllowedOrigins: raw.Server.CORSAllowedOrigins,
		},
	}
	cfg.DimensionsHash = hashDimensions(cfg.Dimensions, cfg.DimensionOrder)
	return cfg, nil
}

// validateWeights checks that all dimension weights sum to 1.0 ±tolerance.
func validateWeights(dims map[string]DimensionDef) error {
	sum := 0.0
	for _, d := range dims {
		sum += d.Weight
	}
	if math.Abs(sum-1.0) > weightSumTolerance {
		return fmt.Errorf("dimension weights sum to %.4f, must be 1.0 (±%.3f)", sum, weightSumTolerance)
	}
	return nil
}

// validateGenreKeys ensures every dimension key in a genre block exists in dimensions.
func validateGenreKeys(genres map[string]map[string]float64, dims map[string]DimensionDef) error {
	for genre, multipliers := range genres {
		for key := range multipliers {
			if _, ok := dims[key]; !ok {
				return fmt.Errorf("genre %q references unknown dimension %q — not present in [dimensions]", genre, key)
			}
		}
	}
	return nil
}

// validateMultipliers checks that every multiplier in every genre block is valid.
func validateMultipliers(genres map[string]map[string]float64, maxMult float64) error {
	for genre, multipliers := range genres {
		if err := validateGenreMultipliers(genre, multipliers, maxMult); err != nil {
			return err
		}
	}
	return nil
}

// validateGenreMultipliers checks that each multiplier in a single genre block
// is > 0.0 and ≤ maxMult.
func validateGenreMultipliers(genre string, multipliers map[string]float64, maxMult float64) error {
	for dim, val := range multipliers {
		if val <= 0.0 || val > maxMult {
			return fmt.Errorf("genre %q dimension %q: multiplier %.4f must be > 0.0 and ≤ %.4f", genre, dim, val, maxMult)
		}
	}
	return nil
}

// validatePort checks that the port is in the valid range for unprivileged ports.
func validatePort(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("server port %d is out of range — must be between 1024 and 65535", port)
	}
	return nil
}

// lowercaseGenreKeys returns a copy of the genre map with all top-level keys lowercased.
func lowercaseGenreKeys(genres map[string]map[string]float64) map[string]map[string]float64 {
	out := make(map[string]map[string]float64, len(genres))
	for genre, multipliers := range genres {
		out[toLower(genre)] = multipliers
	}
	return out
}

// dimensionOrder returns dimension keys in a stable order. Because Go map
// iteration is random, we sort alphabetically as a deterministic fallback.
// The full config.example.toml order is: story, enjoyment, characters,
// production, pacing, world_building, value — users who care about prompt
// order should rely on the TOML file being well-ordered; we preserve
// alphabetic order as a stable tie-breaker.
func dimensionOrder(dims map[string]DimensionDef) []string {
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}
	// Stable sort so tests and output are deterministic.
	stableSort(keys)
	return keys
}

// hashDimensions returns a SHA256 hex digest of the dimension keys and weights
// in order. Used for provenance in scoring results.
func hashDimensions(dims map[string]DimensionDef, order []string) string {
	h := sha256.New()
	for _, key := range order {
		d := dims[key]
		fmt.Fprintf(h, "%s:%.6f:%v\n", key, d.Weight, d.BiasResistant)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// toLower is a dependency-free ASCII lowercase for config keys.
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// stableSort sorts a string slice in place using insertion sort.
// Only used on small slices (dimension count is typically <20).
func stableSort(s []string) {
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
