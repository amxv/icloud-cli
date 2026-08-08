package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type SearchRepository struct {
	db *sql.DB
}

const maxSearchExcerptRunes = 240

func refreshMessageSearchDocument(
	ctx context.Context,
	tx *sql.Tx,
	messageID string,
	body *string,
	truncated *bool,
	bodyState, bodyError, timestamp string,
) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("refresh search document: message ID is required")
	}
	var fallbackSnippet string
	if err := tx.QueryRowContext(ctx, `SELECT snippet FROM messages WHERE message_id = ?`, messageID).Scan(&fallbackSnippet); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ErrMessageNotFound
		}
		return fmt.Errorf("resolve search document message: %w", err)
	}

	storedBody, storedState, storedError := fallbackSnippet, "pending", ""
	storedTruncated := false
	var storedTruncatedInt int
	err := tx.QueryRowContext(ctx, `
SELECT body, body_state, body_truncated, body_error_code
FROM message_search_documents WHERE message_id = ?`, messageID).Scan(
		&storedBody, &storedState, &storedTruncatedInt, &storedError,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read existing search document: %w", err)
	}
	storedTruncated = storedTruncatedInt == 1
	if body != nil {
		storedBody = *body
		storedState = "complete"
		storedError = ""
		storedTruncated = truncated != nil && *truncated
	}
	if strings.TrimSpace(bodyState) != "" {
		storedState = strings.TrimSpace(bodyState)
	}
	if storedState == "complete" {
		storedError = ""
	} else if bodyError != "" {
		storedError = strings.TrimSpace(bodyError)
	}
	if timestamp == "" {
		timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_search_documents (
    message_id, account_id, subject, sender, participants, body,
    attachment_names, attachment_types, rfc_message_id,
    body_state, body_truncated, body_error_code, indexed_at
)
SELECT
    m.message_id,
    m.account_id,
    m.subject,
    trim(m.from_name || ' ' || m.from_email || ' ' || m.reply_to_name || ' ' || m.reply_to_email),
    COALESCE((
        SELECT group_concat(value, ' ')
        FROM (
            SELECT trim(r.display_name || ' ' || r.email_address) AS value
            FROM message_recipients AS r
            WHERE r.message_id = m.message_id AND r.kind IN ('to', 'cc')
            ORDER BY CASE r.kind WHEN 'to' THEN 0 ELSE 1 END, r.ordinal
        )
    ), ''),
    ?,
    COALESCE((
        SELECT group_concat(filename, ' ')
        FROM (
            SELECT a.filename
            FROM attachments AS a
            WHERE a.message_id = m.message_id AND a.filename <> ''
            ORDER BY a.ordinal
        )
    ), ''),
    COALESCE((
        SELECT group_concat(media_type, ' ')
        FROM (
            SELECT a.media_type
            FROM attachments AS a
            WHERE a.message_id = m.message_id AND a.media_type <> ''
            ORDER BY a.ordinal
        )
    ), ''),
    m.rfc_message_id,
    ?, ?, ?, ?
FROM messages AS m
WHERE m.message_id = ?
ON CONFLICT(message_id) DO UPDATE SET
    account_id = excluded.account_id,
    subject = excluded.subject,
    sender = excluded.sender,
    participants = excluded.participants,
    body = excluded.body,
    attachment_names = excluded.attachment_names,
    attachment_types = excluded.attachment_types,
    rfc_message_id = excluded.rfc_message_id,
    body_state = excluded.body_state,
    body_truncated = excluded.body_truncated,
    body_error_code = excluded.body_error_code,
    indexed_at = excluded.indexed_at`,
		storedBody, storedState, boolInt(storedTruncated), storedError, timestamp, messageID,
	); err != nil {
		return fmt.Errorf("refresh search document: %w", err)
	}
	return nil
}

func (repository *SearchRepository) Search(ctx context.Context, accountID string, options maildomain.SearchOptions) (maildomain.SearchPage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return maildomain.SearchPage{}, errors.New("search mail: account ID is required")
	}
	options = normalizeSearchOptions(options)
	if options.Limit < 1 {
		return maildomain.SearchPage{}, errors.New("search mail: limit must be positive")
	}
	if options.ResultKind != maildomain.SearchMessages && options.ResultKind != maildomain.SearchThreads {
		return maildomain.SearchPage{}, errors.New("search mail: result kind must be messages or threads")
	}
	if options.After != nil && options.Before != nil && !options.After.Before(*options.Before) {
		return maildomain.SearchPage{}, errors.New("search mail: after must precede before")
	}
	signature, err := searchSignature(accountID, options)
	if err != nil {
		return maildomain.SearchPage{}, err
	}
	cursor, err := decodeSearchCursor(options.Cursor, accountID, options.ResultKind, signature)
	if err != nil {
		return maildomain.SearchPage{}, err
	}
	var page maildomain.SearchPage
	switch options.ResultKind {
	case maildomain.SearchMessages:
		page, err = repository.searchMessages(ctx, accountID, options, signature, cursor)
	case maildomain.SearchThreads:
		page, err = repository.searchThreads(ctx, accountID, options, signature, cursor)
	}
	if err != nil {
		return maildomain.SearchPage{}, err
	}
	page.Query, err = repository.queryMeta(ctx, accountID, options)
	if err != nil {
		return maildomain.SearchPage{}, err
	}
	return page, nil
}

func normalizeSearchOptions(options maildomain.SearchOptions) maildomain.SearchOptions {
	options.Query = strings.TrimSpace(options.Query)
	options.FolderID = strings.TrimSpace(options.FolderID)
	options.Sender = strings.TrimSpace(options.Sender)
	options.Participant = strings.TrimSpace(options.Participant)
	options.Subject = strings.TrimSpace(options.Subject)
	options.RFCMessageID = strings.Trim(strings.TrimSpace(options.RFCMessageID), "<>")
	options.AttachmentFilename = strings.TrimSpace(options.AttachmentFilename)
	options.AttachmentType = strings.ToLower(strings.TrimSpace(options.AttachmentType))
	options.Cursor = strings.TrimSpace(options.Cursor)
	if options.ResultKind == "" {
		options.ResultKind = maildomain.SearchMessages
	}
	if options.After != nil {
		value := options.After.UTC()
		options.After = &value
	}
	if options.Before != nil {
		value := options.Before.UTC()
		options.Before = &value
	}
	return options
}

func (repository *SearchRepository) searchMessages(
	ctx context.Context,
	accountID string,
	options maildomain.SearchOptions,
	signature string,
	cursor storedSearchCursor,
) (maildomain.SearchPage, error) {
	query, arguments := messageSearchSQL(accountID, options, cursor)
	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return maildomain.SearchPage{}, fmt.Errorf("search local messages: %w", err)
	}
	defer rows.Close()
	hits := make([]maildomain.SearchHit, 0, options.Limit+1)
	for rows.Next() {
		var rank float64
		var excerpt, matchedField string
		message, scanErr := scanMessageWithExtra(rows, &rank, &excerpt, &matchedField)
		if scanErr != nil {
			return maildomain.SearchPage{}, scanErr
		}
		hits = append(hits, maildomain.SearchHit{
			Kind: maildomain.SearchMessages, Message: &message, Rank: rank,
			MatchedField: matchedField, Excerpt: boundedSearchExcerpt(excerpt, message.Snippet),
		})
	}
	if err := rows.Err(); err != nil {
		return maildomain.SearchPage{}, fmt.Errorf("search local messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return maildomain.SearchPage{}, fmt.Errorf("close local message search rows: %w", err)
	}
	for index := range hits {
		if err := repository.loadSearchMessageDetails(ctx, hits[index].Message); err != nil {
			return maildomain.SearchPage{}, err
		}
	}
	more := len(hits) > options.Limit
	if more {
		hits = hits[:options.Limit]
	}
	next := ""
	if more && len(hits) > 0 {
		last := hits[len(hits)-1]
		next, err = encodeSearchCursor(
			accountID, options.ResultKind, signature, last.Rank,
			searchMessageSortAt(*last.Message), last.Message.ID,
		)
		if err != nil {
			return maildomain.SearchPage{}, err
		}
	}
	return maildomain.SearchPage{Hits: hits, More: more, NextCursor: next}, nil
}

func (repository *SearchRepository) searchThreads(
	ctx context.Context,
	accountID string,
	options maildomain.SearchOptions,
	signature string,
	cursor storedSearchCursor,
) (maildomain.SearchPage, error) {
	query, arguments := threadSearchSQL(accountID, options, cursor)
	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return maildomain.SearchPage{}, fmt.Errorf("search local threads: %w", err)
	}
	defer rows.Close()
	hits := make([]maildomain.SearchHit, 0, options.Limit+1)
	for rows.Next() {
		var rank float64
		var excerpt, matchedField, matchedMessageID string
		thread, scanErr := scanThreadWithExtra(rows, &rank, &excerpt, &matchedField, &matchedMessageID)
		if scanErr != nil {
			return maildomain.SearchPage{}, scanErr
		}
		hits = append(hits, maildomain.SearchHit{
			Kind: maildomain.SearchThreads, Thread: &thread, Rank: rank,
			MatchedMessageID: matchedMessageID, MatchedField: matchedField,
			Excerpt: boundedSearchExcerpt(excerpt, thread.Snippet),
		})
	}
	if err := rows.Err(); err != nil {
		return maildomain.SearchPage{}, fmt.Errorf("search local threads: %w", err)
	}
	more := len(hits) > options.Limit
	if more {
		hits = hits[:options.Limit]
	}
	projected := make([]maildomain.Thread, 0, len(hits))
	for _, hit := range hits {
		if hit.Thread != nil {
			projected = append(projected, *hit.Thread)
		}
	}
	if err := applyThreadProjectionState(ctx, repository.db, accountID, projected); err != nil {
		return maildomain.SearchPage{}, err
	}
	projectedIndex := 0
	for index := range hits {
		if hits[index].Thread == nil {
			continue
		}
		value := projected[projectedIndex]
		hits[index].Thread = &value
		projectedIndex++
	}
	next := ""
	if more && len(hits) > 0 {
		last := hits[len(hits)-1]
		next, err = encodeSearchCursor(
			accountID, options.ResultKind, signature, last.Rank,
			searchThreadSortAt(*last.Thread), last.Thread.ID,
		)
		if err != nil {
			return maildomain.SearchPage{}, err
		}
	}
	return maildomain.SearchPage{Hits: hits, More: more, NextCursor: next}, nil
}

func messageSearchSQL(accountID string, options maildomain.SearchOptions, cursor storedSearchCursor) (string, []any) {
	where, filterArgs := structuredSearchWhere(options, "m")
	cursorClause, cursorArgs := searchCursorClause(cursor, "search_rank", "COALESCE(m.sort_at, m.created_at)", "m.message_id")
	matchedField := firstStructuredMatchField(options)
	if options.Query == "" {
		query := `SELECT ` + messageColumnList + `,
       0.0 AS search_rank, m.snippet, ?
FROM messages AS m INDEXED BY messages_account_sort_idx
WHERE m.account_id = ?` + where + strings.ReplaceAll(cursorClause, "search_rank", "0.0") + `
ORDER BY COALESCE(m.sort_at, m.created_at) DESC, m.message_id DESC
LIMIT ?`
		arguments := []any{matchedField, accountID}
		arguments = append(arguments, filterArgs...)
		arguments = append(arguments, cursorArgs...)
		arguments = append(arguments, options.Limit+1)
		return query, arguments
	}
	query := `WITH matched AS (
    SELECT d.message_id,
           bm25(message_search_fts, 5.0, 3.0, 3.0, 1.0, 2.0, 1.0, 4.0) AS rank,
           snippet(message_search_fts, -1, '[', ']', ' … ', 18) AS excerpt,
           CASE
             WHEN instr(highlight(message_search_fts, 0, char(1), char(2)), char(1)) > 0 THEN 'subject'
             WHEN instr(highlight(message_search_fts, 1, char(1), char(2)), char(1)) > 0 THEN 'sender'
             WHEN instr(highlight(message_search_fts, 2, char(1), char(2)), char(1)) > 0 THEN 'participant'
             WHEN instr(highlight(message_search_fts, 3, char(1), char(2)), char(1)) > 0 THEN 'body'
             WHEN instr(highlight(message_search_fts, 4, char(1), char(2)), char(1)) > 0 THEN 'attachment_filename'
             WHEN instr(highlight(message_search_fts, 5, char(1), char(2)), char(1)) > 0 THEN 'attachment_type'
             WHEN instr(highlight(message_search_fts, 6, char(1), char(2)), char(1)) > 0 THEN 'rfc_message_id'
             ELSE 'text'
           END AS matched_field
    FROM message_search_fts
    JOIN message_search_documents AS d ON d.rowid = message_search_fts.rowid
    WHERE message_search_fts MATCH ? AND d.account_id = ?
)
SELECT ` + messageColumnList + `,
       matched.rank AS search_rank, matched.excerpt, matched.matched_field
FROM matched
JOIN messages AS m ON m.message_id = matched.message_id
WHERE m.account_id = ?` + where + strings.ReplaceAll(cursorClause, "search_rank", "matched.rank") + `
ORDER BY matched.rank, COALESCE(m.sort_at, m.created_at) DESC, m.message_id DESC
LIMIT ?`
	arguments := []any{plainFTSQuery(options.Query), accountID, accountID}
	arguments = append(arguments, filterArgs...)
	arguments = append(arguments, cursorArgs...)
	arguments = append(arguments, options.Limit+1)
	return query, arguments
}

func threadSearchSQL(accountID string, options maildomain.SearchOptions, cursor storedSearchCursor) (string, []any) {
	if options.Query == "" && searchFilterCount(options) == 0 {
		cursorClause, cursorArgs := searchCursorClause(cursor, "0.0", "COALESCE(t.last_message_at, t.created_at)", "t.thread_id")
		arguments := []any{accountID}
		arguments = append(arguments, cursorArgs...)
		arguments = append(arguments, options.Limit+1)
		return `SELECT ` + prefixedThreadColumns("t") + `,
       0.0, '', '', ''
FROM threads AS t
WHERE t.account_id = ?` + cursorClause + `
ORDER BY COALESCE(t.last_message_at, t.created_at) DESC, t.thread_id DESC
LIMIT ?`, arguments
	}

	where, filterArgs := structuredSearchWhere(options, "m")
	cursorClause, cursorArgs := searchCursorClause(cursor, "best.rank", "COALESCE(t.last_message_at, t.created_at)", "t.thread_id")
	if options.Query == "" {
		query := `WITH ranked_threads AS (
    SELECT tm.thread_id, m.message_id, 0.0 AS rank, m.snippet AS excerpt,
           ? AS matched_field,
           ROW_NUMBER() OVER (
               PARTITION BY tm.thread_id
               ORDER BY COALESCE(m.sort_at, m.created_at) DESC, m.message_id DESC
           ) AS match_order
    FROM messages AS m
    JOIN thread_messages AS tm ON tm.message_id = m.message_id
    WHERE m.account_id = ?` + where + `
),
best AS (
    SELECT thread_id, message_id, rank, excerpt, matched_field
    FROM ranked_threads WHERE match_order = 1
)
SELECT ` + prefixedThreadColumns("t") + `,
       best.rank, best.excerpt, best.matched_field, best.message_id
FROM best
JOIN threads AS t ON t.thread_id = best.thread_id
WHERE t.account_id = ?` + cursorClause + `
ORDER BY best.rank, COALESCE(t.last_message_at, t.created_at) DESC, t.thread_id DESC
LIMIT ?`
		arguments := []any{firstStructuredMatchField(options), accountID}
		arguments = append(arguments, filterArgs...)
		arguments = append(arguments, accountID)
		arguments = append(arguments, cursorArgs...)
		arguments = append(arguments, options.Limit+1)
		return query, arguments
	}

	query := `WITH matched AS (
    SELECT d.message_id,
           bm25(message_search_fts, 5.0, 3.0, 3.0, 1.0, 2.0, 1.0, 4.0) AS rank,
           snippet(message_search_fts, -1, '[', ']', ' … ', 18) AS excerpt,
           CASE
             WHEN instr(highlight(message_search_fts, 0, char(1), char(2)), char(1)) > 0 THEN 'subject'
             WHEN instr(highlight(message_search_fts, 1, char(1), char(2)), char(1)) > 0 THEN 'sender'
             WHEN instr(highlight(message_search_fts, 2, char(1), char(2)), char(1)) > 0 THEN 'participant'
             WHEN instr(highlight(message_search_fts, 3, char(1), char(2)), char(1)) > 0 THEN 'body'
             WHEN instr(highlight(message_search_fts, 4, char(1), char(2)), char(1)) > 0 THEN 'attachment_filename'
             WHEN instr(highlight(message_search_fts, 5, char(1), char(2)), char(1)) > 0 THEN 'attachment_type'
             WHEN instr(highlight(message_search_fts, 6, char(1), char(2)), char(1)) > 0 THEN 'rfc_message_id'
             ELSE 'text'
           END AS matched_field
    FROM message_search_fts
    JOIN message_search_documents AS d ON d.rowid = message_search_fts.rowid
    WHERE message_search_fts MATCH ? AND d.account_id = ?
),
ranked_threads AS (
    SELECT tm.thread_id, m.message_id, matched.rank, matched.excerpt,
           matched.matched_field,
           ROW_NUMBER() OVER (
               PARTITION BY tm.thread_id
               ORDER BY matched.rank, COALESCE(m.sort_at, m.created_at) DESC, m.message_id DESC
           ) AS match_order
    FROM matched
    JOIN messages AS m ON m.message_id = matched.message_id
    JOIN thread_messages AS tm ON tm.message_id = m.message_id
    WHERE m.account_id = ?` + where + `
),
best AS (
    SELECT thread_id, message_id, rank, excerpt, matched_field
    FROM ranked_threads WHERE match_order = 1
)
SELECT ` + prefixedThreadColumns("t") + `,
       best.rank, best.excerpt, best.matched_field, best.message_id
FROM best
JOIN threads AS t ON t.thread_id = best.thread_id
WHERE t.account_id = ?` + cursorClause + `
ORDER BY best.rank, COALESCE(t.last_message_at, t.created_at) DESC, t.thread_id DESC
LIMIT ?`
	arguments := []any{plainFTSQuery(options.Query), accountID, accountID}
	arguments = append(arguments, filterArgs...)
	arguments = append(arguments, accountID)
	arguments = append(arguments, cursorArgs...)
	arguments = append(arguments, options.Limit+1)
	return query, arguments
}

func structuredSearchWhere(options maildomain.SearchOptions, alias string) (string, []any) {
	prefix := strings.TrimSpace(alias) + "."
	clauses := []string{}
	arguments := []any{}
	if options.FolderID != "" {
		clauses = append(clauses, `EXISTS (
    SELECT 1 FROM message_locations AS sl
    WHERE sl.message_id = `+prefix+`message_id AND sl.account_id = `+prefix+`account_id
      AND sl.folder_id = ? AND sl.present = 1 AND sl.deleted = 0
)`)
		arguments = append(arguments, options.FolderID)
	}
	if options.Unread != nil {
		exists := `EXISTS (
    SELECT 1 FROM message_locations AS su
    JOIN folders AS sf ON sf.folder_id = su.folder_id
    WHERE su.message_id = ` + prefix + `message_id AND ` + unreadLocationSQL("su", "sf") + `
)`
		if !*options.Unread {
			exists = "NOT " + exists
		}
		clauses = append(clauses, exists)
	}
	if options.Flagged != nil {
		exists := `EXISTS (
    SELECT 1 FROM message_locations AS sg
    WHERE sg.message_id = ` + prefix + `message_id AND sg.present = 1 AND sg.deleted = 0
      AND sg.flagged = 1
)`
		if !*options.Flagged {
			exists = "NOT " + exists
		}
		clauses = append(clauses, exists)
	}
	if options.Sender != "" {
		clauses = append(clauses, `(instr(lower(`+prefix+`from_email), lower(?)) > 0
    OR instr(lower(`+prefix+`from_name), lower(?)) > 0
    OR instr(lower(`+prefix+`reply_to_email), lower(?)) > 0
    OR instr(lower(`+prefix+`reply_to_name), lower(?)) > 0)`)
		arguments = append(arguments, options.Sender, options.Sender, options.Sender, options.Sender)
	}
	if options.Participant != "" {
		clauses = append(clauses, `(instr(lower(`+prefix+`from_email), lower(?)) > 0
    OR instr(lower(`+prefix+`reply_to_email), lower(?)) > 0
    OR EXISTS (
        SELECT 1 FROM message_recipients AS sp
        WHERE sp.message_id = `+prefix+`message_id AND sp.kind IN ('to', 'cc')
          AND (instr(lower(sp.email_address), lower(?)) > 0
               OR instr(lower(sp.display_name), lower(?)) > 0)
    ))`)
		arguments = append(arguments, options.Participant, options.Participant, options.Participant, options.Participant)
	}
	if options.Subject != "" {
		clauses = append(clauses, `instr(lower(`+prefix+`subject), lower(?)) > 0`)
		arguments = append(arguments, options.Subject)
	}
	if options.RFCMessageID != "" {
		clauses = append(clauses, `lower(trim(trim(`+prefix+`rfc_message_id), '<>')) = lower(?)`)
		arguments = append(arguments, options.RFCMessageID)
	}
	if options.AttachmentFilename != "" {
		clauses = append(clauses, `EXISTS (
    SELECT 1 FROM attachments AS sa
    WHERE sa.message_id = `+prefix+`message_id
      AND instr(lower(sa.filename), lower(?)) > 0
)`)
		arguments = append(arguments, options.AttachmentFilename)
	}
	if options.AttachmentType != "" {
		clauses = append(clauses, `EXISTS (
    SELECT 1 FROM attachments AS st
    WHERE st.message_id = `+prefix+`message_id
      AND instr(lower(st.media_type), lower(?)) > 0
)`)
		arguments = append(arguments, options.AttachmentType)
	}
	if options.After != nil {
		clauses = append(clauses, `COALESCE(`+prefix+`sort_at, `+prefix+`created_at) >= ?`)
		arguments = append(arguments, options.After.Format(time.RFC3339Nano))
	}
	if options.Before != nil {
		clauses = append(clauses, `COALESCE(`+prefix+`sort_at, `+prefix+`created_at) < ?`)
		arguments = append(arguments, options.Before.Format(time.RFC3339Nano))
	}
	if len(clauses) == 0 {
		return "", arguments
	}
	return " AND " + strings.Join(clauses, " AND "), arguments
}

func searchCursorClause(cursor storedSearchCursor, rankExpression, sortExpression, idExpression string) (string, []any) {
	if cursor.ID == "" {
		return "", nil
	}
	return ` AND (
    ` + rankExpression + ` > ?
    OR (` + rankExpression + ` = ? AND ` + sortExpression + ` < ?)
    OR (` + rankExpression + ` = ? AND ` + sortExpression + ` = ? AND ` + idExpression + ` < ?)
)`, []any{cursor.Rank, cursor.Rank, cursor.SortAt, cursor.Rank, cursor.SortAt, cursor.ID}
}

func prefixedThreadColumns(alias string) string {
	prefix := strings.TrimSpace(alias) + "."
	columns := []string{
		"thread_id", "account_id", "root_rfc_message_id", "canonical_subject", "snippet",
		"participants_json", "folder_ids_json", "message_count", "unread_count",
		"attachment_count", "first_message_at", "last_message_at", "status",
		"reconstruction", "created_at", "updated_at",
	}
	for index := range columns {
		columns[index] = prefix + columns[index]
	}
	return strings.Join(columns, ", ")
}

func plainFTSQuery(value string) string {
	fields := strings.Fields(value)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ReplaceAll(field, `"`, `""`)
		terms = append(terms, `"`+field+`"`)
	}
	return strings.Join(terms, " AND ")
}

