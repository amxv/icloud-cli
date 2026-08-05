package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/gofrs/flock"
)

func testPaths(t *testing.T, root string) config.AppPaths {
	t.Helper()
	paths, err := config.ResolveAppPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestOpenCreatesAndReopensVersionedPrivateState(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	for _, table := range []string{"app_metadata", "accounts", "credential_refs", "goose_db_version"} {
		var count int
		if err := state.db.QueryRowContext(context.Background(),
			"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d", table, count)
		}
	}
	var foreignKeys int
	if err := state.db.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d", foreignKeys)
	}
	var busyTimeout int
	if err := state.db.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d", busyTimeout)
	}
	var journalMode string
	if err := state.db.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q", journalMode)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{paths.Root, paths.BlobRoot, paths.LockRoot} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("mode for %s = %o", path, info.Mode().Perm())
		}
	}
	databaseInfo, err := os.Stat(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if databaseInfo.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %o", databaseInfo.Mode().Perm())
	}

	reopened, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.SchemaVersion() != SchemaVersion {
		t.Fatalf("reopened schema version = %d", reopened.SchemaVersion())
	}
}

func TestOpenWhileAnotherStoreRemainsOpen(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	first, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	firstAccounts, firstMore, err := first.Accounts().List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	secondAccounts, secondMore, err := second.Accounts().List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstAccounts) != 0 || firstMore || len(secondAccounts) != 0 || secondMore {
		t.Fatalf("first=%d/%v second=%d/%v", len(firstAccounts), firstMore, len(secondAccounts), secondMore)
	}
}

func TestAccountRepositoryZeroStateAndResolution(t *testing.T) {
	t.Parallel()
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	accounts, more, err := state.Accounts().List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 || more {
		t.Fatalf("accounts = %+v, more = %v", accounts, more)
	}
	if _, err := state.Accounts().Resolve(context.Background(), "missing"); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("resolve error = %v", err)
	}

	now := time.Date(2026, 8, 6, 1, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if err := state.WithTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"acct_0198d704-6c00-7000-8000-000000000001",
			"Personal",
			"owner@example.com",
			"owner",
			account.StatusPendingValidation,
			1,
			now,
			now,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)`,
			"cred_0198d704-6c00-7000-8000-000000000002",
			"acct_0198d704-6c00-7000-8000-000000000001",
			"env",
			"ICLOUD_TEST_APP_PASSWORD",
			now,
			now,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	accounts, more, err = state.Accounts().List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || more || accounts[0].Name != "Personal" || accounts[0].CredentialScheme != "env" {
		t.Fatalf("accounts = %+v, more = %v", accounts, more)
	}
	for _, selector := range []string{accounts[0].ID, "personal", "OWNER@example.com"} {
		resolved, err := state.Accounts().Resolve(context.Background(), selector)
		if err != nil {
			t.Fatalf("resolve %q: %v", selector, err)
		}
		if resolved.ID != accounts[0].ID || !resolved.Default {
			t.Fatalf("resolved = %+v", resolved)
		}
	}
}

func TestAccountRepositoryListIsBounded(t *testing.T) {
	t.Parallel()
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	now := time.Date(2026, 8, 6, 1, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if err := state.WithTx(context.Background(), func(tx *sql.Tx) error {
		for index, name := range []string{"Alpha", "Beta"} {
			isDefault := 0
			if index == 0 {
				isDefault = 1
			}
			if _, err := tx.ExecContext(context.Background(), `
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				fmt.Sprintf("acct_0198d704-6c00-7000-8000-%012d", index+10),
				name,
				strings.ToLower(name)+"@example.com",
				strings.ToLower(name),
				account.StatusPendingValidation,
				isDefault,
				now,
				now,
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	accounts, more, err := state.Accounts().List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Name != "Alpha" || !more {
		t.Fatalf("accounts = %+v, more = %v", accounts, more)
	}
	if _, _, err := state.Accounts().List(context.Background(), 0); err == nil {
		t.Fatal("zero limit was accepted")
	}
}

func TestSchemaConstraintsAndTransactions(t *testing.T) {
	t.Parallel()
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	err = state.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, created_at, updated_at)
VALUES ('acct_rollback', 'Rollback', 'rollback@example.com', 'rollback', 'pending_validation', ?, ?)`, now, now)
		if err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil || err.Error() != "force rollback" {
		t.Fatalf("rollback error = %v", err)
	}
	var count int
	if err := state.db.QueryRowContext(context.Background(), "SELECT count(*) FROM accounts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back account count = %d", count)
	}

	if _, err := state.db.ExecContext(context.Background(), `
INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, created_at, updated_at)
VALUES ('acct_invalid', 'Invalid', 'invalid@example.com', 'invalid', 'healthy', ?, ?)`, now, now); err == nil {
		t.Fatal("invalid status was accepted")
	}
	if _, err := state.db.ExecContext(context.Background(), `
INSERT INTO credential_refs (credential_ref_id, account_id, scheme, reference, created_at, updated_at)
VALUES ('cred_orphan', 'acct_missing', 'env', 'ICLOUD_MISSING', ?, ?)`, now, now); err == nil {
		t.Fatal("orphan credential reference was accepted")
	}
}

func TestMigrationLockReturnsDeterministicConflict(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(paths.MigrationLock, flock.SetPermissions(0o600))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_, err := Open(ctx, paths)
	if !errors.Is(err, ErrMigrationLock) {
		t.Fatalf("open error = %v", err)
	}
}

func TestConcurrentOpenAcrossProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess contention test")
	}
	home := filepath.Join(t.TempDir(), "shared")
	commands := make([]*exec.Cmd, 4)
	outputs := make([]strings.Builder, len(commands))
	for index := range commands {
		command := exec.Command(os.Args[0], "-test.run=^TestStoreOpenHelperProcess$")
		command.Env = append(os.Environ(),
			"ICLOUD_STORE_HELPER=1",
			"ICLOUD_STORE_HOME="+home,
		)
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		commands[index] = command
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for index := range commands {
		if err := commands[index].Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", index, err, outputs[index].String())
		}
	}

	state, err := Open(context.Background(), testPaths(t, home))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var integrity string
	if err := state.db.QueryRowContext(context.Background(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" || state.SchemaVersion() != SchemaVersion {
		t.Fatalf("integrity=%q schema=%d", integrity, state.SchemaVersion())
	}
}

func TestStoreOpenHelperProcess(t *testing.T) {
	if os.Getenv("ICLOUD_STORE_HELPER") != "1" {
		t.Skip("helper process only")
	}
	home := os.Getenv("ICLOUD_STORE_HOME")
	if home == "" {
		t.Fatal("missing helper home")
	}
	state, err := Open(context.Background(), testPaths(t, home))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accounts, more, err := state.Accounts().List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 || more || state.SchemaVersion() != SchemaVersion {
		t.Fatalf("accounts=%d more=%v schema=%d", len(accounts), more, state.SchemaVersion())
	}
}

func TestNoPrivateValuesAppearInSchema(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(filepath.Join("migrations", "00001_accounts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(contents))
	for _, forbidden := range []string{"password", "secret_value", "app-specific-password"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("migration contains %q", forbidden)
		}
	}
}
