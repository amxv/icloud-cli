package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/config"
	"github.com/gofrs/flock"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const SchemaVersion int64 = 6

var ErrMigrationLock = errors.New("migration lock unavailable")

//go:embed migrations/*.sql
var migrations embed.FS

// Store owns the local SQLite connection and repository transaction boundary.
type Store struct {
	db            *sql.DB
	schemaVersion int64
}

func Open(ctx context.Context, paths config.AppPaths) (*Store, error) {
	if err := config.EnsureAppPaths(paths); err != nil {
		return nil, err
	}

	migrationLock := flock.New(paths.MigrationLock, flock.SetPermissions(0o600))
	locked, err := migrationLock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMigrationLock, err)
	}
	if !locked {
		return nil, ErrMigrationLock
	}
	defer migrationLock.Unlock() // best effort; the process also releases the OS lock on exit

	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	closeWithError := func(openErr error) (*Store, error) {
		_ = db.Close()
		return nil, openErr
	}
	if err := configureSQLite(ctx, db); err != nil {
		return closeWithError(err)
	}
	if err := db.PingContext(ctx); err != nil {
		return closeWithError(fmt.Errorf("ping SQLite database: %w", err))
	}
	version, err := migrate(ctx, db)
	if err != nil {
		return closeWithError(err)
	}
	if version != SchemaVersion {
		return closeWithError(fmt.Errorf("unexpected schema version %d", version))
	}
	if err := os.Chmod(paths.Database, 0o600); err != nil {
		return closeWithError(fmt.Errorf("secure SQLite database: %w", err))
	}

	return &Store{db: db, schemaVersion: version}, nil
}

func configureSQLite(ctx context.Context, db *sql.DB) error {
	// busy_timeout must be the first operation on the sole pooled connection so
	// concurrent readers do not turn harmless startup contention into an
	// immediate SQLITE_BUSY failure.
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("configure SQLite busy timeout: %w", err)
	}

	// journal_mode is persistent. Reissuing PRAGMA journal_mode=WAL on every
	// process open can itself require a write lock, even when the database is
	// already in WAL mode. Inspect first and only perform the transition once.
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("read SQLite journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
			return fmt.Errorf("configure SQLite journal mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return fmt.Errorf("configure SQLite journal mode: got %q", journalMode)
		}
	}

	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite: %w", err)
		}
	}
	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify SQLite foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("SQLite foreign keys are disabled")
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB) (int64, error) {
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return 0, fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		db,
		migrationFS,
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return 0, fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return 0, fmt.Errorf("apply embedded migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) SchemaVersion() int64 {
	if s == nil {
		return 0
	}
	return s.schemaVersion
}

func (s *Store) Accounts() *AccountRepository {
	return &AccountRepository{db: s.db}
}

func (s *Store) Folders() *FolderRepository {
	return &FolderRepository{db: s.db}
}

func (s *Store) Mail() *MailRepository {
	return &MailRepository{db: s.db}
}

// WithTx runs fn in one SQLite transaction and rolls it back on every error.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
