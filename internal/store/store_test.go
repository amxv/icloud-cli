package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/gofrs/flock"
	"github.com/pressly/goose/v3"
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
	for _, table := range []string{"app_metadata", "accounts", "credential_refs", "folders", "raw_blobs", "messages", "message_recipients", "message_references", "message_locations", "message_content", "message_parts", "attachments", "sync_checkpoints", "message_identity_evidence", "message_dedupe_conflicts", "threads", "thread_messages", "thread_aliases", "thread_edges", "message_search_documents", "message_search_fts", "outbound_attempts", "idempotency_records", "local_message_sources", "goose_db_version"} {
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

func TestAccountLifecycleAndCredentialTransactions(t *testing.T) {
	t.Parallel()
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 8, 6, 2, 0, 0, 0, time.UTC)

	first, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID:        "acct_0198d704-6c00-7000-8000-000000000101",
		CredentialID:     "cred_0198d704-6c00-7000-8000-000000000102",
		Name:             "Personal",
		Email:            "owner@icloud.com",
		IMAPUsername:     "owner@icloud.com",
		CredentialScheme: account.CredentialEnv,
		CredentialRef:    "ICLOUD_PERSONAL_PASSWORD",
		Now:              now,
	})
	if err != nil || !first.Default || first.Status != account.StatusPendingValidation {
		t.Fatalf("first = %+v, err = %v", first, err)
	}
	second, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID:        "acct_0198d704-6c00-7000-8000-000000000103",
		CredentialID:     "cred_0198d704-6c00-7000-8000-000000000104",
		Name:             "Work",
		Email:            "work@icloud.com",
		IMAPUsername:     "work@icloud.com",
		CredentialScheme: account.CredentialFile,
		CredentialRef:    "/private/credential",
		Now:              now.Add(time.Minute),
	})
	if err != nil || second.Default {
		t.Fatalf("second = %+v, err = %v", second, err)
	}
	selected, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil || selected.ID != first.ID {
		t.Fatalf("selected = %+v, err = %v", selected, err)
	}

	second, err = state.Accounts().Rename(context.Background(), second.ID, "Work Mail", now.Add(2*time.Minute))
	if err != nil || second.Name != "Work Mail" {
		t.Fatalf("renamed = %+v, err = %v", second, err)
	}
	second, err = state.Accounts().SetDefault(context.Background(), "work@icloud.com", now.Add(3*time.Minute))
	if err != nil || !second.Default {
		t.Fatalf("default = %+v, err = %v", second, err)
	}
	if _, _, err := state.Accounts().RemovalCandidate(context.Background(), second.ID); !errors.Is(err, account.ErrDefaultRemoval) {
		t.Fatalf("default removal error = %v", err)
	}

	_, credential, err := state.Accounts().UpsertCredential(
		context.Background(),
		second.ID,
		"cred_unused_when_replacing",
		account.CredentialKeyring,
		"icloud-cli-deadbeef:"+second.ID,
		now.Add(4*time.Minute),
	)
	if err != nil || credential.Scheme != account.CredentialKeyring || credential.ID != "cred_0198d704-6c00-7000-8000-000000000104" {
		t.Fatalf("credential = %+v, err = %v", credential, err)
	}
	_, deleted, err := state.Accounts().DeleteCredential(context.Background(), second.ID, now.Add(5*time.Minute))
	if err != nil || deleted.ID != credential.ID || deleted.Configured {
		t.Fatalf("deleted credential = %+v, err = %v", deleted, err)
	}
	if _, _, err := state.Accounts().Credential(context.Background(), second.ID); !errors.Is(err, account.ErrCredentialMissing) {
		t.Fatalf("credential after delete = %v", err)
	}

	if _, err := state.Accounts().SetDefault(context.Background(), first.ID, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	removed, _, err := state.Accounts().Remove(context.Background(), second.ID)
	if err != nil || removed.ID != second.ID {
		t.Fatalf("removed = %+v, err = %v", removed, err)
	}
	if _, err := state.Accounts().Resolve(context.Background(), second.ID); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("resolve removed = %v", err)
	}
}

