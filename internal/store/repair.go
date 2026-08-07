package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type BlobReference struct {
	Kind        string
	ID          string
	StorageKey  string
	ContentHash string
	ByteSize    int64
}

type RepairAudit struct {
	Summary               maildomain.RepairSummary
	StaleSearchMessageIDs []string
	RepairCheckpoints     []maildomain.Checkpoint
	BackfillResets        []maildomain.BackfillFrontier
}

func (repository *MailRepository) RepairRequiredCheckpoints(ctx context.Context, accountID string, limit int) ([]maildomain.Checkpoint, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list repair-required checkpoints: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, checkpointColumns+`
WHERE account_id = ? AND state = 'repair_required'
ORDER BY updated_at, checkpoint_id
LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list repair-required checkpoints: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Checkpoint, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanCheckpoint(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list repair-required checkpoints: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *MailRepository) ProblemBackfillFrontiers(ctx context.Context, accountID string, limit int) ([]maildomain.BackfillFrontier, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list problematic backfill frontiers: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT f.frontier_id, f.account_id, f.folder_id, f.after_time, f.before_time,
       f.uid_validity, f.before_uid, f.complete, f.last_attempt_at,
       f.last_success_at, f.last_error_code, f.created_at, f.updated_at
FROM mail_backfill_frontiers AS f
LEFT JOIN sync_checkpoints AS c
  ON c.account_id = f.account_id AND c.folder_id = f.folder_id
WHERE f.account_id = ? AND (
  f.last_error_code = 'uid_validity_changed' OR
  (c.checkpoint_id IS NOT NULL AND c.state <> 'repair_required' AND
   c.uid_validity <> 0 AND f.uid_validity <> 0 AND c.uid_validity <> f.uid_validity)
)
ORDER BY f.updated_at, f.frontier_id
LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list problematic backfill frontiers: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.BackfillFrontier, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanBackfillFrontier(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list problematic backfill frontiers: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *SearchRepository) IncompleteCountBounded(ctx context.Context, accountID, state string, limit int) (int, bool, error) {
	if limit < 1 || (state != "pending" && state != "error") {
		return 0, false, errors.New("count incomplete search documents: bounded limit and known state are required")
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT message_id FROM message_search_documents
WHERE account_id = ? AND body_state = ?
ORDER BY message_id LIMIT ?`, strings.TrimSpace(accountID), state, limit+1)
	if err != nil {
		return 0, false, fmt.Errorf("count incomplete search documents: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			return 0, false, fmt.Errorf("count incomplete search documents: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("count incomplete search documents: %w", err)
	}
	more := count > limit
	if more {
		count = limit
	}
	return count, more, nil
}

