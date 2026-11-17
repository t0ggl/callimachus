package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
)

// Standardized setting keys in lowercase_snake_case.
// Every setting's environment variable is automatically derived via
// EnvVarName(key) -> "CALLIMACHUS_" + strings.ToUpper(key).
const (
	KeyGoogleBooksAPIKey      = "google_books_api_key"
	KeyHardcoverAPIKey        = "hardcover_api_key"
	KeyCompletedDownloadRoot  = "completed_download_root"
	KeyLibraryRoot            = "library_root"
	KeyProwlarrURL            = "prowlarr_url"
	KeyProwlarrAPIKey         = "prowlarr_api_key"
	KeyProwlarrSyncInterval   = "prowlarr_sync_interval"
	KeyIndexerSyncInterval    = "indexer_sync_interval"
	KeyStagingPollInterval    = "staging_poll_interval"
	KeyQualityProfileName     = "quality_profile_name"
	KeyAllowedLanguages       = "allowed_languages"
	KeyAllowedFormats         = "allowed_formats"
	KeyPreferSingleFileM4B    = "prefer_single_file_m4b"
	KeyMaxFileSizeMB          = "max_file_size_mb"
	KeyMaxEbookSizeMB         = "max_ebook_size_mb"
	KeyEbookFormatPreferences = "ebook_format_preferences"
)

// Bootstrap setting keys (resolved before opening SQLite).
const (
	BootstrapKeyConfigDir    = "config_dir"
	BootstrapKeyDatabasePath = "database_path"
	BootstrapKeyHost         = "host"
	BootstrapKeyPort         = "port"
)

// SettingKind classifies how a setting value is parsed and formatted.
type SettingKind string

const (
	KindString     SettingKind = "string"
	KindInt        SettingKind = "int"
	KindBool       SettingKind = "bool"
	KindStringList SettingKind = "string_list"
	KindDuration   SettingKind = "duration"
)

// SettingSpec declares a single configuration setting in the central registry.
type SettingSpec struct {
	Key     string
	Kind    SettingKind
	Secret  bool
	Default string
}

// EnvVarName derives the standardized environment variable name for any setting key.
// For example, "library_root" -> "CALLIMACHUS_LIBRARY_ROOT".
func EnvVarName(settingKey string) string {
	normalized := strings.NewReplacer(".", "_", "-", "_").Replace(strings.TrimSpace(settingKey))
	return "CALLIMACHUS_" + strings.ToUpper(normalized)
}

var registry = []SettingSpec{
	{Key: KeyGoogleBooksAPIKey, Kind: KindString, Secret: true},
	{Key: KeyHardcoverAPIKey, Kind: KindString, Secret: true},
	{Key: KeyLibraryRoot, Kind: KindString},
	{Key: KeyCompletedDownloadRoot, Kind: KindString},
	{Key: KeyProwlarrURL, Kind: KindString},
	{Key: KeyProwlarrAPIKey, Kind: KindString, Secret: true},
	{Key: KeyProwlarrSyncInterval, Kind: KindDuration, Default: "1h"},
	{Key: KeyIndexerSyncInterval, Kind: KindDuration, Default: "15m"},
	{Key: KeyStagingPollInterval, Kind: KindDuration, Default: "5s"},
	{Key: KeyQualityProfileName, Kind: KindString, Default: "Standard Audiobook"},
	{Key: KeyAllowedLanguages, Kind: KindStringList, Default: "en"},
	{Key: KeyAllowedFormats, Kind: KindStringList, Default: "EPUB,M4B"},
	{Key: KeyEbookFormatPreferences, Kind: KindStringList, Default: "EPUB,AZW3,MOBI,PDF,CBR,CBZ"},
	{Key: KeyPreferSingleFileM4B, Kind: KindBool, Default: "true"},
	{Key: KeyMaxFileSizeMB, Kind: KindInt, Default: "1536"},
	{Key: KeyMaxEbookSizeMB, Kind: KindInt, Default: "100"},
}