func TestAccountCreateConflictsAndAmbiguousSelection(t *testing.T) {
	t.Parallel()
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 8, 6, 2, 10, 0, 0, time.UTC)
	create := func(accountID, credentialID, name, email string) error {
		_, err := state.Accounts().Create(context.Background(), account.CreateRecord{
			AccountID:        accountID,
			CredentialID:     credentialID,
			Name:             name,
			Email:            email,
			IMAPUsername:     email,
			CredentialScheme: account.CredentialEnv,
			CredentialRef:    "ICLOUD_PASSWORD",
			Now:              now,
		})
		return err
	}
	if err := create("acct_0198d704-6c00-7000-8000-000000000111", "cred_0198d704-6c00-7000-8000-000000000112", "Personal", "owner@icloud.com"); err != nil {
		t.Fatal(err)
	}
	if err := create("acct_0198d704-6c00-7000-8000-000000000113", "cred_0198d704-6c00-7000-8000-000000000114", "Personal", "other@icloud.com"); !errors.Is(err, account.ErrConflict) {
		t.Fatalf("name conflict = %v", err)
	}
	if err := create("acct_0198d704-6c00-7000-8000-000000000115", "cred_0198d704-6c00-7000-8000-000000000116", "Work", "work@icloud.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(context.Background(), `UPDATE accounts SET is_default = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Accounts().ResolveSelected(context.Background(), ""); !errors.Is(err, account.ErrAmbiguous) {
		t.Fatalf("selection error = %v", err)
	}
}