func firstStructuredMatchField(options maildomain.SearchOptions) string {
	switch {
	case options.FolderID != "":
		return "folder"
	case options.Unread != nil:
		return "unread"
	case options.Flagged != nil:
		return "flagged"
	case options.Sender != "":
		return "sender"
	case options.Participant != "":
		return "participant"
	case options.Subject != "":
		return "subject"
	case options.RFCMessageID != "":
		return "rfc_message_id"
	case options.AttachmentFilename != "":
		return "attachment_filename"
	case options.AttachmentType != "":
		return "attachment_type"
	case options.After != nil || options.Before != nil:
		return "date"
	default:
		return ""
	}
}

func (repository *SearchRepository) loadSearchMessageDetails(ctx context.Context, value *maildomain.Message) error {
	return (&MailRepository{db: repository.db}).loadMessageDetails(ctx, value)
}

func searchMessageSortAt(message maildomain.Message) time.Time {
	if message.SortAt != nil && !message.SortAt.IsZero() {
		return message.SortAt.UTC()
	}
	return message.CreatedAt.UTC()
}

func searchThreadSortAt(thread maildomain.Thread) time.Time {
	if !thread.LastMessageAt.IsZero() {
		return thread.LastMessageAt.UTC()
	}
	return thread.CreatedAt.UTC()
}