var specByKey = func() map[string]SettingSpec {
	m := make(map[string]SettingSpec, len(registry))
	for _, s := range registry {
		m[s.Key] = s
	}
	return m
}()

// AllSettings returns a copy of all registered setting specifications.
func AllSettings() []SettingSpec {
	out := make([]SettingSpec, len(registry))
	copy(out, registry)
	return out
}

// LookupSpec returns the SettingSpec for a standardized setting key.
func LookupSpec(key string) (SettingSpec, bool) {
	spec, ok := specByKey[key]
	return spec, ok
}

// OverrideInfo describes whether a setting is currently overridden by an environment variable.
type OverrideInfo struct {
	IsOverridden bool   `json:"isOverridden"`
	OverriddenBy string `json:"overriddenBy"`
	EnvVar       string `json:"envVar"`
}

// Settings holds the effective resolved application configuration.
type Settings struct {
	GoogleBooksAPIKey      string        `json:"-"`
	HardcoverAPIKey        string        `json:"-"`
	CompletedDownloadRoot  string        `json:"completedDownloadRoot"`
	LibraryRoot            string        `json:"libraryRoot"`
	ProwlarrURL            string        `json:"prowlarrUrl"`
	ProwlarrAPIKey         string        `json:"-"`
	ProwlarrSyncInterval   time.Duration `json:"prowlarrSyncInterval"`
	IndexerSyncInterval    time.Duration `json:"indexerSyncInterval"`
	StagingPollInterval    time.Duration `json:"stagingPollInterval"`
	QualityProfileName     string        `json:"qualityProfileName"`
	AllowedLanguages       []string      `json:"allowedLanguages"`
	AllowedFormats         []string      `json:"allowedFormats"`
	PreferSingleFileM4B    bool          `json:"preferSingleFileM4b"`
	MaxFileSizeMB          int           `json:"maxFileSizeMb"`
	MaxEbookSizeBytes      int64         `json:"maxEbookSizeBytes"`
	EbookFormatPreferences []string      `json:"ebookFormatPreferences"`
}

// ToQualityProfile builds a downloaders.QualityProfile from the resolved settings.
func (s Settings) ToQualityProfile() downloaders.QualityProfile {
	langs := make([]string, len(s.AllowedLanguages))
	copy(langs, s.AllowedLanguages)
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	fmts := make([]string, len(s.AllowedFormats))
	for i, f := range s.AllowedFormats {
		fmts[i] = strings.ToLower(strings.TrimSpace(f))
	}
	return downloaders.QualityProfile{
		AllowedLanguages:       langs,
		AllowedFormats:         fmts,
		PreferSingleM4B:        s.PreferSingleFileM4B,
		MaxFileSizeMB:          s.MaxFileSizeMB,
		MaxEbookSizeBytes:      s.MaxEbookSizeBytes,
		EbookFormatPreferences: s.EbookFormatPreferences,
	}
}

// Overrides allows programmatic callers and CLI flags to supply non-DB defaults
// without shadowing environment variable overrides.
type Overrides struct {
	LibraryRoot          string
	ProwlarrURL          string
	ProwlarrAPIKey       string
	ProwlarrSyncInterval time.Duration
	IndexerSyncInterval  time.Duration
	StagingPollInterval  time.Duration
}

// ResolvedSettings pairs effective Settings with per-key override metadata.
type ResolvedSettings struct {
	Settings  Settings
	Overrides map[string]OverrideInfo
}

// BootstrapConfig holds pre-database server/CLI startup configuration resolved from flags + env vars.
type BootstrapConfig struct {
	ConfigDir string
	DBPath    string
	Host      string
	Port      int
}

