package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// Default connection settings
const (
	DefaultMaxOpenConns = 25
	DefaultMaxIdleConns = 25
	DefaultBusyTimeout  = 5000
)

// Config represents database connection configuration.
type Config struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DB wraps *sql.DB with Callimachus-specific helpers.
type DB struct {
	*sql.DB
	cfg Config
}

func init() {
	// Enable fdatasync on supported platforms (Linux) for faster WAL commits
	_, _ = sqlite.Fdatasync(true)

	// Register global connection hook for modernc.org/sqlite to guarantee
	// per-connection PRAGMAs are executed on every physical connection opened
	// by database/sql connection pool. Database-level settings (journal_mode=WAL,
	// auto_vacuum=INCREMENTAL) are persistent in the DB file header and set in Open().
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		pragmas := []string{
			"PRAGMA synchronous = NORMAL;",
			"PRAGMA foreign_keys = ON;",
			"PRAGMA busy_timeout = 5000;",
			"PRAGMA cache_size = -64000;",
			"PRAGMA temp_store = MEMORY;",
		}
		for _, pragma := range pragmas {
			_, _ = conn.ExecContext(context.Background(), pragma, nil)
		}
		return nil
	})
}

// Open creates and initializes a new SQLite database connection pool with required PRAGMAs.
func Open(cfg Config) (*DB, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, errors.New("database path cannot be empty")
	}

	dsn := buildDSN(cfg.Path)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = DefaultMaxOpenConns
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = DefaultMaxIdleConns
	}

	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}

	// Verify connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	// Ensure persistent database-level settings are initialized on file creation
	var autoVac int
	_ = sqlDB.QueryRowContext(ctx, "PRAGMA auto_vacuum;").Scan(&autoVac)
	if autoVac != 2 {
		_, _ = sqlDB.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL; BEGIN IMMEDIATE; COMMIT;")
	}

	var journalMode string
	_ = sqlDB.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode)
	if journalMode != "wal" {
		_, _ = sqlDB.ExecContext(ctx, "PRAGMA journal_mode = WAL;")
	}

	// Apply remaining PRAGMAs to initial connection
	initialPragmas := []string{
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA cache_size = -64000;",
		"PRAGMA temp_store = MEMORY;",
	}
	for _, pragma := range initialPragmas {
		_, _ = sqlDB.ExecContext(ctx, pragma)
	}

	return &DB{
		DB:  sqlDB,
		cfg: cfg,
	}, nil
}

// Migrate executes all pending schema migrations on the database.
func (db *DB) Migrate(ctx context.Context) error {
	return Migrate(ctx, db.DB)
}

// Config returns the configuration used to open the database.
func (db *DB) Config() Config {
	return db.cfg
}

// buildDSN formats the connection DSN with required SQLite parameters.
func buildDSN(rawPath string) string {
	separator := "?"
	if strings.Contains(rawPath, "?") {
		separator = "&"
	}

	params := "_synchronous=NORMAL&_foreign_keys=ON&_busy_timeout=5000&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)"
	return rawPath + separator + params
}