func boundedSearchExcerpt(value, fallback string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if value == "" {
		value = strings.TrimSpace(strings.ToValidUTF8(fallback, "�"))
	}
	if utf8.RuneCountInString(value) <= maxSearchExcerptRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxSearchExcerptRunes-1])) + "…"
}

func (repository *SearchRepository) queryMeta(ctx context.Context, accountID string, options maildomain.SearchOptions) (maildomain.SearchQueryMeta, error) {
	var pending, failed int
	if err := repository.db.QueryRowContext(ctx, `
SELECT
    COALESCE(sum(CASE WHEN body_state = 'pending' THEN 1 ELSE 0 END), 0),
    COALESCE(sum(CASE WHEN body_state = 'error' THEN 1 ELSE 0 END), 0)
FROM message_search_documents WHERE account_id = ?`, accountID).Scan(&pending, &failed); err != nil {
		return maildomain.SearchQueryMeta{}, fmt.Errorf("read search index state: %w", err)
	}
	state := "complete"
	if failed > 0 {
		state = "error"
	} else if pending > 0 {
		state = "pending"
	}
	sortMode := "newest"
	if options.Query != "" {
		sortMode = "relevance"
	}
	return maildomain.SearchQueryMeta{
		HasText: options.Query != "", FilterCount: searchFilterCount(options),
		ResultKind: options.ResultKind, Sort: sortMode, IndexState: state,
		PendingCount: pending, ErrorCount: failed,
	}, nil
}