// ResolveBootstrap resolves startup settings needed before opening the database.
// Environment variables (CALLIMACHUS_CONFIG_DIR, CALLIMACHUS_DATABASE_PATH,
// CALLIMACHUS_HOST, CALLIMACHUS_PORT) take absolute precedence over CLI flags and defaults.
func ResolveBootstrap(configDirFlag, dbPathFlag, hostFlag string, portFlag int, portSet bool) BootstrapConfig {
	cfgDir := strings.TrimSpace(configDirFlag)
	if envVal, _, ok := LookupEnvForSetting(BootstrapKeyConfigDir); ok {
		cfgDir = envVal
	}
	if cfgDir == "" {
		if fi, err := os.Stat("/config"); err == nil && fi.IsDir() {
			cfgDir = "/config"
		} else {
			cfgDir = "."
		}
	}

	dbPath := strings.TrimSpace(dbPathFlag)
	if envVal, _, ok := LookupEnvForSetting(BootstrapKeyDatabasePath); ok {
		dbPath = envVal
	}
	if dbPath == "" {
		if cfgDir != "" && cfgDir != "." {
			dbPath = filepath.Join(cfgDir, "callimachus.db")
		} else {
			dbPath = "callimachus.db"
		}
	}

	host := strings.TrimSpace(hostFlag)
	if envVal, _, ok := LookupEnvForSetting(BootstrapKeyHost); ok {
		host = envVal
	}
	if host == "" {
		host = "0.0.0.0"
	}

	port := portFlag
	if envVal, _, ok := LookupEnvForSetting(BootstrapKeyPort); ok {
		if parsed, err := strconv.Atoi(envVal); err == nil && parsed > 0 {
			port = parsed
		}
	}
	if !portSet && port <= 0 {
		port = 8080
	}

	return BootstrapConfig{
		ConfigDir: cfgDir,
		DBPath:    dbPath,
		Host:      host,
		Port:      port,
	}
}

// LookupEnvForSetting checks the standardized CALLIMACHUS_{SETTING_NAME} env var for key.
func LookupEnvForSetting(key string) (val string, envName string, ok bool) {
	canonical := EnvVarName(key)
	if v, exists := os.LookupEnv(canonical); exists && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), canonical, true
	}
	return "", "", false
}

// Load resolves every registered setting using strict precedence:
// Environment Variable (CALLIMACHUS_{SETTING_NAME}) > Caller/CLI Overrides > SQLite settings table > Defaults.
func Load(ctx context.Context, db *database.DB, caller Overrides) (*ResolvedSettings, error) {
	dbValues := make(map[string]string)
	if db != nil {
		rows, err := db.QueryContext(ctx, "SELECT key, value FROM settings")
		if err != nil {
			return nil, fmt.Errorf("load settings from database: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if scanErr := rows.Scan(&k, &v); scanErr != nil {
				return nil, fmt.Errorf("scan setting row: %w", scanErr)
			}
			dbValues[k] = v
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate settings rows: %w", err)
		}
	}

	callerValues := callerOverridesMap(caller)
	resolvedValues := make(map[string]string, len(registry))
	overrideMap := make(map[string]OverrideInfo, len(registry))

	for _, spec := range registry {
		canonicalEnv := EnvVarName(spec.Key)
		info := OverrideInfo{
			IsOverridden: false,
			OverriddenBy: "",
			EnvVar:       canonicalEnv,
		}

		// 1. Default value
		val := spec.Default
		if spec.Key == KeyLibraryRoot && val == "" {
			val = defaultLibraryPath()
		}

		// 2. Database-stored value
		if dbVal, ok := dbValues[spec.Key]; ok && strings.TrimSpace(dbVal) != "" {
			val = strings.TrimSpace(dbVal)
		}

		// 3. Programmatic / CLI override (when not overridden by env)
		if callerVal, ok := callerValues[spec.Key]; ok && strings.TrimSpace(callerVal) != "" {
			val = strings.TrimSpace(callerVal)
		}

		// 4. Environment variable ALWAYS takes absolute precedence over DB, caller, and defaults
		if envVal, envName, ok := LookupEnvForSetting(spec.Key); ok {
			val = envVal
			info.IsOverridden = true
			info.OverriddenBy = envName
		}

		resolvedValues[spec.Key] = val
		overrideMap[spec.Key] = info
	}

	if _, err := validateAndNormalize(specByKey[KeyMaxEbookSizeMB], resolvedValues[KeyMaxEbookSizeMB]); err != nil {
		return nil, fmt.Errorf("invalid max_ebook_size_mb: %w", err)
	}
	settings := materializeSettings(resolvedValues)
	return &ResolvedSettings{
		Settings:  settings,
		Overrides: overrideMap,
	}, nil
}

