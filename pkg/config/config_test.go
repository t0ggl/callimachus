package config

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/t0ggl/callimachus/pkg/testutil"
)

func TestEnvVarName(t *testing.T) {
	cases := map[string]string{
		"library_root":             "CALLIMACHUS_LIBRARY_ROOT",
		"prowlarr_url":             "CALLIMACHUS_PROWLARR_URL",
		"prowlarr_api_key":         "CALLIMACHUS_PROWLARR_API_KEY",
		"max_audiobook_size_mb":    "CALLIMACHUS_MAX_AUDIOBOOK_SIZE_MB",
		"allowed_formats":          "CALLIMACHUS_ALLOWED_FORMATS",
		"oidc.issuer-url":          "CALLIMACHUS_OIDC_ISSUER_URL",
		"  session_timeout_hours ": "CALLIMACHUS_SESSION_TIMEOUT_HOURS",
	}
	for input, want := range cases {
		if got := EnvVarName(input); got != want {
			t.Errorf("EnvVarName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAllRegisteredSettings_AutoEnvOverridePrecedence(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()

	// Seed every registered setting in SQLite with DB-specific values.
	for _, spec := range AllSettings() {
		dbVal := "db-val-" + spec.Key
		switch spec.Kind {
		case KindInt:
			dbVal = "111"
		case KindBool:
			dbVal = "false"
		case KindDuration:
			dbVal = "2m"
		case KindStringList:
			dbVal = `["DB1","DB2"]`
		}
		if spec.Key == KeyDefaultSearchFormat {
			dbVal = "audiobook"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, spec.Key, dbVal); err != nil {
			t.Fatalf("seed setting %s: %v", spec.Key, err)
		}
	}

	// Set CALLIMACHUS_{SETTING_NAME} for EVERY registered setting.
	for _, spec := range AllSettings() {
		envName := EnvVarName(spec.Key)
		envVal := "env-val-" + spec.Key
		switch spec.Kind {
		case KindInt:
			envVal = "777"
		case KindBool:
			envVal = "true"
		case KindDuration:
			envVal = "45s"
		case KindStringList:
			envVal = "ENV1,ENV2"
		}
		if spec.Key == KeyEbookTargetFormat {
			envVal = "AZW3"
		}
		if spec.Key == KeyLogLevel {
			envVal = "warn"
		}
		if spec.Key == KeyDefaultSearchFormat {
			envVal = "both"
		}
		t.Setenv(envName, envVal)
	}

	resolved, err := Load(ctx, db, Overrides{
		LibraryRoot: "/caller/books",
		ProwlarrURL: "http://caller-prowlarr",
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Every single registered setting must report overridden by CALLIMACHUS_{SETTING_NAME}.
	for _, spec := range AllSettings() {
		expectedEnv := EnvVarName(spec.Key)
		info, ok := resolved.Overrides[spec.Key]
		if !ok {
			t.Fatalf("missing override metadata for %q", spec.Key)
		}
		if !info.IsOverridden {
			t.Errorf("setting %q expected IsOverridden=true, got false", spec.Key)
		}
		if info.OverriddenBy != expectedEnv {
			t.Errorf("setting %q OverriddenBy = %q, want %q", spec.Key, info.OverriddenBy, expectedEnv)
		}
		if info.EnvVar != expectedEnv {
			t.Errorf("setting %q EnvVar = %q, want %q", spec.Key, info.EnvVar, expectedEnv)
		}
	}

	// Spot-check effective values match env, NOT DB or caller.
	s := resolved.Settings
	if s.LibraryRoot != "env-val-library_root" {
		t.Errorf("LibraryRoot = %q, want env-val-library_root", s.LibraryRoot)
	}
	if s.ProwlarrURL != "env-val-prowlarr_url" {
		t.Errorf("ProwlarrURL = %q, want env-val-prowlarr_url", s.ProwlarrURL)
	}
	if s.ProwlarrAPIKey != "env-val-prowlarr_api_key" {
		t.Errorf("ProwlarrAPIKey = %q, want env-val-prowlarr_api_key", s.ProwlarrAPIKey)
	}
	if s.ProwlarrSyncInterval != 45*time.Second {
		t.Errorf("ProwlarrSyncInterval = %v, want 45s", s.ProwlarrSyncInterval)
	}
	if !reflect.DeepEqual(s.AllowedFormats, []string{"ENV1", "ENV2"}) {
		t.Errorf("AllowedFormats = %v, want [ENV1 ENV2]", s.AllowedFormats)
	}
	if !reflect.DeepEqual(s.AllowedLanguages, []string{"env1", "env2"}) {
		t.Errorf("AllowedLanguages = %v, want [env1 env2]", s.AllowedLanguages)
	}
	if s.MaxAudiobookSizeMB != 777 {
		t.Errorf("MaxAudiobookSizeMB = %d, want 777", s.MaxAudiobookSizeMB)
	}
	if !s.PreferSingleFileM4B {
		t.Errorf("expected boolean env override true, got preferM4B=%v", s.PreferSingleFileM4B)
	}
}

func TestLoad_DatabaseAndDefaultFallbackWhenEnvUnset(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()

	// Persist a subset of settings in SQLite
	_, err := db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES
		('library_root', '/mnt/audiobooks'),
		('prowlarr_url', 'http://prowlarr.local:9696/'),
		('allowed_formats', '["EPUB","PDF"]'),
		('max_audiobook_size_mb', '4096')
	`)
	if err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	resolved, err := Load(ctx, db, Overrides{})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	s := resolved.Settings
	if s.LibraryRoot != "/mnt/audiobooks" {
		t.Errorf("LibraryRoot = %q, want /mnt/audiobooks", s.LibraryRoot)
	}
	if s.ProwlarrURL != "http://prowlarr.local:9696" {
		t.Errorf("ProwlarrURL = %q, want http://prowlarr.local:9696", s.ProwlarrURL)
	}
	if !reflect.DeepEqual(s.AllowedFormats, []string{"EPUB", "PDF"}) {
		t.Errorf("AllowedFormats = %v, want [EPUB PDF]", s.AllowedFormats)
	}
	if s.MaxAudiobookSizeMB != 4096 {
		t.Errorf("MaxAudiobookSizeMB = %d, want 4096", s.MaxAudiobookSizeMB)
	}
	if resolved.Overrides[KeyLibraryRoot].IsOverridden {
		t.Errorf("expected library_root IsOverridden=false when env var unset")
	}
}

func TestSaveNonOverridden_ProtectsEnvOverriddenKeys(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()

	t.Setenv("CALLIMACHUS_PROWLARR_URL", "http://env-prowlarr:9696")

	resolved, err := SaveNonOverridden(ctx, db, map[string]string{
		KeyProwlarrURL:        "http://attempted-db-overwrite:9696",
		KeyLibraryRoot:        "/data/library",
		KeyMaxAudiobookSizeMB: "1536",
		KeyAllowedFormats:     "EPUB,M4B,MP3",
	}, Overrides{})
	if err != nil {
		t.Fatalf("SaveNonOverridden failed: %v", err)
	}

	if resolved.Settings.ProwlarrURL != "http://env-prowlarr:9696" {
		t.Errorf("ProwlarrURL = %q, want env value", resolved.Settings.ProwlarrURL)
	}
	if resolved.Settings.LibraryRoot != "/data/library" {
		t.Errorf("LibraryRoot = %q, want /data/library", resolved.Settings.LibraryRoot)
	}
	if resolved.Settings.MaxAudiobookSizeMB != 1536 {
		t.Errorf("MaxAudiobookSizeMB = %d, want 1536", resolved.Settings.MaxAudiobookSizeMB)
	}
	if !reflect.DeepEqual(resolved.Settings.AllowedFormats, []string{"EPUB", "M4B", "MP3"}) {
		t.Errorf("AllowedFormats = %v, want [EPUB M4B MP3]", resolved.Settings.AllowedFormats)
	}

	// Verify prowlarr_url was NOT written into the DB settings table
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'prowlarr_url'`).Scan(&count); err != nil {
		t.Fatalf("query settings count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected overridden key prowlarr_url NOT to be persisted, found %d rows", count)
	}
}

func TestResolveBootstrap_EnvOverridesFlags(t *testing.T) {
	t.Setenv("CALLIMACHUS_CONFIG_DIR", "/env/config")
	t.Setenv("CALLIMACHUS_DATABASE_PATH", "/env/db/app.db")
	t.Setenv("CALLIMACHUS_HOST", "127.0.0.1")
	t.Setenv("CALLIMACHUS_PORT", "9090")

	boot := ResolveBootstrap("/flag/config", "/flag/db.db", "0.0.0.0", 8080, true)
	if boot.ConfigDir != "/env/config" {
		t.Errorf("ConfigDir = %q, want /env/config", boot.ConfigDir)
	}
	if boot.DBPath != "/env/db/app.db" {
		t.Errorf("DBPath = %q, want /env/db/app.db", boot.DBPath)
	}
	if boot.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", boot.Host)
	}
	if boot.Port != 9090 {
		t.Errorf("Port = %d, want 9090", boot.Port)
	}
}

func TestEbookSizeConfiguration(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	resolved, err := Load(ctx, db, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Settings.ToQualityProfile().MaxAudiobookSizeMB != 1536 {
		t.Fatal("expected 1,536 MB audiobook size default")
	}
	if resolved.Settings.ToQualityProfile().MaxEbookSizeBytes != 100*1024*1024 {
		t.Fatal("missing default")
	}
	resolved, err = SaveNonOverridden(ctx, db, map[string]string{KeyMaxEbookSizeMB: "150"}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Settings.MaxEbookSizeBytes != 150*1024*1024 {
		t.Fatal("not persisted")
	}
	t.Setenv("CALLIMACHUS_MAX_EBOOK_SIZE_MB", "200")
	resolved, err = SaveNonOverridden(ctx, db, map[string]string{KeyMaxEbookSizeMB: "10"}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Settings.ToQualityProfile().MaxEbookSizeBytes != 200*1024*1024 || !resolved.Overrides[KeyMaxEbookSizeMB].IsOverridden {
		t.Fatal("override lost")
	}
	t.Setenv("CALLIMACHUS_MAX_EBOOK_SIZE_MB", "")
	resolved, err = Load(ctx, db, Overrides{})
	if err != nil || resolved.Settings.MaxEbookSizeBytes != 150*1024*1024 {
		t.Fatal("override mutated DB", err)
	}
	for _, invalid := range []string{"-1", "1.5", "8796093022208", "bad"} {
		t.Setenv("CALLIMACHUS_MAX_EBOOK_SIZE_MB", invalid)
		if _, err := Load(ctx, db, Overrides{}); err == nil {
			t.Fatalf("accepted invalid env %q", invalid)
		}
	}
}

func TestEbookPreferencesPersistenceAndOverrides(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	const ordered = `["pdf","EPUB","mobi"]`
	resolved, err := SaveNonOverridden(ctx, db, map[string]string{KeyEbookFormatPreferences: ordered}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PDF", "EPUB", "MOBI"}
	if !reflect.DeepEqual(resolved.Settings.ToQualityProfile().EbookFormatPreferences, want) {
		t.Fatalf("order lost: %+v", resolved.Settings)
	}
	var stored string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", KeyEbookFormatPreferences).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `["PDF","EPUB","MOBI"]` {
		t.Fatalf("not stored as ordered JSON: %s", stored)
	}
	for _, invalid := range []string{`["PDF","pdf"]`, `["M4B"]`, `["EXE"]`, `[""]`, `null`, `"EPUB"`} {
		if _, err := SaveNonOverridden(ctx, db, map[string]string{KeyEbookFormatPreferences: invalid}, Overrides{}); err == nil {
			t.Errorf("accepted invalid sequence %s", invalid)
		}
	}
	t.Setenv(EnvVarName(KeyEbookFormatPreferences), "MOBI,PDF")
	resolved, err = SaveNonOverridden(ctx, db, map[string]string{KeyEbookFormatPreferences: `["EPUB"]`}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Settings.EbookFormatPreferences, []string{"MOBI", "PDF"}) || !resolved.Overrides[KeyEbookFormatPreferences].IsOverridden {
		t.Fatalf("env override not applied: %+v", resolved)
	}
	t.Setenv(EnvVarName(KeyEbookFormatPreferences), "")
	resolved, err = Load(ctx, db, Overrides{})
	if err != nil || !reflect.DeepEqual(resolved.Settings.EbookFormatPreferences, want) {
		t.Fatalf("override mutated DB: %+v, %v", resolved, err)
	}
}

func TestEbookConversionSettings(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	got, err := SaveNonOverridden(ctx, db, map[string]string{KeyConvertEbooks: "true", KeyEbookTargetFormat: "azw3"}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Settings.ToQualityProfile().ConvertEbooks || got.Settings.EbookTargetFormat != "AZW3" {
		t.Fatalf("settings: %+v", got.Settings)
	}
	if _, err := SaveNonOverridden(ctx, db, map[string]string{KeyEbookTargetFormat: "pdf"}, Overrides{}); err == nil {
		t.Fatal("accepted unsupported target")
	}
	t.Setenv(EnvVarName(KeyEbookTargetFormat), "EPUB")
	got, err = SaveNonOverridden(ctx, db, map[string]string{KeyEbookTargetFormat: "AZW3"}, Overrides{})
	if err != nil || got.Settings.EbookTargetFormat != "EPUB" || !got.Overrides[KeyEbookTargetFormat].IsOverridden {
		t.Fatalf("override: %+v %v", got, err)
	}
}

func TestAudiobookSizeInvalidEnvironment(t *testing.T) {
	for _, value := range []string{"-1", "1.5", "8796093022208"} {
		t.Setenv("CALLIMACHUS_MAX_AUDIOBOOK_SIZE_MB", value)
		if _, err := Load(context.Background(), nil, Overrides{}); err == nil {
			t.Fatalf("accepted invalid audiobook size %q", value)
		}
	}
}

func TestLogLevelSettings(t *testing.T) {
	const env = "CALLIMACHUS_LOG_LEVEL"
	t.Setenv(env, "")
	t.Setenv("LOG_LEVEL", "debug")
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	spec, ok := LookupSpec(KeyLogLevel)
	if !ok || spec.Default != "info" || EnvVarName(KeyLogLevel) != env {
		t.Fatalf("incorrect log level registration: %+v", spec)
	}
	got, err := Load(ctx, db, Overrides{})
	if err != nil || got.Settings.LogLevel != slog.LevelInfo || got.Overrides[KeyLogLevel] != (OverrideInfo{EnvVar: env}) {
		t.Fatalf("default log level: %+v, %v", got, err)
	}

	for _, tc := range []struct {
		raw  string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"  WARN  ", slog.LevelWarn},
		{"error", slog.LevelError},
	} {
		got, err := SaveNonOverridden(ctx, db, map[string]string{KeyLogLevel: tc.raw}, Overrides{})
		if err != nil || got.Settings.LogLevel != tc.want {
			t.Fatalf("save %q: %+v, %v", tc.raw, got, err)
		}
	}
	for _, invalid := range []string{"verbose", "", "INFO+1", "warning"} {
		if _, err := SaveNonOverridden(ctx, db, map[string]string{KeyLogLevel: invalid}, Overrides{}); !errors.Is(err, ErrInvalidLogLevel) {
			t.Errorf("expected validation error for %q, got %v", invalid, err)
		}
	}

	t.Setenv(env, " DeBuG ")
	got, err = SaveNonOverridden(ctx, db, map[string]string{KeyLogLevel: "warn"}, Overrides{})
	if err != nil || got.Settings.LogLevel != slog.LevelDebug ||
		got.Overrides[KeyLogLevel] != (OverrideInfo{IsOverridden: true, OverriddenBy: env, EnvVar: env}) {
		t.Fatalf("environment override: %+v, %v", got, err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", KeyLogLevel).Scan(&stored); err != nil || stored != "error" {
		t.Fatalf("stored log level: %q, %v", stored, err)
	}

	t.Setenv(env, "invalid")
	if _, err := Load(ctx, db, Overrides{}); !errors.Is(err, ErrInvalidLogLevel) {
		t.Fatalf("invalid override must fail validation: %v", err)
	}
	t.Setenv(env, "")
	if _, err := db.ExecContext(ctx, "UPDATE settings SET value='trace' WHERE key=?", KeyLogLevel); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, db, Overrides{}); !errors.Is(err, ErrInvalidLogLevel) {
		t.Fatalf("invalid database value must fail validation: %v", err)
	}
}

func TestDefaultSearchFormat(t *testing.T) {
	const env = "CALLIMACHUS_DEFAULT_SEARCH_FORMAT"
	t.Setenv(env, "")
	ctx := context.Background()
	db := testutil.NewTestDB(t)

	spec, ok := LookupSpec(KeyDefaultSearchFormat)
	if !ok || spec.Kind != KindString || spec.Default != "ebook" || EnvVarName(spec.Key) != env {
		t.Fatalf("unexpected default_search_format spec: %+v", spec)
	}
	assertFormat := func(got *ResolvedSettings, err error, want string, override OverrideInfo) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if got.Settings.DefaultSearchFormat != want || got.Overrides[KeyDefaultSearchFormat] != override {
			t.Fatalf("format = %q, override = %+v; want %q, %+v",
				got.Settings.DefaultSearchFormat, got.Overrides[KeyDefaultSearchFormat], want, override)
		}
	}

	resolved, err := Load(ctx, db, Overrides{})
	assertFormat(resolved, err, "ebook", OverrideInfo{EnvVar: env})
	for _, format := range []string{"ebook", "audiobook", "both"} {
		resolved, err := SaveNonOverridden(ctx, db, map[string]string{KeyDefaultSearchFormat: format}, Overrides{})
		assertFormat(resolved, err, format, OverrideInfo{EnvVar: env})
	}
	for _, invalid := range []string{"", "epub", "ebook,audiobook"} {
		if _, err := SaveNonOverridden(ctx, db, map[string]string{KeyDefaultSearchFormat: invalid}, Overrides{}); err == nil {
			t.Errorf("accepted invalid format %q", invalid)
		}
	}

	t.Setenv(env, "audiobook")
	resolved, err = SaveNonOverridden(ctx, db, map[string]string{KeyDefaultSearchFormat: "ebook"}, Overrides{})
	assertFormat(resolved, err, "audiobook", OverrideInfo{IsOverridden: true, OverriddenBy: env, EnvVar: env})

	t.Setenv(env, "epub")
	if _, err := Load(ctx, db, Overrides{}); err == nil {
		t.Fatal("accepted invalid environment override")
	}
	t.Setenv(env, "")
	resolved, err = Load(ctx, db, Overrides{})
	assertFormat(resolved, err, "both", OverrideInfo{EnvVar: env})

	if _, err := db.ExecContext(ctx, "UPDATE settings SET value = ? WHERE key = ?", "m4b", KeyDefaultSearchFormat); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, db, Overrides{}); err == nil {
		t.Fatal("accepted invalid database value")
	}
}

func TestBundleSeriesInLibrarySettings(t *testing.T) {
	const env = "CALLIMACHUS_BUNDLE_SERIES_IN_LIBRARY"
	t.Setenv(env, "")

	ctx := context.Background()
	db := testutil.NewTestDB(t)

	spec, ok := LookupSpec(KeyBundleSeriesInLibrary)
	if !ok || spec.Kind != KindBool || spec.Default != "true" || EnvVarName(KeyBundleSeriesInLibrary) != env {
		t.Fatalf("unexpected bundle_series_in_library spec: %+v", spec)
	}

	// Default is true
	got, err := Load(ctx, db, Overrides{})
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if !got.Settings.BundleSeriesInLibrary {
		t.Fatalf("expected BundleSeriesInLibrary default true, got false")
	}
	if got.Overrides[KeyBundleSeriesInLibrary] != (OverrideInfo{EnvVar: env}) {
		t.Fatalf("unexpected default override info: %+v", got.Overrides[KeyBundleSeriesInLibrary])
	}

	// Persist false in SQLite
	got, err = SaveNonOverridden(ctx, db, map[string]string{KeyBundleSeriesInLibrary: "false"}, Overrides{})
	if err != nil {
		t.Fatalf("SaveNonOverridden false: %v", err)
	}
	if got.Settings.BundleSeriesInLibrary {
		t.Fatalf("expected BundleSeriesInLibrary=false after save")
	}
	var stored string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", KeyBundleSeriesInLibrary).Scan(&stored); err != nil || stored != "false" {
		t.Fatalf("stored value = %q, err = %v; want false", stored, err)
	}

	// Invalid bool rejected
	if _, err := SaveNonOverridden(ctx, db, map[string]string{KeyBundleSeriesInLibrary: "maybe"}, Overrides{}); err == nil {
		t.Fatalf("expected error for invalid bool value")
	}

	// Environment override takes precedence and prevents DB mutation
	t.Setenv(env, "true")
	got, err = SaveNonOverridden(ctx, db, map[string]string{KeyBundleSeriesInLibrary: "false"}, Overrides{})
	if err != nil {
		t.Fatalf("SaveNonOverridden while overridden: %v", err)
	}
	if !got.Settings.BundleSeriesInLibrary {
		t.Fatalf("expected env override true, got false")
	}
	wantOverride := OverrideInfo{IsOverridden: true, OverriddenBy: env, EnvVar: env}
	if got.Overrides[KeyBundleSeriesInLibrary] != wantOverride {
		t.Fatalf("override info = %+v, want %+v", got.Overrides[KeyBundleSeriesInLibrary], wantOverride)
	}

	// Re-set env to false while DB has false, or check DB stayed false
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", KeyBundleSeriesInLibrary).Scan(&stored); err != nil || stored != "false" {
		t.Fatalf("stored value after overridden save = %q, err = %v; want false", stored, err)
	}
}

func TestOpenLibraryEnabledSettings(t *testing.T) {
	const env = "CALLIMACHUS_OPENLIBRARY_ENABLED"
	t.Setenv(env, "")

	ctx := context.Background()
	db := testutil.NewTestDB(t)

	spec, ok := LookupSpec(KeyOpenLibraryEnabled)
	if !ok || spec.Kind != KindBool || spec.Default != "true" || EnvVarName(KeyOpenLibraryEnabled) != env {
		t.Fatalf("unexpected openlibrary_enabled spec: %+v", spec)
	}

	// Default is true
	got, err := Load(ctx, db, Overrides{})
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if !got.Settings.OpenLibraryEnabled {
		t.Fatalf("expected OpenLibraryEnabled default true, got false")
	}
	if got.Overrides[KeyOpenLibraryEnabled] != (OverrideInfo{EnvVar: env}) {
		t.Fatalf("unexpected default override info: %+v", got.Overrides[KeyOpenLibraryEnabled])
	}

	// Persist false in SQLite
	got, err = SaveNonOverridden(ctx, db, map[string]string{KeyOpenLibraryEnabled: "false"}, Overrides{})
	if err != nil {
		t.Fatalf("SaveNonOverridden false: %v", err)
	}
	if got.Settings.OpenLibraryEnabled {
		t.Fatalf("expected OpenLibraryEnabled=false after save")
	}
	var stored string
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", KeyOpenLibraryEnabled).Scan(&stored); err != nil || stored != "false" {
		t.Fatalf("stored value = %q, err = %v; want false", stored, err)
	}

	// Invalid bool rejected
	if _, err := SaveNonOverridden(ctx, db, map[string]string{KeyOpenLibraryEnabled: "maybe"}, Overrides{}); err == nil {
		t.Fatalf("expected error for invalid bool value")
	}

	// Environment override takes precedence and prevents DB mutation
	t.Setenv(env, "true")
	got, err = SaveNonOverridden(ctx, db, map[string]string{KeyOpenLibraryEnabled: "false"}, Overrides{})
	if err != nil {
		t.Fatalf("SaveNonOverridden while overridden: %v", err)
	}
	if !got.Settings.OpenLibraryEnabled {
		t.Fatalf("expected env override true, got false")
	}
	wantOverride := OverrideInfo{IsOverridden: true, OverriddenBy: env, EnvVar: env}
	if got.Overrides[KeyOpenLibraryEnabled] != wantOverride {
		t.Fatalf("override info = %+v, want %+v", got.Overrides[KeyOpenLibraryEnabled], wantOverride)
	}

	// Verify DB stayed false
	if err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", KeyOpenLibraryEnabled).Scan(&stored); err != nil || stored != "false" {
		t.Fatalf("stored value after overridden save = %q, err = %v; want false", stored, err)
	}
}
