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

type SenderPreferenceRepository struct {
	db *sql.DB
}

type senderPreferenceCursor struct {
	Version   int    `json:"v"`
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
}

func (repository *SenderPreferenceRepository) Counts(ctx context.Context, accountID string) (total, vip, blocked int, err error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, 0, 0, maildomain.ErrInvalidSenderPreference
	}
	if err := repository.db.QueryRowContext(ctx, `
SELECT count(*),
       COALESCE(sum(CASE WHEN vip = 1 THEN 1 ELSE 0 END), 0),
       COALESCE(sum(CASE WHEN blocked = 1 THEN 1 ELSE 0 END), 0)
FROM sender_preferences
WHERE account_id = ?`, accountID).Scan(&total, &vip, &blocked); err != nil {
		return 0, 0, 0, fmt.Errorf("count sender preferences: %w", err)
	}
	return total, vip, blocked, nil
}

func (repository *SenderPreferenceRepository) Get(ctx context.Context, accountID, email string) (maildomain.SenderPreference, error) {
	accountID = strings.TrimSpace(accountID)
	normalized, err := maildomain.NormalizeSenderEmail(email)
	if err != nil || accountID == "" {
		return maildomain.SenderPreference{}, maildomain.ErrInvalidSenderPreference
	}
	return scanSenderPreference(repository.db.QueryRowContext(ctx, `
SELECT account_id, email_address, vip, blocked, created_at, updated_at
FROM sender_preferences
WHERE account_id = ? AND email_address = ?`, accountID, normalized))
}

func (repository *SenderPreferenceRepository) SetVIP(ctx context.Context, accountID, email string, value bool, now time.Time) (maildomain.SenderPreference, error) {
	return repository.setFlag(ctx, accountID, email, "vip", value, now)
}

func (repository *SenderPreferenceRepository) SetBlocked(ctx context.Context, accountID, email string, value bool, now time.Time) (maildomain.SenderPreference, error) {
	return repository.setFlag(ctx, accountID, email, "blocked", value, now)
}