// SaveNonOverridden persists provided setting updates to SQLite, skipping any setting
// currently overridden by an environment variable so env overrides can never be bypassed.
// Returns the updated ResolvedSettings.
func SaveNonOverridden(ctx context.Context, db *database.DB, updates map[string]string, caller Overrides) (*ResolvedSettings, error) {
	if db == nil {
		return nil, errors.New("database is required to save settings")
	}

	current, err := Load(ctx, db, caller)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin settings tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for key, rawVal := range updates {
		spec, ok := specByKey[key]
		if !ok {
			return nil, fmt.Errorf("unknown setting key: %q", key)
		}
		if current.Overrides[key].IsOverridden {
			// Enforce absolute precedence: never persist mutations for env-overridden settings.
			continue
		}

		normalized, err := validateAndNormalize(spec, rawVal)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: %w", key, err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at)
			 VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			key, normalized,
		)
		if err != nil {
			return nil, fmt.Errorf("upsert setting %s: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit settings tx: %w", err)
	}

	return Load(ctx, db, caller)
}

func validateAndNormalize(spec SettingSpec, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	switch spec.Kind {
	case KindString:
		return trimmed, nil
	case KindInt:
		if trimmed == "" {
			return spec.Default, nil
		}
		v, err := strconv.Atoi(trimmed)
		if err != nil {
			return "", fmt.Errorf("expected integer, got %q", raw)
		}
		if spec.Key == KeyMaxEbookSizeMB && int64(v) > (1<<63-1)/(1024*1024) {
			return "", fmt.Errorf("size exceeds supported byte range")
		}
		if v < 0 {
			return "", fmt.Errorf("expected non-negative integer, got %d", v)
		}
		return strconv.Itoa(v), nil
	case KindBool:
		if trimmed == "" {
			return spec.Default, nil
		}
		b, err := parseBool(trimmed)
		if err != nil {
			return "", err
		}
		return strconv.FormatBool(b), nil
	case KindDuration:
		if trimmed == "" {
			return spec.Default, nil
		}
		d, err := time.ParseDuration(trimmed)
		if err != nil || d <= 0 {
			return "", fmt.Errorf("expected positive duration (e.g. 5s, 15m, 1h), got %q", raw)
		}
		return d.String(), nil
	case KindStringList:
		if spec.Key == KeyEbookFormatPreferences {
			var items []string
			if err := json.Unmarshal([]byte(trimmed), &items); err != nil || items == nil {
				return "", errors.New("expected an ordered array of ebook formats")
			}
			seen := make(map[string]bool)
			for i, item := range items {
				format := strings.ToUpper(strings.TrimSpace(item))
				switch format {
				case "EPUB", "AZW3", "MOBI", "PDF", "CBR", "CBZ":
				default:
					return "", fmt.Errorf("unsupported ebook format %q", item)
				}
				if seen[format] {
					return "", fmt.Errorf("duplicate ebook format %q", format)
				}
				seen[format] = true
				items[i] = format
			}
			encoded, err := json.Marshal(items)
			return string(encoded), err
		}
		items := parseList(trimmed)
		if spec.Key == KeyAllowedFormats {
			for i := range items {
				items[i] = strings.ToUpper(items[i])
			}
		} else if spec.Key == KeyAllowedLanguages {
			for i := range items {
				items[i] = strings.ToLower(items[i])
			}
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	default:
		return trimmed, nil
	}
}

func callerOverridesMap(c Overrides) map[string]string {
	m := make(map[string]string)
	if strings.TrimSpace(c.LibraryRoot) != "" {
		m[KeyLibraryRoot] = c.LibraryRoot
	}
	if strings.TrimSpace(c.ProwlarrURL) != "" {
		m[KeyProwlarrURL] = c.ProwlarrURL
	}
	if strings.TrimSpace(c.ProwlarrAPIKey) != "" {
		m[KeyProwlarrAPIKey] = c.ProwlarrAPIKey
	}
	if c.ProwlarrSyncInterval > 0 {
		m[KeyProwlarrSyncInterval] = c.ProwlarrSyncInterval.String()
	}
	if c.IndexerSyncInterval > 0 {
		m[KeyIndexerSyncInterval] = c.IndexerSyncInterval.String()
	}
	if c.StagingPollInterval > 0 {
		m[KeyStagingPollInterval] = c.StagingPollInterval.String()
	}
	return m
}

func materializeSettings(vals map[string]string) Settings {
	return Settings{
		GoogleBooksAPIKey:      vals[KeyGoogleBooksAPIKey],
		HardcoverAPIKey:        vals[KeyHardcoverAPIKey],
		LibraryRoot:            vals[KeyLibraryRoot],
		CompletedDownloadRoot:  vals[KeyCompletedDownloadRoot],
		ProwlarrURL:            strings.TrimRight(vals[KeyProwlarrURL], "/"),
		ProwlarrAPIKey:         vals[KeyProwlarrAPIKey],
		ProwlarrSyncInterval:   parseDurationOr(vals[KeyProwlarrSyncInterval], time.Hour),
		IndexerSyncInterval:    parseDurationOr(vals[KeyIndexerSyncInterval], 15*time.Minute),
		StagingPollInterval:    parseDurationOr(vals[KeyStagingPollInterval], 5*time.Second),
		QualityProfileName:     orDefault(vals[KeyQualityProfileName], "Standard Audiobook"),
		AllowedLanguages:       parseLowerList(vals[KeyAllowedLanguages], []string{"en"}),
		AllowedFormats:         parseUpperList(vals[KeyAllowedFormats], []string{"EPUB", "M4B"}),
		PreferSingleFileM4B:    parseBoolOr(vals[KeyPreferSingleFileM4B], true),
		MaxFileSizeMB:          parseNonNegIntOr(vals[KeyMaxFileSizeMB], 1536),
		MaxEbookSizeBytes:      int64(parseNonNegIntOr(vals[KeyMaxEbookSizeMB], 100)) * 1024 * 1024,
		EbookFormatPreferences: parseList(strings.ToUpper(vals[KeyEbookFormatPreferences])),
	}
}

func defaultLibraryPath() string {
	if fi, err := os.Stat("/books"); err == nil && fi.IsDir() {
		return "/books"
	}
	return "./books"
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func parseIntOr(v string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return n
	}
	return def
}

func parseNonNegIntOr(v string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return n
	}
	return def
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected boolean, got %q", v)
	}
}

func parseBoolOr(v string, def bool) bool {
	b, err := parseBool(v)
	if err != nil {
		return def
	}
	return b
}

func parseDurationOr(v string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
		return d
	}
	return def
}

func parseList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}
	}
	if strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, item := range arr {
				if s := strings.TrimSpace(item); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseUpperList(raw string, def []string) []string {
	items := parseList(raw)
	if len(items) == 0 {
		return def
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, strings.ToUpper(it))
	}
	return out
}

func parseLowerList(raw string, def []string) []string {
	items := parseList(raw)
	if len(items) == 0 {
		return def
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, strings.ToLower(it))
	}
	return out
}