func searchFilterCount(options maildomain.SearchOptions) int {
	count := 0
	if options.FolderID != "" {
		count++
	}
	if options.Unread != nil {
		count++
	}
	if options.Flagged != nil {
		count++
	}
	if options.Sender != "" {
		count++
	}
	if options.Participant != "" {
		count++
	}
	if options.Subject != "" {
		count++
	}
	if options.RFCMessageID != "" {
		count++
	}
	if options.AttachmentFilename != "" {
		count++
	}
	if options.AttachmentType != "" {
		count++
	}
	if options.After != nil {
		count++
	}
	if options.Before != nil {
		count++
	}
	return count
}

type searchSignatureInput struct {
	AccountID          string                      `json:"account"`
	Query              string                      `json:"query"`
	FolderID           string                      `json:"folder"`
	Unread             *bool                       `json:"unread,omitempty"`
	Flagged            *bool                       `json:"flagged,omitempty"`
	Sender             string                      `json:"sender"`
	Participant        string                      `json:"participant"`
	Subject            string                      `json:"subject"`
	RFCMessageID       string                      `json:"rfc_id"`
	AttachmentFilename string                      `json:"attachment_filename"`
	AttachmentType     string                      `json:"attachment_type"`
	After              string                      `json:"after"`
	Before             string                      `json:"before"`
	ResultKind         maildomain.SearchResultKind `json:"kind"`
}