func (repository *SenderPreferenceRepository) setFlag(ctx context.Context, accountID, email, column string, value bool, now time.Time) (maildomain.SenderPreference, error) {
	accountID = strings.TrimSpace(accountID)
	normalized, err := maildomain.NormalizeSenderEmail(email)
	if err != nil || accountID == "" || (column != "vip" && column != "blocked") {
		return maildomain.SenderPreference{}, maildomain.ErrInvalidSenderPreference
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	vip, blocked := 0, 0
	if column == "vip" {
		vip = boolInt(value)
	} else {
		blocked = boolInt(value)
	}
	statement := `
INSERT INTO sender_preferences (account_id, email_address, vip, blocked, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, email_address) DO UPDATE SET ` + column + ` = excluded.` + column + `, updated_at = excluded.updated_at`
	if _, err := repository.db.ExecContext(ctx, statement, accountID, normalized, vip, blocked, timestamp, timestamp); err != nil {
		return maildomain.SenderPreference{}, fmt.Errorf("update sender preference: %w", err)
	}
	return repository.Get(ctx, accountID, normalized)
}

func (repository *SenderPreferenceRepository) ListPage(ctx context.Context, accountID, cursor string, limit int) (maildomain.SenderPreferencePage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 || limit > 1000 {
		return maildomain.SenderPreferencePage{}, maildomain.ErrInvalidSenderPreference
	}
	decoded := senderPreferenceCursor{}
	if strings.TrimSpace(cursor) != "" {
		if err := decodeOpaqueCursor("spcur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || decoded.Email == "" {
			return maildomain.SenderPreferencePage{}, maildomain.ErrInvalidListCursor
		}
	}
	where := `WHERE account_id = ?`
	args := []any{accountID}
	if decoded.Email != "" {
		where += ` AND email_address > ?`
		args = append(args, decoded.Email)
	}
	args = append(args, limit+1)
	rows, err := repository.db.QueryContext(ctx, `
SELECT account_id, email_address, vip, blocked, created_at, updated_at
FROM sender_preferences
`+where+`
ORDER BY email_address ASC
LIMIT ?`, args...)
	if err != nil {
		return maildomain.SenderPreferencePage{}, fmt.Errorf("list sender preferences: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.SenderPreference, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanSenderPreference(rows)
		if scanErr != nil {
			return maildomain.SenderPreferencePage{}, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return maildomain.SenderPreferencePage{}, fmt.Errorf("list sender preferences: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	next := ""
	if more && len(values) > 0 {
		next, err = encodeOpaqueCursor("spcur_", senderPreferenceCursor{Version: 1, AccountID: accountID, Email: values[len(values)-1].Email})
		if err != nil {
			return maildomain.SenderPreferencePage{}, fmt.Errorf("encode sender preference cursor: %w", err)
		}
	}
	return maildomain.SenderPreferencePage{Preferences: values, More: more, NextCursor: next}, nil
}

func scanSenderPreference(scanner interface{ Scan(...any) error }) (maildomain.SenderPreference, error) {
	var value maildomain.SenderPreference
	var vip, blocked int
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.AccountID, &value.Email, &vip, &blocked, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.SenderPreference{}, maildomain.ErrSenderPreferenceNotFound
		}
		return maildomain.SenderPreference{}, fmt.Errorf("read sender preference: %w", err)
	}
	value.VIP = vip == 1
	value.Blocked = blocked == 1
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.SenderPreference{}, fmt.Errorf("parse sender preference creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.SenderPreference{}, fmt.Errorf("parse sender preference update time: %w", err)
	}
	return value, nil
}

func applyMessageSenderPreferenceState(ctx context.Context, db *sql.DB, accountID string, messages []*maildomain.Message) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || len(messages) == 0 {
		return nil
	}
	indexes := map[string][]int{}
	for index, message := range messages {
		if message == nil {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(message.From.Email))
		if email == "" {
			message.SenderPreference = nil
			continue
		}
		message.SenderPreference = &maildomain.SenderPreferenceState{Email: email}
		indexes[email] = append(indexes[email], index)
	}
	if len(indexes) == 0 {
		return nil
	}
	emails := make([]string, 0, len(indexes))
	placeholders := make([]string, 0, len(indexes))
	for email := range indexes {
		emails = append(emails, email)
		placeholders = append(placeholders, "?")
	}
	args := make([]any, 0, len(emails)+1)
	args = append(args, accountID)
	for _, email := range emails {
		args = append(args, email)
	}
	rows, err := db.QueryContext(ctx, `
SELECT email_address, vip, blocked
FROM sender_preferences
WHERE account_id = ? AND email_address IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return fmt.Errorf("project message sender preferences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		var vip, blocked int
		if err := rows.Scan(&email, &vip, &blocked); err != nil {
			return fmt.Errorf("read projected message sender preference: %w", err)
		}
		for _, index := range indexes[email] {
			messages[index].SenderPreference = &maildomain.SenderPreferenceState{Email: email, Explicit: true, VIP: vip == 1, Blocked: blocked == 1}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("project message sender preferences: %w", err)
	}
	return nil
}

func applyThreadSenderPreferenceState(ctx context.Context, db *sql.DB, accountID string, threads []maildomain.Thread) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || len(threads) == 0 {
		return nil
	}
	threadIndexes := map[string]int{}
	placeholders := make([]string, 0, len(threads))
	args := make([]any, 0, len(threads)+1)
	args = append(args, accountID)
	for index := range threads {
		threads[index].SenderPreferences = &maildomain.ThreadSenderPreferenceSummary{}
		threadIndexes[threads[index].ID] = index
		placeholders = append(placeholders, "?")
		args = append(args, threads[index].ID)
	}
	rows, err := db.QueryContext(ctx, `
WITH sender_states AS (
    SELECT tm.thread_id,
           lower(trim(m.from_email)) AS email_address,
           max(CASE WHEN sp.account_id IS NOT NULL THEN 1 ELSE 0 END) AS explicit,
           max(COALESCE(sp.vip, 0)) AS vip,
           max(COALESCE(sp.blocked, 0)) AS blocked
    FROM thread_messages AS tm
    JOIN messages AS m ON m.message_id = tm.message_id AND m.account_id = ?
    LEFT JOIN sender_preferences AS sp
      ON sp.account_id = m.account_id AND sp.email_address = lower(trim(m.from_email))
    WHERE tm.thread_id IN (`+strings.Join(placeholders, ",")+`)
      AND trim(m.from_email) <> ''
    GROUP BY tm.thread_id, lower(trim(m.from_email))
)
SELECT thread_id, count(*),
       sum(explicit), sum(vip), sum(blocked),
       sum(CASE WHEN explicit = 0 THEN 1 ELSE 0 END)
FROM sender_states
GROUP BY thread_id`, args...)
	if err != nil {
		return fmt.Errorf("project thread sender preferences: %w", err)
	}
	for rows.Next() {
		var threadID string
		var distinct, explicit, vip, blocked, unpreferred int
		if err := rows.Scan(&threadID, &distinct, &explicit, &vip, &blocked, &unpreferred); err != nil {
			rows.Close()
			return fmt.Errorf("read projected thread sender preferences: %w", err)
		}
		if index, ok := threadIndexes[threadID]; ok {
			threads[index].SenderPreferences = &maildomain.ThreadSenderPreferenceSummary{
				DistinctSenders: distinct, ExplicitSenders: explicit, VIPSenders: vip,
				BlockedSenders: blocked, UnpreferredSenders: unpreferred,
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("project thread sender preferences: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close thread sender preference projection: %w", err)
	}

	missingArgs := make([]any, 0, len(threads)+1)
	missingArgs = append(missingArgs, accountID)
	for index := range threads {
		missingArgs = append(missingArgs, threads[index].ID)
	}
	missingRows, err := db.QueryContext(ctx, `
SELECT tm.thread_id, count(*)
FROM thread_messages AS tm
JOIN messages AS m ON m.message_id = tm.message_id AND m.account_id = ?
WHERE tm.thread_id IN (`+strings.Join(placeholders, ",")+`) AND trim(m.from_email) = ''
GROUP BY tm.thread_id`, missingArgs...)
	if err != nil {
		return fmt.Errorf("project missing thread senders: %w", err)
	}
	defer missingRows.Close()
	for missingRows.Next() {
		var threadID string
		var count int
		if err := missingRows.Scan(&threadID, &count); err != nil {
			return fmt.Errorf("read missing thread senders: %w", err)
		}
		if index, ok := threadIndexes[threadID]; ok && threads[index].SenderPreferences != nil {
			threads[index].SenderPreferences.MissingSenderMessages = count
		}
	}
	if err := missingRows.Err(); err != nil {
		return fmt.Errorf("project missing thread senders: %w", err)
	}
	return nil
}