func (store *Store) AuditMailRepair(ctx context.Context, accountID string, limit int) (RepairAudit, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 || limit > 1000 {
		return RepairAudit{}, errors.New("audit mail repair: account and bounded limit are required")
	}
	audit := RepairAudit{Summary: maildomain.RepairSummary{AccountID: accountID, Mode: maildomain.RepairCheck}}
	add := func(kind, id, folderID, action, detail string, applicable bool) {
		audit.Summary.Findings = append(audit.Summary.Findings, maildomain.RepairFinding{Kind: kind, ID: id, FolderID: folderID, Action: action, Detail: detail, Applicable: applicable})
	}

	pending, pendingMore, err := store.Search().IncompleteCountBounded(ctx, accountID, "pending", limit)
	if err != nil {
		return RepairAudit{}, err
	}
	failed, failedMore, err := store.Search().IncompleteCountBounded(ctx, accountID, "error", limit)
	if err != nil {
		return RepairAudit{}, err
	}
	audit.Summary.PendingSearch, audit.Summary.FailedSearch = pending, failed
	if pendingMore || failedMore {
		audit.Summary.More = true
	}

	stale, more, err := store.Search().StaleMetadata(ctx, accountID, limit)
	if err != nil {
		return RepairAudit{}, err
	}
	audit.StaleSearchMessageIDs = stale
	audit.Summary.StaleFTSRows = len(stale)
	if more {
		audit.Summary.More = true
	}
	for _, messageID := range stale {
		add("stale_search_document", messageID, "", "refresh_search_metadata", "local search metadata differs from the canonical message", true)
	}

	checkpoints, checkpointMore, err := store.Mail().RepairRequiredCheckpoints(ctx, accountID, limit)
	if err != nil {
		return RepairAudit{}, err
	}
	if checkpointMore {
		audit.Summary.More = true
	}
	for _, checkpoint := range checkpoints {
		audit.RepairCheckpoints = append(audit.RepairCheckpoints, checkpoint)
		audit.Summary.RepairCheckpoints++
		add("uid_validity_repair", checkpoint.ID, checkpoint.FolderID, "rebaseline_current_tail", "provider mailbox identity changed; exact folder status is required before the forward frontier can resume", true)
	}

	frontiers, frontierMore, err := store.Mail().ProblemBackfillFrontiers(ctx, accountID, limit)
	if err != nil {
		return RepairAudit{}, err
	}
	if frontierMore {
		audit.Summary.More = true
	}
	for _, frontier := range frontiers {
		audit.BackfillResets = append(audit.BackfillResets, frontier)
		audit.Summary.FrontierProblems++
		detail := "historical frontier belongs to an older provider UID namespace"
		if frontier.LastErrorCode == "uid_validity_changed" {
			detail = "iCloud reported a different UID namespace while resuming historical backfill"
		}
		add("backfill_uid_validity_mismatch", frontier.ID, frontier.FolderID, "reset_backfill_frontier", detail, true)
	}

	foreign, foreignMore, err := foreignKeyFindings(ctx, store.db, limit)
	if err != nil {
		return RepairAudit{}, err
	}
	audit.Summary.ForeignKeyProblems = len(foreign)
	audit.Summary.Findings = append(audit.Summary.Findings, foreign...)
	if foreignMore {
		audit.Summary.More = true
	}

	integrity, integrityMore, err := integrityFindings(ctx, store.db, limit)
	if err != nil {
		return RepairAudit{}, err
	}
	audit.Summary.IntegrityProblems = len(integrity)
	audit.Summary.Findings = append(audit.Summary.Findings, integrity...)
	if integrityMore {
		audit.Summary.More = true
	}

	locations, locationMore, err := scalarIssueFindings(ctx, store.db, `
SELECT printf('%s:%s:%d:%d', account_id, folder_id, uid_validity, uid)
FROM message_locations WHERE account_id = ?
GROUP BY account_id, folder_id, uid_validity, uid HAVING count(*) > 1
LIMIT ?`, accountID, limit+1, "location_uniqueness", "multiple local locations claim one provider UID", false)
	if err != nil {
		return RepairAudit{}, err
	}
	if len(locations) > limit {
		locations = locations[:limit]
		locationMore = true
	}
	audit.Summary.LocationProblems = len(locations)
	audit.Summary.Findings = append(audit.Summary.Findings, locations...)
	if locationMore {
		audit.Summary.More = true
	}

	redirects, redirectMore, err := scalarIssueFindings(ctx, store.db, `
SELECT a.alias_thread_id FROM thread_aliases AS a
JOIN threads AS t ON t.account_id = a.account_id AND t.thread_id = a.alias_thread_id
WHERE a.account_id = ? LIMIT ?`, accountID, limit+1, "thread_redirect_collision", "thread alias shadows an active canonical thread ID", false)
	if err != nil {
		return RepairAudit{}, err
	}
	if len(redirects) > limit {
		redirects = redirects[:limit]
		redirectMore = true
	}
	audit.Summary.ThreadRedirectProblems = len(redirects)
	audit.Summary.Findings = append(audit.Summary.Findings, redirects...)
	if redirectMore {
		audit.Summary.More = true
	}

	dedupeIDs, dedupeMore, err := boundedIDs(ctx, store.db, `
SELECT conflict_id FROM message_dedupe_conflicts
WHERE account_id=? AND status='open'
ORDER BY created_at, conflict_id LIMIT ?`, accountID, limit)
	if err != nil {
		return RepairAudit{}, fmt.Errorf("list dedupe conflicts: %w", err)
	}
	audit.Summary.DedupeConflicts = len(dedupeIDs)
	if dedupeMore {
		audit.Summary.More = true
	}
	for _, conflictID := range dedupeIDs {
		add("dedupe_conflict", conflictID, "", "manual_review", "unresolved identity evidence requires review; repair does not guess a merge", false)
	}

	unknownIDs, unknownMore, err := boundedIDs(ctx, store.db, `
SELECT attempt_id FROM outbound_attempts
WHERE account_id=? AND (status='unknown' OR reconciliation='unknown')
ORDER BY updated_at, attempt_id LIMIT ?`, accountID, limit)
	if err != nil {
		return RepairAudit{}, fmt.Errorf("list outbound unknown attempts: %w", err)
	}
	audit.Summary.OutboundUnknown = len(unknownIDs)
	if unknownMore {
		audit.Summary.More = true
	}
	for _, attemptID := range unknownIDs {
		add("outbound_unknown", attemptID, "", "reconcile_sent_evidence", "outbound attempt needs exact local Sent evidence reconciliation", true)
	}

	audit.Summary.Checked = len(audit.Summary.Findings) + 7
	return audit, nil
}