func TestAccountHealthUpdatesPersistAndPreserveLastKnownGoodState(t *testing.T) {
	t.Parallel()
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	createdAt := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	created, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID:        "acct_0198d704-6c00-7000-8000-000000000090",
		CredentialID:     "cred_0198d704-6c00-7000-8000-000000000091",
		Name:             "Health",
		Email:            "health@icloud.com",
		IMAPUsername:     "health@icloud.com",
		CredentialScheme: account.CredentialEnv,
		CredentialRef:    "env:ICLOUD_HEALTH_TEST",
		Now:              createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := createdAt.Add(time.Minute)
	healthy, err := state.Accounts().UpdateHealth(context.Background(), account.HealthUpdate{
		AccountID:        created.ID,
		Status:           account.StatusConnected,
		IMAPUsername:     "health",
		IMAPCapabilities: []string{"IDLE", "IMAP4REV1"},
		SMTPCapabilities: []string{"AUTH", "SMTPUTF8"},
		CheckedAt:        healthyAt,
		Healthy:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if healthy.Status != account.StatusConnected || healthy.IMAPUsername != "health" || healthy.LastHealthyAt == nil || healthy.LastTestedAt == nil || healthy.LastErrorAt != nil || healthy.LastErrorCode != "" {
		t.Fatalf("healthy = %+v", healthy)
	}
	if !healthy.LastHealthyAt.Equal(healthyAt) || !healthy.LastTestedAt.Equal(healthyAt) {
		t.Fatalf("healthy timestamps = %+v", healthy)
	}

	failedAt := healthyAt.Add(time.Minute)
	degraded, err := state.Accounts().UpdateHealth(context.Background(), account.HealthUpdate{
		AccountID:        created.ID,
		Status:           account.StatusDegraded,
		IMAPUsername:     "health",
		IMAPCapabilities: healthy.IMAPCapabilities,
		SMTPCapabilities: healthy.SMTPCapabilities,
		LastErrorCode:    string(transport.StageSMTPAuth),
		CheckedAt:        failedAt,
		Healthy:          false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if degraded.Status != account.StatusDegraded || degraded.LastErrorCode != string(transport.StageSMTPAuth) || degraded.LastErrorAt == nil || degraded.LastTestedAt == nil || degraded.LastHealthyAt == nil {
		t.Fatalf("degraded = %+v", degraded)
	}
	if !degraded.LastErrorAt.Equal(failedAt) || !degraded.LastTestedAt.Equal(failedAt) || !degraded.LastHealthyAt.Equal(healthyAt) {
		t.Fatalf("degraded timestamps = %+v", degraded)
	}
	if strings.Join(degraded.IMAPCapabilities, ",") != "IDLE,IMAP4REV1" || strings.Join(degraded.SMTPCapabilities, ",") != "AUTH,SMTPUTF8" {
		t.Fatalf("capabilities were lost: %+v", degraded)
	}
}

func TestOpenUpgradesPhaseThreeDatabaseWithoutChangingAccountsOrCredentials(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 3, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := database.Exec(`
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
		"acct_0198d704-6c00-7000-8000-000000000095",
		"Legacy",
		"legacy@icloud.com",
		"legacy",
		account.StatusPendingValidation,
		timestamp,
		timestamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)`,
		"cred_0198d704-6c00-7000-8000-000000000096",
		"acct_0198d704-6c00-7000-8000-000000000095",
		account.CredentialEnv,
		"env:ICLOUD_LEGACY_TEST",
		timestamp,
		timestamp,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	value, credential, err := state.Accounts().Credential(context.Background(), "Legacy")
	if err != nil {
		t.Fatal(err)
	}
	if value.ID != "acct_0198d704-6c00-7000-8000-000000000095" || credential.Reference != "env:ICLOUD_LEGACY_TEST" || value.Status != account.StatusPendingValidation {
		t.Fatalf("upgraded values: account=%+v credential=%+v", value, credential)
	}
	if value.IMAPCapabilities == nil || value.SMTPCapabilities == nil || len(value.IMAPCapabilities) != 0 || len(value.SMTPCapabilities) != 0 || value.LastTestedAt != nil {
		t.Fatalf("unexpected health defaults: %+v", value)
	}
}

func TestOpenUpgradesPhaseFourDatabaseWithoutChangingHealth(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 4, 15, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := database.Exec(`
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, last_healthy_at, imap_capabilities_json, smtp_capabilities_json,
    last_tested_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		"acct_0198d704-6c00-7000-8000-000000000097",
		"Phase Four",
		"phase-four@icloud.com",
		"phase-four",
		account.StatusConnected,
		timestamp,
		`["IDLE","IMAP4REV1"]`,
		`["AUTH","SMTPUTF8"]`,
		timestamp,
		timestamp,
		timestamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)`,
		"cred_0198d704-6c00-7000-8000-000000000098",
		"acct_0198d704-6c00-7000-8000-000000000097",
		account.CredentialFile,
		"file:/synthetic/phase-four-password",
		timestamp,
		timestamp,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	value, credential, err := state.Accounts().Credential(context.Background(), "Phase Four")
	if err != nil {
		t.Fatal(err)
	}
	if value.Status != account.StatusConnected || value.LastHealthyAt == nil || value.LastTestedAt == nil ||
		strings.Join(value.IMAPCapabilities, ",") != "IDLE,IMAP4REV1" || strings.Join(value.SMTPCapabilities, ",") != "AUTH,SMTPUTF8" ||
		credential.Reference != "file:/synthetic/phase-four-password" {
		t.Fatalf("phase-four state changed: account=%+v credential=%+v", value, credential)
	}
	folders, more, err := state.Folders().List(context.Background(), value.ID, 20, true)
	if err != nil || more || len(folders) != 0 {
		t.Fatalf("folder defaults len=%d more=%v err=%v", len(folders), more, err)
	}
}

func TestOpenUpgradesPhaseSixDatabaseWithoutChangingMessagesOrLocations(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 5, 20, 0, 0, time.UTC).Format(time.RFC3339Nano)
	accountID := "acct_0198d704-6c00-7000-8000-000000000601"
	folderID := "fld_0198d704-6c00-7000-8000-000000000602"
	messageID := "msg_0198d704-6c00-7000-8000-000000000603"
	locationID := "loc_0198d704-6c00-7000-8000-000000000604"
	hash := strings.Repeat("a", 64)
	if _, err := database.Exec(`
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, 'Phase Six', 'phase-six@icloud.com', 'phase-six', 'connected', 1, ?, ?)`, accountID, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES ('cred_0198d704-6c00-7000-8000-000000000605', ?, 'env', 'env:PHASE_SIX_PASSWORD', ?, ?)`, accountID, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO folders (
    folder_id, account_id, name, wire_name, kind, kind_source, selectable,
    subscription, present, stale, sync_enabled, uid_validity,
    discovered_at, status_at, created_at, updated_at
) VALUES (?, ?, 'INBOX', 'INBOX', 'inbox', 'inbox', 1,
          'unknown', 1, 0, 1, 100, ?, ?, ?, ?)`, folderID, accountID, timestamp, timestamp, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, 128, ?)`, hash, "raw/sha256/aa/"+hash+".eml", timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO messages (
    message_id, account_id, rfc_message_id, direction, from_email,
    subject, snippet, internal_date, sort_at, date_source,
    created_at, updated_at
) VALUES (?, ?, 'phase-six@example.test', 'inbound', 'sender@example.test',
          'Phase Six fixture', 'Legacy preview', ?, ?, 'internal_date', ?, ?)`, messageID, accountID, timestamp, timestamp, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    internal_date, rfc822_size, raw_content_hash, raw_storage_key,
    raw_byte_size, present, created_at, updated_at
) VALUES (?, ?, ?, ?, 100, 42, ?, 128, ?, ?, 128, 1, ?, ?)`,
		locationID, messageID, accountID, folderID, timestamp, hash, "raw/sha256/aa/"+hash+".eml", timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	message, err := state.Mail().Message(context.Background(), accountID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != messageID || len(message.Locations) != 1 || message.Locations[0].ID != locationID ||
		message.Locations[0].RawContentHash != hash || message.Content != nil {
		t.Fatalf("phase-six message changed: %+v", message)
	}
	var contentTables int
	if err := state.db.QueryRowContext(context.Background(), `
SELECT count(*) FROM sqlite_master
WHERE type = 'table' AND name IN ('message_content', 'message_parts', 'attachments')`).Scan(&contentTables); err != nil {
		t.Fatal(err)
	}
	if contentTables != 3 {
		t.Fatalf("content tables = %d", contentTables)
	}
}

func TestOpenUpgradesPhaseSevenContentAsPendingAttachmentIndex(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 6, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	accountID := "acct_0198d704-6c00-7000-8000-000000000701"
	folderID := "fld_0198d704-6c00-7000-8000-000000000702"
	messageID := "msg_0198d704-6c00-7000-8000-000000000703"
	partID := "prt_0198d704-6c00-7000-8000-000000000704"
	hash := strings.Repeat("b", 64)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at)
          VALUES (?, 'Phase Seven', 'phase-seven@icloud.com', 'phase-seven', 'connected', 1, ?, ?)`, []any{accountID, timestamp, timestamp}},
		{`INSERT INTO credential_refs (credential_ref_id, account_id, scheme, reference, created_at, updated_at)
          VALUES ('cred_0198d704-6c00-7000-8000-000000000705', ?, 'env', 'env:PHASE_SEVEN_PASSWORD', ?, ?)`, []any{accountID, timestamp, timestamp}},
		{`INSERT INTO folders (folder_id, account_id, name, wire_name, kind, kind_source, selectable, subscription, present, stale, sync_enabled, uid_validity, discovered_at, status_at, created_at, updated_at)
          VALUES (?, ?, 'INBOX', 'INBOX', 'inbox', 'inbox', 1, 'subscribed', 1, 0, 1, 100, ?, ?, ?, ?)`, []any{folderID, accountID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at) VALUES (?, ?, 64, ?)`, []any{hash, "raw/sha256/bb/" + hash + ".eml", timestamp}},
		{`INSERT INTO messages (message_id, account_id, rfc_message_id, direction, from_email, subject, snippet, attachment_count, internal_date, sort_at, date_source, created_at, updated_at)
          VALUES (?, ?, 'phase-seven@example.test', 'inbound', 'sender@example.test', 'Phase Seven fixture', 'Legacy content', 1, ?, ?, 'internal_date', ?, ?)`, []any{messageID, accountID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO message_locations (location_id, message_id, account_id, folder_id, uid_validity, uid, internal_date, rfc822_size, raw_content_hash, raw_storage_key, raw_byte_size, present, created_at, updated_at)
          VALUES ('loc_0198d704-6c00-7000-8000-000000000706', ?, ?, ?, 100, 7, ?, 64, ?, ?, 64, 1, ?, ?)`, []any{messageID, accountID, folderID, timestamp, hash, "raw/sha256/bb/" + hash + ".eml", timestamp, timestamp}},
		{`INSERT INTO message_content (message_id, headers_json, diagnostics_json, text_paths_json, html_paths_json, parsed_complete, created_at, updated_at)
          VALUES (?, '[]', '[]', '[]', '[]', 1, ?, ?)`, []any{messageID, timestamp, timestamp}},
		{`INSERT INTO message_parts (part_id, message_id, ordinal, path, media_type, disposition, filename, attachment, decoded_size, diagnostics_json, created_at, updated_at)
          VALUES (?, ?, 0, '0', 'application/pdf', 'attachment', 'legacy.pdf', 1, 12, '[]', ?, ?)`, []any{partID, messageID, timestamp, timestamp}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	message, err := state.Mail().Message(context.Background(), accountID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content == nil || message.Content.AttachmentsIndexed || len(message.Content.Attachments) != 0 ||
		len(message.Content.Parts) != 1 || message.Content.Parts[0].ID != partID || message.Content.Parts[0].Filename != "legacy.pdf" {
		t.Fatalf("upgraded phase-seven content = %+v", message.Content)
	}
}

func TestOpenUpgradesPhaseEightLocationFlagsWithoutChangingIdentity(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 6, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	accountID := "acct_0198d704-6c00-7000-8000-000000000801"
	folderID := "fld_0198d704-6c00-7000-8000-000000000802"
	messageID := "msg_0198d704-6c00-7000-8000-000000000803"
	locationID := "loc_0198d704-6c00-7000-8000-000000000804"
	hash := strings.Repeat("d", 64)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at)
          VALUES (?, 'Phase Eight', 'phase-eight@icloud.com', 'phase-eight', 'connected', 1, ?, ?)`, []any{accountID, timestamp, timestamp}},
		{`INSERT INTO credential_refs (credential_ref_id, account_id, scheme, reference, created_at, updated_at)
          VALUES ('cred_0198d704-6c00-7000-8000-000000000805', ?, 'env', 'env:PHASE_EIGHT_PASSWORD', ?, ?)`, []any{accountID, timestamp, timestamp}},
		{`INSERT INTO folders (folder_id, account_id, name, wire_name, kind, kind_source, selectable, subscription, present, stale, sync_enabled, uid_validity, discovered_at, status_at, created_at, updated_at)
          VALUES (?, ?, 'INBOX', 'INBOX', 'inbox', 'inbox', 1, 'subscribed', 1, 0, 1, 100, ?, ?, ?, ?)`, []any{folderID, accountID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at) VALUES (?, ?, 64, ?)`, []any{hash, "raw/sha256/dd/" + hash + ".eml", timestamp}},
		{`INSERT INTO messages (message_id, account_id, rfc_message_id, direction, from_email, subject, snippet, attachment_count, internal_date, sort_at, date_source, created_at, updated_at)
          VALUES (?, ?, 'phase-eight@example.test', 'inbound', 'sender@example.test', 'Phase Eight fixture', 'Legacy flags', 0, ?, ?, 'internal_date', ?, ?)`, []any{messageID, accountID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO message_locations (location_id, message_id, account_id, folder_id, uid_validity, uid, flags_json, internal_date, rfc822_size, raw_content_hash, raw_storage_key, raw_byte_size, present, created_at, updated_at)
          VALUES (?, ?, ?, ?, 100, 8, '["\\Seen","\\Flagged","\\Answered","\\Draft","\\Deleted","$Forwarded","custom"]', ?, 64, ?, ?, 64, 1, ?, ?)`, []any{locationID, messageID, accountID, folderID, timestamp, hash, "raw/sha256/dd/" + hash + ".eml", timestamp, timestamp}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	message, err := state.Mail().Message(context.Background(), accountID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Locations) != 1 {
		t.Fatalf("upgraded locations = %+v", message.Locations)
	}
	location := message.Locations[0]
	if location.ID != locationID || location.MessageID != messageID || location.FolderID != folderID ||
		location.UID != 8 || location.ModSequence != 0 || !location.Present ||
		!location.Flags.Seen || !location.Flags.Flagged || !location.Flags.Answered ||
		!location.Flags.Draft || !location.Flags.Deleted ||
		!reflect.DeepEqual(location.Flags.Unknown, []string{"$Forwarded", "custom"}) {
		t.Fatalf("upgraded location = %+v", location)
	}
	if message.Unread || message.Flagged {
		t.Fatalf("deleted migrated location affected summary = %+v", message)
	}
}

func TestOpenUpgradesPhaseNineMailStateAndAddsIdentityTelemetry(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 7, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	accountID := "acct_0198d704-6c00-7000-8000-000000000951"
	folderID := "fld_0198d704-6c00-7000-8000-000000000952"
	messageID := "msg_0198d704-6c00-7000-8000-000000000953"
	locationID := "loc_0198d704-6c00-7000-8000-000000000954"
	hash := strings.Repeat("e", 64)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at)
          VALUES (?, 'Phase Nine', 'phase-nine@icloud.com', 'phase-nine', 'connected', 1, ?, ?)`, []any{accountID, timestamp, timestamp}},
		{`INSERT INTO credential_refs (credential_ref_id, account_id, scheme, reference, created_at, updated_at)
          VALUES ('cred_0198d704-6c00-7000-8000-000000000955', ?, 'env', 'env:PHASE_NINE_PASSWORD', ?, ?)`, []any{accountID, timestamp, timestamp}},
		{`INSERT INTO folders (folder_id, account_id, name, wire_name, kind, kind_source, selectable, subscription, present, stale, sync_enabled, uid_validity, discovered_at, status_at, created_at, updated_at)
          VALUES (?, ?, 'INBOX', 'INBOX', 'inbox', 'inbox', 1, 'subscribed', 1, 0, 1, 100, ?, ?, ?, ?)`, []any{folderID, accountID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at) VALUES (?, ?, 64, ?)`, []any{hash, "raw/sha256/ee/" + hash + ".eml", timestamp}},
		{`INSERT INTO messages (message_id, account_id, rfc_message_id, direction, from_email, subject, snippet, attachment_count, internal_date, sort_at, date_source, created_at, updated_at)
          VALUES (?, ?, 'phase-nine@example.test', 'inbound', 'sender@example.test', 'Phase Nine fixture', 'Structured flags', 0, ?, ?, 'internal_date', ?, ?)`, []any{messageID, accountID, timestamp, timestamp, timestamp, timestamp}},
		{`INSERT INTO message_locations (
              location_id, message_id, account_id, folder_id, uid_validity, uid,
              seen, flagged, answered, draft, deleted, unknown_flags_json,
              mod_sequence, flags_checked_at, internal_date, rfc822_size,
              raw_content_hash, raw_storage_key, raw_byte_size, present, created_at, updated_at
          ) VALUES (?, ?, ?, ?, 100, 9, 1, 1, 0, 0, 0, '["$PhaseNine"]',
                    '42', ?, ?, 64, ?, ?, 64, 1, ?, ?)`, []any{
			locationID, messageID, accountID, folderID, timestamp, timestamp,
			hash, "raw/sha256/ee/" + hash + ".eml", timestamp, timestamp,
		}},
		{`INSERT INTO message_content (
              message_id, headers_json, diagnostics_json, text_paths_json,
              html_paths_json, parsed_complete, attachments_indexed, created_at, updated_at
          ) VALUES (?, '[]', '[]', '["1"]', '[]', 1, 1, ?, ?)`, []any{messageID, timestamp, timestamp}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	message, err := state.Mail().Message(context.Background(), accountID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != messageID || len(message.Locations) != 1 || message.Locations[0].ID != locationID ||
		message.Locations[0].ModSequence != 42 || !message.Locations[0].Flags.Seen ||
		!message.Locations[0].Flags.Flagged || !reflect.DeepEqual(message.Locations[0].Flags.Unknown, []string{"$PhaseNine"}) ||
		message.Content == nil || message.ContentLocationID != locationID {
		t.Fatalf("upgraded Phase Nine message = %+v", message)
	}
	var searchMessageID, bodyState string
	if err := state.db.QueryRow(`
SELECT message_id, body_state FROM message_search_documents WHERE message_id = ?`, messageID).Scan(&searchMessageID, &bodyState); err != nil {
		t.Fatal(err)
	}
	if searchMessageID != messageID || bodyState != "pending" {
		t.Fatalf("migrated search document id=%q state=%q", searchMessageID, bodyState)
	}
	searchPage, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "Phase Nine fixture", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchPage.Hits) != 1 || searchPage.Hits[0].Message.ID != messageID ||
		searchPage.Hits[0].MatchedField != "subject" || searchPage.Query.IndexState != "pending" {
		t.Fatalf("migrated search page = %+v", searchPage)
	}
	generated := "phase-nine-generated@example.test"
	if err := state.Mail().RegisterGeneratedMessageID(context.Background(), accountID, messageID, generated, time.Date(2026, 8, 6, 7, 31, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	message, err = state.Mail().Message(context.Background(), accountID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Identity) != 1 || message.Identity[0].Kind != maildomain.IdentityGeneratedMessageID || message.Identity[0].Value != generated {
		t.Fatalf("identity telemetry after upgrade = %+v", message.Identity)
	}
}

func TestOpenUpgradesSchemaEightMessagesIntoRFCThreadsWithoutChangingIDs(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 6, 9, 10, 0, 0, time.UTC)
	rootAt := base.Format(time.RFC3339Nano)
	replyAt := base.Add(time.Minute).Format(time.RFC3339Nano)
	accountID := "acct_0198d704-6c00-7000-8000-000000000971"
	rootID := "msg_0198d704-6c00-7000-8000-000000000972"
	replyID := "msg_0198d704-6c00-7000-8000-000000000973"
	if _, err := database.Exec(`
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username,
    status, is_default, created_at, updated_at
) VALUES (?, 'Thread migration', 'thread-migration@icloud.com',
          'thread-migration', 'connected', 1, ?, ?)`, accountID, rootAt, rootAt); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO messages (
    message_id, account_id, rfc_message_id, in_reply_to, direction,
    from_email, subject, snippet, attachment_count,
    internal_date, sort_at, date_source, created_at, updated_at
) VALUES
    (?, ?, 'migration-root@example.test', '', 'inbound',
     'alice@example.test', 'Migration thread', 'Root', 0,
     ?, ?, 'internal_date', ?, ?),
    (?, ?, 'migration-reply@example.test', 'migration-root@example.test', 'inbound',
     'thread-migration@icloud.com', 'Re: Migration thread', 'Reply', 0,
     ?, ?, 'internal_date', ?, ?)`,
		rootID, accountID, rootAt, rootAt, rootAt, rootAt,
		replyID, accountID, replyAt, replyAt, replyAt, replyAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO message_references (message_id, ordinal, reference)
VALUES (?, 0, 'migration-root@example.test')`, replyID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	page, err := state.Threads().Show(context.Background(), accountID, replyID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Thread.ID != provisionalThreadID(rootID) || page.Thread.MessageCount != 2 ||
		page.Thread.RootRFCMessageID != "migration-root@example.test" ||
		!reflect.DeepEqual(messageIDs(page.Thread.Messages), []string{rootID, replyID}) {
		t.Fatalf("migrated thread = %+v", page.Thread)
	}
	var backfill string
	if err := state.db.QueryRow(`SELECT value FROM app_metadata WHERE key = 'thread_graph_backfill_v1'`).Scan(&backfill); err != nil {
		t.Fatal(err)
	}
	if backfill != "complete" {
		t.Fatalf("thread graph backfill state = %q", backfill)
	}
	for _, messageID := range []string{rootID, replyID} {
		var storedID string
		if err := state.db.QueryRow(`SELECT message_id FROM messages WHERE message_id = ?`, messageID).Scan(&storedID); err != nil {
			t.Fatal(err)
		}
		if storedID != messageID {
			t.Fatalf("message ID changed: got %q want %q", storedID, messageID)
		}
	}
}

func TestOpenUpgradesPhaseThirteenStateWithEmptyOutboundLedger(t *testing.T) {
	t.Parallel()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 8, 6, 13, 45, 0, 0, time.UTC).Format(time.RFC3339Nano)
	accountID := "acct_0198d704-6c00-7000-8000-000000001401"
	credentialID := "cred_0198d704-6c00-7000-8000-000000001402"
	if _, err := database.Exec(`
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username,
    status, is_default, created_at, updated_at
) VALUES (?, 'Phase Thirteen', 'phase-thirteen@icloud.com',
          'phase-thirteen', 'connected', 1, ?, ?)`, accountID, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, 'env', 'env:PHASE_THIRTEEN_PASSWORD', ?, ?)`, credentialID, accountID, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version = %d", state.SchemaVersion())
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if accountValue.ID != accountID || accountValue.Name != "Phase Thirteen" || accountValue.Email != "phase-thirteen@icloud.com" {
		t.Fatalf("upgraded account = %+v", accountValue)
	}
	_, credential, err := state.Accounts().Credential(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.ID != credentialID || credential.Reference != "env:PHASE_THIRTEEN_PASSWORD" {
		t.Fatalf("upgraded credential = %+v", credential)
	}
	for _, table := range []string{"outbound_attempts", "idempotency_records", "local_message_sources"} {
		var count int
		if err := state.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d", table, count)
		}
	}
}