func searchSignature(accountID string, options maildomain.SearchOptions) (string, error) {
	input := searchSignatureInput{
		AccountID: accountID, Query: options.Query, FolderID: options.FolderID,
		Unread: options.Unread, Flagged: options.Flagged, Sender: options.Sender,
		Participant: options.Participant, Subject: options.Subject,
		RFCMessageID: options.RFCMessageID, AttachmentFilename: options.AttachmentFilename,
		AttachmentType: options.AttachmentType, ResultKind: options.ResultKind,
	}
	if options.After != nil {
		input.After = options.After.Format(time.RFC3339Nano)
	}
	if options.Before != nil {
		input.Before = options.Before.Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode search signature: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

type storedSearchCursor struct {
	Version   int                         `json:"v"`
	AccountID string                      `json:"account"`
	Kind      maildomain.SearchResultKind `json:"kind"`
	Signature string                      `json:"signature"`
	Rank      float64                     `json:"rank"`
	SortAt    string                      `json:"sort_at"`
	ID        string                      `json:"id"`
}

func encodeSearchCursor(accountID string, kind maildomain.SearchResultKind, signature string, rank float64, sortAt time.Time, id string) (string, error) {
	if accountID == "" || kind == "" || signature == "" || sortAt.IsZero() || id == "" || math.IsNaN(rank) || math.IsInf(rank, 0) {
		return "", maildomain.ErrInvalidSearchCursor
	}
	payload, err := json.Marshal(storedSearchCursor{
		Version: 1, AccountID: accountID, Kind: kind, Signature: signature,
		Rank: rank, SortAt: sortAt.UTC().Format(time.RFC3339Nano), ID: id,
	})
	if err != nil {
		return "", fmt.Errorf("encode search cursor: %w", err)
	}
	return "cur_" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeSearchCursor(value, accountID string, kind maildomain.SearchResultKind, signature string) (storedSearchCursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return storedSearchCursor{}, nil
	}
	if !strings.HasPrefix(value, "cur_") {
		return storedSearchCursor{}, maildomain.ErrInvalidSearchCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "cur_"))
	if err != nil {
		return storedSearchCursor{}, maildomain.ErrInvalidSearchCursor
	}
	var cursor storedSearchCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return storedSearchCursor{}, maildomain.ErrInvalidSearchCursor
	}
	if cursor.Version != 1 || cursor.AccountID != accountID || cursor.Kind != kind ||
		cursor.Signature != signature || cursor.ID == "" || math.IsNaN(cursor.Rank) || math.IsInf(cursor.Rank, 0) {
		return storedSearchCursor{}, maildomain.ErrInvalidSearchCursor
	}
	parsed, err := time.Parse(time.RFC3339Nano, cursor.SortAt)
	if err != nil || parsed.IsZero() {
		return storedSearchCursor{}, maildomain.ErrInvalidSearchCursor
	}
	cursor.SortAt = parsed.UTC().Format(time.RFC3339Nano)
	return cursor, nil
}

