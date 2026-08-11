package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/config"
	"github.com/pressly/goose/v3"
)

func TestAgentParityPhase35Schema19UpgradePreservesEveryStableMailObjectID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t, filepath.Join(t.TempDir(), "state"))
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(ctx, database); err != nil {
		database.Close()
		t.Fatal(err)
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrationFS, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 19); err != nil {
		database.Close()
		t.Fatal(err)
	}

	stamp := time.Date(2026, 8, 11, 7, 15, 0, 0, time.UTC).Format(time.RFC3339Nano)
	const (
		accountID    = "acct_019fda7b-3ba1-7000-8000-000000000035"
		folderID     = "fld_019fda7b-3ba1-7000-8000-000000000035"
		messageID    = "msg_019fda7b-3ba1-7000-8000-000000000035"
		locationID   = "loc_019fda7b-3ba1-7000-8000-000000000035"
		threadID     = "thr_019fda7b-3ba1-7000-8000-000000000035"
		partID       = "prt_019fda7b-3ba1-7000-8000-000000000035"
		attachmentID = "att_019fda7b-3ba1-7000-8000-000000000035"
		draftID      = "drf_019fda7b-3ba1-7000-8000-000000000035"
		attemptID    = "send_019fda7b-3ba1-7000-8000-000000000035"
	)
	rawHash := strings.Repeat("a", 64)
	attachmentHash := strings.Repeat("b", 64)
	requestHash := strings.Repeat("c", 64)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at) VALUES (?, 'Phase 35 v19', 'phase35-v19@example.test', 'phase35-v19', 'connected', 1, ?, ?)`, []any{accountID, stamp, stamp}},
		{`INSERT INTO credential_refs (credential_ref_id, account_id, scheme, reference, created_at, updated_at) VALUES ('cred_019fda7b-3ba1-7000-8000-000000000035', ?, 'env', 'PHASE35_V19_TEST_SECRET', ?, ?)`, []any{accountID, stamp, stamp}},
		{`INSERT INTO folders (folder_id, account_id, name, wire_name, kind, kind_source, selectable, present, sync_enabled, discovered_at, created_at, updated_at) VALUES (?, ?, 'Inbox', 'INBOX', 'inbox', 'fixture', 1, 1, 1, ?, ?, ?)`, []any{folderID, accountID, stamp, stamp, stamp}},
		{`INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at) VALUES (?, 'aa/phase35-v19.eml', 35, ?)`, []any{rawHash, stamp}},
		{`INSERT INTO messages (message_id, account_id, rfc_message_id, direction, from_name, from_email, subject, snippet, header_date, internal_date, sort_at, date_source, attachment_count, created_at, updated_at) VALUES (?, ?, '<phase35-v19@example.test>', 'inbound', 'Fixture', 'fixture@example.test', 'Phase 35 v19 migration', 'stable IDs', ?, ?, ?, 'header', 1, ?, ?)`, []any{messageID, accountID, stamp, stamp, stamp, stamp, stamp}},
		{`INSERT INTO message_locations (location_id, message_id, account_id, folder_id, uid_validity, uid, seen, flagged, answered, draft, deleted, unknown_flags_json, mod_sequence, flags_checked_at, internal_date, rfc822_size, raw_content_hash, raw_storage_key, raw_byte_size, present, created_at, updated_at) VALUES (?, ?, ?, ?, 35, 35, 0, 1, 0, 0, 0, '[]', '35', ?, ?, 35, ?, 'aa/phase35-v19.eml', 35, 1, ?, ?)`, []any{locationID, messageID, accountID, folderID, stamp, stamp, rawHash, stamp, stamp}},
		{`INSERT INTO message_content (message_id, headers_json, diagnostics_json, text_paths_json, html_paths_json, parsed_complete, source_location_id, attachments_indexed, created_at, updated_at) VALUES (?, '[]', '[]', '["1"]', '[]', 1, ?, 1, ?, ?)`, []any{messageID, locationID, stamp, stamp}},
		{`INSERT INTO message_parts (part_id, message_id, ordinal, path, media_type, charset, transfer_encoding, disposition, filename, content_id, inline, attachment, embedded_message, decoded_size, diagnostics_json, media_params_json, created_at, updated_at) VALUES (?, ?, 0, '1', 'text/plain', 'utf-8', '7bit', 'attachment', 'phase35.txt', '', 0, 1, 0, 35, '[]', '{}', ?, ?)`, []any{partID, messageID, stamp, stamp}},
		{`INSERT INTO attachments (attachment_id, message_id, part_id, ordinal, part_path, filename, media_type, media_params_json, disposition, content_id, inline, content_hash, storage_key, byte_size, availability, created_at, updated_at) VALUES (?, ?, ?, 0, '1', 'phase35.txt', 'text/plain', '{}', 'attachment', '', 0, ?, 'bb/phase35.txt', 35, 'available', ?, ?)`, []any{attachmentID, messageID, partID, attachmentHash, stamp, stamp}},
		{`INSERT INTO threads (thread_id, account_id, root_rfc_message_id, canonical_subject, snippet, participants_json, folder_ids_json, message_count, unread_count, attachment_count, first_message_at, last_message_at, created_at, updated_at) VALUES (?, ?, '<phase35-v19@example.test>', 'phase 35 v19 migration', 'stable IDs', '["fixture@example.test"]', ?, 1, 1, 1, ?, ?, ?, ?)`, []any{threadID, accountID, `["` + folderID + `"]`, stamp, stamp, stamp, stamp}},
		{`INSERT INTO thread_messages (thread_id, message_id, joined_at) VALUES (?, ?, ?)`, []any{threadID, messageID, stamp}},
		{`INSERT INTO outbound_attempts (attempt_id, account_id, proposed_message_id, request_hash, idempotency_key, status, rfc_message_id, raw_content_hash, raw_storage_key, raw_byte_size, to_count, body_part_count, message_id, thread_id, reconciliation, attachment_count, attachment_byte_size, retry_of_attempt_id, reconciled_location_id, reconciliation_error_code, outbound_kind, parent_message_id, source_message_id, created_at, updated_at) VALUES (?, ?, 'msg_019fda7b-3ba1-7000-8000-000000000036', ?, 'phase35-v19-idempotency', 'sent', '<phase35-v19-outbound@example.test>', ?, 'aa/phase35-v19.eml', 35, 1, 1, ?, ?, 'confirmed', 0, 0, '', ?, '', 'send', '', '', ?, ?)`, []any{attemptID, accountID, requestHash, rawHash, messageID, threadID, locationID, stamp, stamp}},
		{`INSERT INTO mail_drafts (draft_id, account_id, message_id, revision, sync_state, remote_location_id, previous_location_id, outbound_attempt_id, custom_headers_json, last_error_code, created_at, updated_at) VALUES (?, ?, ?, 3, 'cleanup_pending', ?, '', ?, '[]', '', ?, ?)`, []any{draftID, accountID, messageID, locationID, attemptID, stamp, stamp}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			database.Close()
			t.Fatalf("seed schema-19 stable-ID fixture: %v\nquery: %s", err, statement.query)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.SchemaVersion() != SchemaVersion {
		t.Fatalf("schema version=%d want=%d", state.SchemaVersion(), SchemaVersion)
	}
	for _, check := range []struct {
		table  string
		column string
		id     string
	}{
		{"accounts", "account_id", accountID},
		{"folders", "folder_id", folderID},
		{"messages", "message_id", messageID},
		{"message_locations", "location_id", locationID},
		{"threads", "thread_id", threadID},
		{"attachments", "attachment_id", attachmentID},
		{"mail_drafts", "draft_id", draftID},
		{"outbound_attempts", "attempt_id", attemptID},
	} {
		var actual string
		query := `SELECT ` + check.column + ` FROM ` + check.table + ` WHERE ` + check.column + ` = ?`
		if err := state.db.QueryRowContext(ctx, query, check.id).Scan(&actual); err != nil {
			t.Fatalf("stable ID %s.%s=%q missing after v19->v%d upgrade: %v", check.table, check.column, check.id, SchemaVersion, err)
		}
		if actual != check.id {
			t.Fatalf("stable ID %s.%s changed: got=%q want=%q", check.table, check.column, actual, check.id)
		}
	}
	var integrity string
	if err := state.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check=%q", integrity)
	}
}

func TestAgentParityPhase35LargeOutboundHistoryStaysPagedAndIndexed(t *testing.T) {
	t.Parallel()
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	stamp := time.Date(2026, 8, 11, 7, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	identityID := primaryMailIdentityID(accountID)
	rawHash := strings.Repeat("d", 64)
	if _, err := state.db.ExecContext(ctx, `INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at) VALUES (?, 'raw/phase35-scale.eml', 1, ?)`, rawHash, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `
WITH RECURSIVE seq(n) AS (
  SELECT 1
  UNION ALL
  SELECT n + 1 FROM seq WHERE n < 1001
)
INSERT INTO outbound_attempts (
  attempt_id, account_id, proposed_message_id, request_hash, status,
  rfc_message_id, raw_content_hash, raw_storage_key, raw_byte_size,
  sender_identity_id, created_at, updated_at
)
SELECT
  printf('send_phase35_scale_%04d', n), ?, printf('msg_phase35_scale_%04d', n),
  lower(hex(randomblob(32))), 'pending', printf('phase35-scale-%04d@example.test', n),
  ?, 'raw/phase35-scale.eml', 1,
  ?, ?, ?
FROM seq`, accountID, rawHash, identityID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{}, 1001)
	cursor := ""
	for {
		values, more, next, err := state.Outbound().ListPage(ctx, accountID, 37, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(values) > 37 {
			t.Fatalf("outbound page exceeded bound: %d", len(values))
		}
		for _, value := range values {
			if _, duplicate := seen[value.ID]; duplicate {
				t.Fatalf("duplicate outbound attempt across pages: %s", value.ID)
			}
			seen[value.ID] = struct{}{}
		}
		if !more {
			if next != "" {
				t.Fatalf("terminal outbound page returned cursor %q", next)
			}
			break
		}
		if next == "" {
			t.Fatal("non-terminal outbound page omitted cursor")
		}
		cursor = next
	}
	if len(seen) != 1001 {
		t.Fatalf("enumerated %d outbound attempts, want 1001", len(seen))
	}
	rows, err := state.db.QueryContext(ctx, `EXPLAIN QUERY PLAN
SELECT attempt_id
FROM outbound_attempts
WHERE account_id = ?
ORDER BY created_at DESC, attempt_id DESC
LIMIT 38`, accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "outbound_attempts_account_created_idx") {
		t.Fatalf("outbound page query lost account/order index:\n%s", plan)
	}
	t.Logf("enumerated %d attempts in bounded pages; query plan uses outbound_attempts_account_created_idx", len(seen))
}
