package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"modernc.org/sqlite"
)

var (
	iterationFailureRegister sync.Once
	iterationFailureAt       atomic.Int64
	iterationFailureCalls    atomic.Int64
)

func registerIterationFailureFunction(t *testing.T) {
	t.Helper()
	var registerErr error
	iterationFailureRegister.Do(func() {
		registerErr = sqlite.RegisterScalarFunction("icloud_test_iteration_failure", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			call := iterationFailureCalls.Add(1)
			if failAt := iterationFailureAt.Load(); failAt > 0 && call == failAt {
				return nil, errors.New("synthetic mid-iteration SQLite failure")
			}
			if len(args) == 0 {
				return "", nil
			}
			return args[0], nil
		})
	})
	if registerErr != nil {
		t.Fatal(registerErr)
	}
}

func TestMessageReadSurfacesMidIterationSQLiteErrorInsteadOfTruncatedSuccess(t *testing.T) {
	registerIterationFailureFunction(t)
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 4, 30, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 61001, "rows-err@example.test")
	record.Parsed.To = []maildomain.Address{{Email: "first@example.test"}, {Email: "second@example.test"}}
	if _, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID:     "chk_rows_err_fixture",
		AccountID:        accountID,
		FolderID:         folderID,
		UIDValidity:      100,
		LastCommittedUID: record.UID,
		LastScannedUID:   record.UID,
		Bootstrap:        true,
		AttemptedAt:      now,
		Records:          []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `
CREATE TEMP VIEW message_recipients AS
SELECT message_id, kind, ordinal, display_name,
       icloud_test_iteration_failure(email_address) AS email_address
FROM main.message_recipients`); err != nil {
		t.Fatal(err)
	}
	iterationFailureCalls.Store(0)
	iterationFailureAt.Store(2)
	t.Cleanup(func() {
		iterationFailureAt.Store(0)
		iterationFailureCalls.Store(0)
	})
	_, err := state.Mail().Message(ctx, accountID, record.MessageID)
	if err == nil {
		t.Fatal("mid-iteration SQLite error was converted into a truncated successful message read")
	}
	if !strings.Contains(err.Error(), "message recipients") && !strings.Contains(err.Error(), "mid-iteration") {
		t.Fatalf("unexpected iteration error: %v", err)
	}
}

func TestAccountPageSurfacesMidIterationSQLiteErrorInsteadOfTruncatedSuccess(t *testing.T) {
	registerIterationFailureFunction(t)
	state, _, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 4, 45, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for i := 0; i < 3; i++ {
		if _, err := state.db.ExecContext(ctx, `
INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at)
VALUES (?, ?, ?, ?, 'pending_validation', 0, ?, ?)`,
			"acct_iteration_"+string(rune('a'+i)), "Iteration "+string(rune('A'+i)), "iteration-"+string(rune('a'+i))+"@example.test", "iteration", now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.db.ExecContext(ctx, `
CREATE TEMP VIEW accounts AS
SELECT account_id,
       icloud_test_iteration_failure(friendly_name) AS friendly_name,
       email_address, imap_username, status, is_default,
       last_error_code, last_error_at, last_healthy_at,
       created_at, updated_at, imap_capabilities_json,
       smtp_capabilities_json, last_tested_at
FROM main.accounts`); err != nil {
		t.Fatal(err)
	}
	iterationFailureCalls.Store(0)
	iterationFailureAt.Store(2)
	t.Cleanup(func() {
		iterationFailureAt.Store(0)
		iterationFailureCalls.Store(0)
	})
	if _, _, _, err := state.Accounts().ListPage(ctx, 10, ""); err == nil {
		t.Fatal("mid-iteration SQLite error was converted into a truncated successful account page")
	}
}