func (repository *SearchRepository) Pending(ctx context.Context, accountID string, limit int) ([]maildomain.SearchRepairItem, error) {
	if limit < 1 {
		return nil, errors.New("list pending search documents: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT message_id, body_state, body_error_code
FROM message_search_documents
WHERE account_id = ? AND body_state <> 'complete'
ORDER BY CASE body_state WHEN 'pending' THEN 0 ELSE 1 END, message_id
LIMIT ?`, strings.TrimSpace(accountID), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending search documents: %w", err)
	}
	defer rows.Close()
	items := []maildomain.SearchRepairItem{}
	for rows.Next() {
		var item maildomain.SearchRepairItem
		if err := rows.Scan(&item.MessageID, &item.State, &item.ErrorCode); err != nil {
			return nil, fmt.Errorf("read pending search document: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending search documents: %w", err)
	}
	return items, nil
}

func (repository *SearchRepository) SaveBody(ctx context.Context, accountID, messageID, body string, truncated bool, now time.Time) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin search body save: %w", err)
	}
	defer tx.Rollback()
	var storedAccount string
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM messages WHERE message_id = ?`, strings.TrimSpace(messageID)).Scan(&storedAccount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ErrMessageNotFound
		}
		return fmt.Errorf("resolve search body message: %w", err)
	}
	if storedAccount != strings.TrimSpace(accountID) {
		return maildomain.ErrMessageNotFound
	}
	if err := refreshMessageSearchDocument(ctx, tx, messageID, &body, &truncated, "complete", "", now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit search body save: %w", err)
	}
	return nil
}