func (repository *SearchRepository) StaleMetadata(ctx context.Context, accountID string, limit int) ([]string, bool, error) {
	rows, err := repository.db.QueryContext(ctx, `
SELECT d.message_id
FROM message_search_documents AS d
JOIN messages AS m ON m.message_id = d.message_id
WHERE d.account_id = ? AND (
 d.subject <> m.subject OR
 d.sender <> trim(m.from_name || ' ' || m.from_email || ' ' || m.reply_to_name || ' ' || m.reply_to_email) OR
 d.participants <> COALESCE((
   SELECT group_concat(value, ' ') FROM (
     SELECT trim(r.display_name || ' ' || r.email_address) AS value
     FROM message_recipients AS r WHERE r.message_id=m.message_id AND r.kind IN ('to','cc')
     ORDER BY CASE r.kind WHEN 'to' THEN 0 ELSE 1 END, r.ordinal
   )
 ), '') OR
 d.attachment_names <> COALESCE((
   SELECT group_concat(filename, ' ') FROM (
     SELECT a.filename FROM attachments AS a WHERE a.message_id=m.message_id AND a.filename<>'' ORDER BY a.ordinal
   )
 ), '') OR
 d.attachment_types <> COALESCE((
   SELECT group_concat(media_type, ' ') FROM (
     SELECT a.media_type FROM attachments AS a WHERE a.message_id=m.message_id AND a.media_type<>'' ORDER BY a.ordinal
   )
 ), '') OR
 d.rfc_message_id <> m.rfc_message_id
)
ORDER BY d.message_id LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("find stale search documents: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	return ids, more, nil
}

func (repository *SearchRepository) RefreshMetadata(ctx context.Context, accountID string, messageIDs []string, now time.Time) (int, error) {
	if len(messageIDs) == 0 {
		return 0, nil
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin search metadata repair: %w", err)
	}
	defer tx.Rollback()
	changed := 0
	for _, messageID := range messageIDs {
		var storedAccount string
		if err := tx.QueryRowContext(ctx, `SELECT account_id FROM messages WHERE message_id=?`, strings.TrimSpace(messageID)).Scan(&storedAccount); err != nil {
			return 0, err
		}
		if storedAccount != strings.TrimSpace(accountID) {
			return 0, maildomain.ErrMessageNotFound
		}
		if err := refreshMessageSearchDocument(ctx, tx, messageID, nil, nil, "", "", now.UTC().Format(time.RFC3339Nano)); err != nil {
			return 0, err
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit search metadata repair: %w", err)
	}
	return changed, nil
}

func (store *Store) MailBlobReferences(ctx context.Context, accountID string, limit int) ([]BlobReference, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list mail blob references: positive limit required")
	}
	type blobCategory struct {
		kind  string
		query string
	}
	categories := []blobCategory{
		{kind: "message_raw", query: `SELECT min(l.location_id), l.raw_storage_key, l.raw_content_hash, l.raw_byte_size FROM message_locations AS l WHERE l.account_id=? GROUP BY l.raw_storage_key, l.raw_content_hash, l.raw_byte_size ORDER BY min(l.location_id) LIMIT ?`},
		{kind: "local_raw", query: `SELECT min(s.message_id), s.raw_storage_key, s.raw_content_hash, s.raw_byte_size FROM local_message_sources AS s JOIN messages AS m ON m.message_id=s.message_id WHERE m.account_id=? GROUP BY s.raw_storage_key, s.raw_content_hash, s.raw_byte_size ORDER BY min(s.message_id) LIMIT ?`},
		{kind: "attachment", query: `SELECT min(a.attachment_id), a.storage_key, a.content_hash, a.byte_size FROM attachments AS a JOIN messages AS m ON m.message_id=a.message_id WHERE m.account_id=? AND a.storage_key<>'' GROUP BY a.storage_key, a.content_hash, a.byte_size ORDER BY min(a.attachment_id) LIMIT ?`},
		{kind: "outbound_raw", query: `SELECT min(o.attempt_id), o.raw_storage_key, o.raw_content_hash, o.raw_byte_size FROM outbound_attempts AS o WHERE o.account_id=? GROUP BY o.raw_storage_key, o.raw_content_hash, o.raw_byte_size ORDER BY min(o.attempt_id) LIMIT ?`},
		{kind: "outbound_attachment", query: `SELECT min(oa.attempt_id || ':' || oa.ordinal), oa.storage_key, oa.content_hash, oa.byte_size FROM outbound_attempt_attachments AS oa JOIN outbound_attempts AS o ON o.attempt_id=oa.attempt_id WHERE o.account_id=? GROUP BY oa.storage_key, oa.content_hash, oa.byte_size ORDER BY min(oa.attempt_id || ':' || oa.ordinal) LIMIT ?`},
	}
	values := make([]BlobReference, 0, len(categories)*limit)
	more := false
	for _, category := range categories {
		rows, err := store.db.QueryContext(ctx, category.query, strings.TrimSpace(accountID), limit+1)
		if err != nil {
			return nil, false, fmt.Errorf("list %s blob references: %w", category.kind, err)
		}
		categoryValues := make([]BlobReference, 0, limit+1)
		for rows.Next() {
			value := BlobReference{Kind: category.kind}
			if err := rows.Scan(&value.ID, &value.StorageKey, &value.ContentHash, &value.ByteSize); err != nil {
				_ = rows.Close()
				return nil, false, fmt.Errorf("read %s blob reference: %w", category.kind, err)
			}
			categoryValues = append(categoryValues, value)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("list %s blob references: %w", category.kind, err)
		}
		_ = rows.Close()
		if len(categoryValues) > limit {
			categoryValues = categoryValues[:limit]
			more = true
		}
		values = append(values, categoryValues...)
	}
	return values, more, nil
}

func foreignKeyFindings(ctx context.Context, db *sql.DB, limit int) ([]maildomain.RepairFinding, bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, false, fmt.Errorf("check foreign keys: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.RepairFinding, 0, limit+1)
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var fk int
		if err := rows.Scan(&table, &rowID, &parent, &fk); err != nil {
			return nil, false, err
		}
		values = append(values, maildomain.RepairFinding{Kind: "foreign_key", ID: fmt.Sprintf("%s:%d", table, rowID.Int64), Detail: "local relational integrity failure", Applicable: false})
		if len(values) > limit {
			break
		}
	}
	return values[:minInt(len(values), limit)], len(values) > limit, rows.Err()
}

func integrityFindings(ctx context.Context, db *sql.DB, limit int) ([]maildomain.RepairFinding, bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA quick_check(%d)", limit+1))
	if err != nil {
		return nil, false, fmt.Errorf("check database integrity: %w", err)
	}
	defer rows.Close()
	values := []maildomain.RepairFinding{}
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return nil, false, err
		}
		if detail != "ok" {
			values = append(values, maildomain.RepairFinding{Kind: "database_integrity", Detail: "SQLite quick-check reported an integrity problem", Applicable: false})
		}
		if len(values) > limit {
			break
		}
	}
	return values[:minInt(len(values), limit)], len(values) > limit, rows.Err()
}

func scalarIssueFindings(ctx context.Context, db *sql.DB, query string, accountID string, queryLimit int, kind, detail string, applicable bool) ([]maildomain.RepairFinding, bool, error) {
	rows, err := db.QueryContext(ctx, query, accountID, queryLimit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	values := []maildomain.RepairFinding{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		values = append(values, maildomain.RepairFinding{Kind: kind, ID: id, Detail: detail, Applicable: applicable})
	}
	return values, false, rows.Err()
}

func boundedIDs(ctx context.Context, db *sql.DB, query, accountID string, limit int) ([]string, bool, error) {
	rows, err := db.QueryContext(ctx, query, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	values := make([]string, 0, limit+1)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, false, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
