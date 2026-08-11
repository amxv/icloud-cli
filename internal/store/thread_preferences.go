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

func (repository *ThreadRepository) PreferenceCounts(ctx context.Context, accountID string) (total, muted int, err error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, 0, maildomain.ErrThreadNotFound
	}
	if err := repository.db.QueryRowContext(ctx, `
SELECT count(*), COALESCE(sum(CASE WHEN muted = 1 THEN 1 ELSE 0 END), 0)
FROM thread_preferences
WHERE account_id = ?`, accountID).Scan(&total, &muted); err != nil {
		return 0, 0, fmt.Errorf("count thread preferences: %w", err)
	}
	return total, muted, nil
}

// SetMuted updates local thread presentation state at the same repository
// boundary that resolves canonical thread IDs. Preferences are anchored to
// stable logical messages rather than derived thread rows: current
// thread_messages membership therefore carries a mute through aliases/merges,
// while a later split retains it only in the component containing the anchor.
func (repository *ThreadRepository) SetMuted(ctx context.Context, accountID, selector string, muted bool, now time.Time) (maildomain.ThreadPreference, error) {
	accountID = strings.TrimSpace(accountID)
	selector = strings.TrimSpace(selector)
	if accountID == "" || selector == "" || now.IsZero() {
		return maildomain.ThreadPreference{}, maildomain.ErrThreadNotFound
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.ThreadPreference{}, fmt.Errorf("begin thread preference update: %w", err)
	}
	defer tx.Rollback()
	threadID, _, err := resolveThread(ctx, tx, accountID, selector)
	if err != nil {
		return maildomain.ThreadPreference{}, err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)

	preference, hasPreference, err := firstThreadPreferenceInThread(ctx, tx, accountID, threadID, muted)
	if err != nil {
		return maildomain.ThreadPreference{}, err
	}
	if !hasPreference {
		anchor, anchorErr := canonicalThreadPreferenceAnchor(ctx, tx, accountID, threadID)
		if anchorErr != nil {
			return maildomain.ThreadPreference{}, anchorErr
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_preferences (account_id, anchor_message_id, muted, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(account_id, anchor_message_id) DO UPDATE SET
    muted = excluded.muted,
    updated_at = excluded.updated_at`, accountID, anchor, boolInt(muted), timestamp, timestamp); err != nil {
			return maildomain.ThreadPreference{}, fmt.Errorf("persist thread preference: %w", err)
		}
		preference, err = scanThreadPreference(tx.QueryRowContext(ctx, `
SELECT tp.account_id, ?, tp.anchor_message_id, tp.muted, tp.created_at, tp.updated_at
FROM thread_preferences AS tp
WHERE tp.account_id = ? AND tp.anchor_message_id = ?`, threadID, accountID, anchor))
		if err != nil {
			return maildomain.ThreadPreference{}, err
		}
	} else if muted {
		// If the conversation already carries one or more historical anchors,
		// activate one deterministic anchor. An already-muted anchor sorts first.
		if _, err := tx.ExecContext(ctx, `
UPDATE thread_preferences
SET muted = 1, updated_at = ?
WHERE account_id = ? AND anchor_message_id = ?`, timestamp, accountID, preference.AnchorMessageID); err != nil {
			return maildomain.ThreadPreference{}, fmt.Errorf("mute thread preference: %w", err)
		}
		preference.Muted = true
		preference.UpdatedAt = now.UTC()
	} else {
		// Unmuting a currently merged conversation clears every preference
		// anchor that is presently a member. If those messages later split,
		// explicit unmute remains truthful on every former component.
		if _, err := tx.ExecContext(ctx, `
UPDATE thread_preferences
SET muted = 0, updated_at = ?
WHERE account_id = ?
  AND anchor_message_id IN (
      SELECT message_id FROM thread_messages WHERE thread_id = ?
  )`, timestamp, accountID, threadID); err != nil {
			return maildomain.ThreadPreference{}, fmt.Errorf("unmute thread preferences: %w", err)
		}
		preference.Muted = false
		preference.UpdatedAt = now.UTC()
	}
	if err := tx.Commit(); err != nil {
		return maildomain.ThreadPreference{}, fmt.Errorf("commit thread preference update: %w", err)
	}
	preference.ThreadID = threadID
	preference.LocalOnly = true
	return preference, nil
}

func firstThreadPreferenceInThread(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, accountID, threadID string, preferMuted bool) (maildomain.ThreadPreference, bool, error) {
	order := "tp.created_at, tp.anchor_message_id"
	if preferMuted {
		order = "tp.muted DESC, tp.created_at, tp.anchor_message_id"
	}
	value, err := scanThreadPreference(queryer.QueryRowContext(ctx, `
SELECT tp.account_id, ?, tp.anchor_message_id, tp.muted, tp.created_at, tp.updated_at
FROM thread_preferences AS tp
JOIN thread_messages AS tm ON tm.message_id = tp.anchor_message_id
WHERE tp.account_id = ? AND tm.thread_id = ?
ORDER BY `+order+`
LIMIT 1`, threadID, accountID, threadID))
	if errors.Is(err, sql.ErrNoRows) {
		return maildomain.ThreadPreference{}, false, nil
	}
	if err != nil {
		return maildomain.ThreadPreference{}, false, err
	}
	return value, true, nil
}

func canonicalThreadPreferenceAnchor(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, accountID, threadID string) (string, error) {
	var messageID string
	err := queryer.QueryRowContext(ctx, `
SELECT tm.message_id
FROM thread_messages AS tm
JOIN messages AS m ON m.message_id = tm.message_id
WHERE tm.thread_id = ? AND m.account_id = ?
ORDER BY CASE WHEN ? = 'thr_' || substr(tm.message_id, 5) THEN 0 ELSE 1 END,
         COALESCE(m.sort_at, m.created_at), tm.message_id
LIMIT 1`, threadID, accountID, threadID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", maildomain.ErrThreadNotFound
	}
	if err != nil {
		return "", fmt.Errorf("select thread preference anchor: %w", err)
	}
	return messageID, nil
}

func scanThreadPreference(scanner interface{ Scan(...any) error }) (maildomain.ThreadPreference, error) {
	var value maildomain.ThreadPreference
	var muted int
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.AccountID, &value.ThreadID, &value.AnchorMessageID, &muted, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ThreadPreference{}, sql.ErrNoRows
		}
		return maildomain.ThreadPreference{}, fmt.Errorf("read thread preference: %w", err)
	}
	value.Muted = muted == 1
	value.LocalOnly = true
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.ThreadPreference{}, fmt.Errorf("parse thread preference creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.ThreadPreference{}, fmt.Errorf("parse thread preference update time: %w", err)
	}
	return value, nil
}

func applyThreadMuteState(ctx context.Context, db *sql.DB, accountID string, threads []maildomain.Thread) error {
	if len(threads) == 0 {
		return nil
	}
	values := make([]string, 0, len(threads))
	args := make([]any, 0, len(threads)+1)
	indices := make(map[string]int, len(threads))
	for index := range threads {
		threads[index].Muted = false
		if threadID := strings.TrimSpace(threads[index].ID); threadID != "" {
			values = append(values, "(?)")
			args = append(args, threadID)
			indices[threadID] = index
		}
	}
	if len(values) == 0 {
		return nil
	}
	args = append(args, strings.TrimSpace(accountID))
	rows, err := db.QueryContext(ctx, `
WITH selected(thread_id) AS (VALUES `+strings.Join(values, ",")+`)
SELECT selected.thread_id,
       EXISTS (
           SELECT 1
           FROM thread_messages AS tm
           JOIN thread_preferences AS tp
             ON tp.anchor_message_id = tm.message_id
            AND tp.account_id = ?
            AND tp.muted = 1
           WHERE tm.thread_id = selected.thread_id
           LIMIT 1
       )
FROM selected`, args...)
	if err != nil {
		return fmt.Errorf("project thread mute state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var threadID string
		var muted int
		if err := rows.Scan(&threadID, &muted); err != nil {
			return fmt.Errorf("read thread mute state: %w", err)
		}
		if index, ok := indices[threadID]; ok {
			threads[index].Muted = muted == 1
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read thread mute state: %w", err)
	}
	return nil
}

func applyMessageThreadMuteState(ctx context.Context, db *sql.DB, accountID string, messages []*maildomain.Message) error {
	if len(messages) == 0 {
		return nil
	}
	values := make([]string, 0, len(messages))
	args := make([]any, 0, len(messages)+1)
	indices := make(map[string][]int, len(messages))
	for index, message := range messages {
		if message == nil {
			continue
		}
		message.ThreadMuted = false
		messageID := strings.TrimSpace(message.ID)
		if messageID == "" {
			continue
		}
		if _, exists := indices[messageID]; !exists {
			values = append(values, "(?)")
			args = append(args, messageID)
		}
		indices[messageID] = append(indices[messageID], index)
	}
	if len(values) == 0 {
		return nil
	}
	args = append(args, strings.TrimSpace(accountID))
	rows, err := db.QueryContext(ctx, `
WITH selected(message_id) AS (VALUES `+strings.Join(values, ",")+`)
SELECT selected.message_id,
       EXISTS (
           SELECT 1
           FROM thread_messages AS own
           JOIN thread_messages AS member ON member.thread_id = own.thread_id
           JOIN thread_preferences AS tp
             ON tp.anchor_message_id = member.message_id
            AND tp.account_id = ?
            AND tp.muted = 1
           WHERE own.message_id = selected.message_id
           LIMIT 1
       )
FROM selected`, args...)
	if err != nil {
		return fmt.Errorf("project message thread mute state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var messageID string
		var muted int
		if err := rows.Scan(&messageID, &muted); err != nil {
			return fmt.Errorf("read message thread mute state: %w", err)
		}
		for _, index := range indices[messageID] {
			messages[index].ThreadMuted = muted == 1
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read message thread mute state: %w", err)
	}
	return nil
}

// AllMessagesMuted is deliberately conservative for watch presentation. It
// returns confident=false unless every changed logical message still resolves
// to a current reconstructed thread. When confident is true, allMuted says
// whether every resolved conversation carries an active local mute anchor.
func (repository *ThreadRepository) AllMessagesMuted(ctx context.Context, accountID string, messageIDs []string) (allMuted, confident bool, err error) {
	unique := uniqueSortedStrings(messageIDs)
	if strings.TrimSpace(accountID) == "" || len(unique) == 0 {
		return false, false, nil
	}
	for _, messageID := range unique {
		var threadID string
		if err := repository.db.QueryRowContext(ctx, `
SELECT tm.thread_id
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.message_id = ?`, strings.TrimSpace(accountID), messageID).Scan(&threadID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, false, nil
			}
			return false, false, fmt.Errorf("resolve changed message thread mute state: %w", err)
		}
		var muted int
		if err := repository.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM thread_messages AS member
    JOIN thread_preferences AS tp
      ON tp.anchor_message_id = member.message_id
     AND tp.account_id = ?
     AND tp.muted = 1
    WHERE member.thread_id = ?
    LIMIT 1
)`, strings.TrimSpace(accountID), threadID).Scan(&muted); err != nil {
			return false, false, fmt.Errorf("read changed thread mute state: %w", err)
		}
		if muted != 1 {
			return false, true, nil
		}
	}
	return true, true, nil
}