func (repository *SearchRepository) RecordBodyError(ctx context.Context, accountID, messageID, code string, now time.Time) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin search body error: %w", err)
	}
	defer tx.Rollback()
	var storedAccount string
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM messages WHERE message_id = ?`, strings.TrimSpace(messageID)).Scan(&storedAccount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ErrMessageNotFound
		}
		return fmt.Errorf("resolve search body error message: %w", err)
	}
	if storedAccount != strings.TrimSpace(accountID) {
		return maildomain.ErrMessageNotFound
	}
	if err := refreshMessageSearchDocument(ctx, tx, messageID, nil, nil, "error", strings.TrimSpace(code), now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit search body error: %w", err)
	}
	return nil
}

func (repository *SearchRepository) Counts(ctx context.Context, accountID string) (pending, failed int, err error) {
	err = repository.db.QueryRowContext(ctx, `
SELECT
    COALESCE(sum(CASE WHEN body_state = 'pending' THEN 1 ELSE 0 END), 0),
    COALESCE(sum(CASE WHEN body_state = 'error' THEN 1 ELSE 0 END), 0)
FROM message_search_documents WHERE account_id = ?`, strings.TrimSpace(accountID)).Scan(&pending, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("count incomplete search documents: %w", err)
	}
	return pending, failed, nil
}

func (repository *SearchRepository) SearchQueryPlan(ctx context.Context, accountID string, options maildomain.SearchOptions) ([]string, error) {
	options = normalizeSearchOptions(options)
	if options.Limit < 1 {
		options.Limit = 20
	}
	if options.ResultKind == "" {
		options.ResultKind = maildomain.SearchMessages
	}
	signature, err := searchSignature(accountID, options)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeSearchCursor(options.Cursor, accountID, options.ResultKind, signature)
	if err != nil {
		return nil, err
	}
	var query string
	var args []any
	if options.ResultKind == maildomain.SearchThreads {
		query, args = threadSearchSQL(accountID, options, cursor)
	} else {
		query, args = messageSearchSQL(accountID, options, cursor)
	}
	rows, err := repository.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return nil, fmt.Errorf("explain search query: %w", err)
	}
	defer rows.Close()
	plans := []string{}
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return nil, err
		}
		plans = append(plans, strconv.Itoa(id)+":"+detail)
	}
	return plans, rows.Err()
}
