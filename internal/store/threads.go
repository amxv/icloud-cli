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
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/amxv/icloud-cli/internal/id"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type ThreadRepository struct {
	db *sql.DB
}

type threadNode struct {
	ID               string
	AccountID        string
	RFCMessageID     string
	InReplyTo        string
	References       []string
	Subject          string
	Snippet          string
	FromEmail        string
	ReplyToEmail     string
	Recipients       []string
	FolderIDs        []string
	SortAt           time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Unread           bool
	AttachmentCount  int
	SuppressFallback bool
}

type graphEdge struct {
	ID              string
	ChildMessageID  string
	Relation        string
	Ordinal         int
	Token           string
	ParentMessageID string
	Status          string
}

type threadAggregate struct {
	ID               string
	RootRFCMessageID string
	Subject          string
	Snippet          string
	Participants     []string
	FolderIDs        []string
	MessageCount     int
	UnreadCount      int
	AttachmentCount  int
	FirstMessageAt   time.Time
	LastMessageAt    time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	MessageIDs       []string
}

const threadBackfillOpenBatchSize = 64

// ensureThreadGraphBackfill advances at most one persisted keyset page per
// database open. The migration can therefore be interrupted/restarted without
// rescanning an account or materializing a whole mailbox in one transaction.
func ensureThreadGraphBackfill(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin thread graph backfill: %w", err)
	}
	defer tx.Rollback()

	var accountID, cursor string
	err = tx.QueryRowContext(ctx, `
SELECT account_id, backfill_cursor
FROM thread_projection_state
WHERE state = 'pending' AND reason = 'schema_backfill'
ORDER BY account_id
LIMIT 1`).Scan(&accountID, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read thread graph backfill frontier: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT message_id
FROM messages
WHERE account_id = ? AND message_id > ?
ORDER BY message_id
LIMIT ?`, accountID, cursor, threadBackfillOpenBatchSize+1)
	if err != nil {
		return fmt.Errorf("read thread graph backfill page: %w", err)
	}
	messageIDs := make([]string, 0, threadBackfillOpenBatchSize+1)
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return fmt.Errorf("read thread graph backfill message: %w", err)
		}
		messageIDs = append(messageIDs, messageID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close thread graph backfill page: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read thread graph backfill page: %w", err)
	}

	now := time.Now().UTC()
	more := len(messageIDs) > threadBackfillOpenBatchSize
	if more {
		messageIDs = messageIDs[:threadBackfillOpenBatchSize]
	}
	if len(messageIDs) > 0 {
		if err := rebuildBackfillThreadProjection(ctx, tx, accountID, messageIDs, now); err != nil {
			return fmt.Errorf("advance thread graph backfill: %w", err)
		}
		cursor = messageIDs[len(messageIDs)-1]
	}

	state, reason := "pending", "schema_backfill"
	if !more {
		var pendingJobs int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM thread_projection_jobs WHERE account_id = ?`, accountID).Scan(&pendingJobs); err != nil {
			return fmt.Errorf("count deferred thread projections after backfill: %w", err)
		}
		if pendingJobs == 0 {
			state, reason = "ready", ""
		} else {
			state, reason = "degraded", "affected_scope_limit"
		}
	}
	timestamp := now.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_state
SET state = ?, reason = ?, backfill_cursor = ?,
    pending_since = CASE WHEN ? = 'ready' THEN NULL ELSE COALESCE(pending_since, ?) END,
    updated_at = ?
WHERE account_id = ?`, state, reason, cursor, state, timestamp, timestamp, accountID); err != nil {
		return fmt.Errorf("persist thread graph backfill frontier: %w", err)
	}

	if !more {
		var remaining int
		if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM thread_projection_state
WHERE state = 'pending' AND reason = 'schema_backfill'`).Scan(&remaining); err != nil {
			return fmt.Errorf("count remaining thread graph backfills: %w", err)
		}
		if remaining == 0 {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO app_metadata (key, value, updated_at)
VALUES ('thread_graph_backfill_v1', 'complete', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, timestamp); err != nil {
				return fmt.Errorf("persist thread graph backfill completion: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit thread graph backfill page: %w", err)
	}
	return nil
}

// rebuildBackfillThreadProjection applies the same exact bounded reconstructor
// used by foreground writes, but coalesces an oversized migration page to one
// durable seed per pre-existing thread. This prevents a 10k-message legacy
// thread from producing 10k redundant repair jobs as the keyset frontier walks
// through it on successive opens.
func rebuildBackfillThreadProjection(ctx context.Context, tx *sql.Tx, accountID string, seedMessageIDs []string, now time.Time) error {
	seeds := uniqueSortedStrings(seedMessageIDs)
	if len(seeds) == 0 {
		return nil
	}
	complete, err := attemptAffectedThreadGraph(ctx, tx, accountID, seeds, now)
	if err != nil {
		return err
	}
	if complete {
		return clearCompletedThreadProjectionSeeds(ctx, tx, accountID, seeds, now)
	}
	representatives, err := backfillDeferredRepresentatives(ctx, tx, accountID, seeds)
	if err != nil {
		return err
	}
	if len(representatives) == 0 {
		return nil
	}
	if err := preserveDeferredThreadProjectionSeeds(ctx, tx, accountID, representatives, now); err != nil {
		return err
	}
	return persistDeferredThreadProjection(ctx, tx, accountID, representatives, "affected_scope_limit", now)
}

func backfillDeferredRepresentatives(ctx context.Context, tx *sql.Tx, accountID string, seeds []string) ([]string, error) {
	byThread := map[string]string{}
	values := make([]string, 0, len(seeds))
	for _, seed := range uniqueSortedStrings(seeds) {
		var threadID string
		err := tx.QueryRowContext(ctx, `
SELECT tm.thread_id
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.message_id = ?`, accountID, seed).Scan(&threadID)
		if errors.Is(err, sql.ErrNoRows) {
			values = append(values, seed)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("resolve backfill thread representative: %w", err)
		}
		if _, exists := byThread[threadID]; exists {
			continue
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1
    FROM thread_projection_jobs AS j
    JOIN thread_messages AS jm ON jm.message_id = j.seed_message_id
    WHERE j.account_id = ? AND jm.thread_id = ?
)`, accountID, threadID).Scan(&pending); err != nil {
			return nil, fmt.Errorf("check existing backfill thread projection job: %w", err)
		}
		byThread[threadID] = seed
		if pending == 0 {
			values = append(values, seed)
		}
	}
	return uniqueSortedStrings(values), nil
}

func (store *Store) AdvanceThreadGraphBackfill(ctx context.Context, pages int) (int, bool, error) {
	if pages < 1 || pages > 1000 {
		return 0, false, errors.New("advance thread graph backfill: bounded page count is required")
	}
	advanced := 0
	for advanced < pages {
		var pending int
		if err := store.db.QueryRowContext(ctx, `
SELECT count(*) FROM thread_projection_state
WHERE state = 'pending' AND reason = 'schema_backfill'`).Scan(&pending); err != nil {
			return advanced, false, fmt.Errorf("count pending thread graph backfill: %w", err)
		}
		if pending == 0 {
			return advanced, false, nil
		}
		if err := ensureThreadGraphBackfill(ctx, store.db); err != nil {
			return advanced, true, err
		}
		advanced++
	}
	var pending int
	if err := store.db.QueryRowContext(ctx, `
SELECT count(*) FROM thread_projection_state
WHERE state = 'pending' AND reason = 'schema_backfill'`).Scan(&pending); err != nil {
		return advanced, false, fmt.Errorf("count remaining thread graph backfill: %w", err)
	}
	return advanced, pending > 0, nil
}

// rebuildAffectedThreadGraph reconstructs only the graph closure touched by a
// bounded sync page or one location mutation. The closure includes prior
// memberships and direct RFC/fallback neighbors, so late roots and bridges can
// merge or split threads without materializing every local message.
func rebuildAffectedThreadGraph(ctx context.Context, tx *sql.Tx, accountID string, seedMessageIDs []string, now time.Time) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("rebuild affected thread graph: account ID is required")
	}
	nodes, err := loadAffectedThreadNodes(ctx, tx, accountID, seedMessageIDs)
	if err != nil {
		return err
	}
	return persistReconstructedThreadGraph(ctx, tx, accountID, nodes, now)
}

func persistReconstructedThreadGraph(ctx context.Context, tx *sql.Tx, accountID string, nodes map[string]*threadNode, now time.Time) error {
	if len(nodes) == 0 {
		return nil
	}
	messageIDs := sortedNodeIDs(nodes)
	existingMembership, threadCreated, err := loadExistingThreadMembershipForMessages(ctx, tx, accountID, messageIDs)
	if err != nil {
		return err
	}
	components, edges := buildThreadComponents(nodes)
	aggregates, retired, err := aggregateThreadComponents(ctx, tx, accountID, nodes, components, existingMembership, threadCreated)
	if err != nil {
		return err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)

	if err := deleteThreadRowsForMessages(ctx, tx, accountID, "thread_edges", "child_message_id", messageIDs); err != nil {
		return fmt.Errorf("replace affected thread edges: %w", err)
	}
	if err := deleteThreadRowsForMessages(ctx, tx, "", "thread_messages", "message_id", messageIDs); err != nil {
		return fmt.Errorf("replace affected thread memberships: %w", err)
	}

	active := make(map[string]struct{}, len(aggregates))
	for _, aggregate := range aggregates {
		active[aggregate.ID] = struct{}{}
		participantsJSON, err := json.Marshal(aggregate.Participants)
		if err != nil {
			return fmt.Errorf("encode thread participants: %w", err)
		}
		foldersJSON, err := json.Marshal(aggregate.FolderIDs)
		if err != nil {
			return fmt.Errorf("encode thread folders: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_aliases WHERE alias_thread_id = ?`, aggregate.ID); err != nil {
			return fmt.Errorf("remove active thread alias: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO threads (
    thread_id, account_id, root_rfc_message_id, canonical_subject, snippet,
    participants_json, folder_ids_json, message_count, unread_count,
    attachment_count, first_message_at, last_message_at, status,
    reconstruction, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 'local_rfc_graph', ?, ?)
ON CONFLICT(thread_id) DO UPDATE SET
    root_rfc_message_id = excluded.root_rfc_message_id,
    canonical_subject = excluded.canonical_subject,
    snippet = excluded.snippet,
    participants_json = excluded.participants_json,
    folder_ids_json = excluded.folder_ids_json,
    message_count = excluded.message_count,
    unread_count = excluded.unread_count,
    attachment_count = excluded.attachment_count,
    first_message_at = excluded.first_message_at,
    last_message_at = excluded.last_message_at,
    updated_at = excluded.updated_at`,
			aggregate.ID, accountID, aggregate.RootRFCMessageID, aggregate.Subject, aggregate.Snippet,
			string(participantsJSON), string(foldersJSON), aggregate.MessageCount, aggregate.UnreadCount,
			aggregate.AttachmentCount, nullableThreadTime(aggregate.FirstMessageAt), nullableThreadTime(aggregate.LastMessageAt),
			aggregate.CreatedAt.UTC().Format(time.RFC3339Nano), aggregate.UpdatedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("persist reconstructed thread: %w", err)
		}
		for _, messageID := range aggregate.MessageIDs {
			joinedAt := nodes[messageID].CreatedAt
			if joinedAt.IsZero() {
				joinedAt = now
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_messages (thread_id, message_id, joined_at)
VALUES (?, ?, ?)`, aggregate.ID, messageID, joinedAt.UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("persist reconstructed thread membership: %w", err)
			}
		}
	}

	for aliasID, canonicalID := range retired {
		if aliasID == canonicalID {
			continue
		}
		if _, ok := active[aliasID]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE thread_aliases SET canonical_thread_id = ?
WHERE account_id = ? AND canonical_thread_id = ?`, canonicalID, accountID, aliasID); err != nil {
			return fmt.Errorf("flatten retired thread aliases: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE account_id = ? AND thread_id = ?`, accountID, aliasID); err != nil {
			return fmt.Errorf("retire merged thread: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_aliases (alias_thread_id, account_id, canonical_thread_id, reason, created_at)
VALUES (?, ?, ?, 'graph_merge', ?)
ON CONFLICT(alias_thread_id) DO UPDATE SET
    canonical_thread_id = excluded.canonical_thread_id,
    reason = excluded.reason`, aliasID, accountID, canonicalID, timestamp); err != nil {
			return fmt.Errorf("persist retired thread alias: %w", err)
		}
	}

	affectedThreadIDs := map[string]struct{}{}
	for _, threadID := range existingMembership {
		affectedThreadIDs[threadID] = struct{}{}
	}
	for threadID := range affectedThreadIDs {
		if _, ok := active[threadID]; ok {
			continue
		}
		if _, ok := retired[threadID]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE account_id = ? AND thread_id = ?`, accountID, threadID); err != nil {
			return fmt.Errorf("delete obsolete reconstructed thread: %w", err)
		}
	}

	for _, edge := range edges {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_edges (
    edge_id, account_id, child_message_id, relation, ordinal,
    token, parent_message_id, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			edge.ID, accountID, edge.ChildMessageID, edge.Relation, edge.Ordinal,
			edge.Token, nullableThreadString(edge.ParentMessageID), edge.Status, timestamp, timestamp,
		); err != nil {
			return fmt.Errorf("persist thread edge evidence: %w", err)
		}
	}
	return nil
}

const (
	threadScopeTable                 = "temp.thread_rebuild_scope"
	maxAffectedThreadMessages        = 10000
	threadReconstructionBatchSize    = 300
	threadProjectionRepairBatchSize  = 500
	maxThreadProjectionRepairTargets = 32
)

var errThreadProjectionScopeLimit = errors.New("affected thread reconstruction scope limit exceeded")

// rebuildCanonicalThreadProjection keeps derived thread work off the canonical
// ingestion frontier. An oversized but otherwise valid conversation may defer
// its thread projection, but it must never roll back durable message/location
// ingestion or a sync/backfill checkpoint. Other reconstruction failures still
// surface because they can indicate corruption rather than bounded-work pressure.
func rebuildCanonicalThreadProjection(ctx context.Context, tx *sql.Tx, accountID string, seedMessageIDs []string, now time.Time) error {
	seeds := uniqueSortedStrings(seedMessageIDs)
	if len(seeds) == 0 {
		return nil
	}
	complete, err := attemptAffectedThreadGraph(ctx, tx, accountID, seeds, now)
	if err != nil {
		return err
	}
	if !complete {
		if preserveErr := preserveDeferredThreadProjectionSeeds(ctx, tx, accountID, seeds, now); preserveErr != nil {
			return preserveErr
		}
		return persistDeferredThreadProjection(ctx, tx, accountID, seeds, "affected_scope_limit", now)
	}
	return clearCompletedThreadProjectionSeeds(ctx, tx, accountID, seeds, now)
}

// attemptAffectedThreadGraph is the single boundary around the exact bounded
// reconstructor. Exceeding its scope is a deferred-state result, never a write
// failure; real storage/corruption failures still propagate.
func attemptAffectedThreadGraph(ctx context.Context, tx *sql.Tx, accountID string, seeds []string, now time.Time) (bool, error) {
	err := rebuildAffectedThreadGraph(ctx, tx, accountID, seeds, now)
	if errors.Is(err, errThreadProjectionScopeLimit) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func persistDeferredThreadProjection(ctx context.Context, tx *sql.Tx, accountID string, seeds []string, reason string, now time.Time) error {
	timestamp := now.UTC().Format(time.RFC3339Nano)
	for _, seed := range uniqueSortedStrings(seeds) {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_projection_jobs (
    account_id, seed_message_id, reason, repair_cursor, attempts,
    last_error, created_at, updated_at
) VALUES (?, ?, ?, '', 0, '', ?, ?)
ON CONFLICT(account_id, seed_message_id) DO UPDATE SET
    reason = excluded.reason,
    repair_phase = 'discover',
    repair_cursor = '',
    target_threads_json = '[]',
    canonical_thread_id = '',
    aggregate_message_count = 0,
    aggregate_unread_count = 0,
    aggregate_attachment_count = 0,
    aggregate_first_message_at = '',
    aggregate_last_message_at = '',
    aggregate_latest_sort_at = '',
    aggregate_latest_message_id = '',
    aggregate_latest_snippet = '',
    aggregate_latest_subject = '',
    aggregate_latest_rfc_message_id = '',
    updated_at = excluded.updated_at`, accountID, seed, reason, timestamp, timestamp); err != nil {
			return fmt.Errorf("persist deferred thread projection job: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_projection_state (
    account_id, state, reason, backfill_cursor, pending_since, updated_at
) VALUES (?, 'degraded', ?, '', ?, ?)
ON CONFLICT(account_id) DO UPDATE SET
    state = CASE
        WHEN thread_projection_state.state = 'pending' AND thread_projection_state.reason = 'schema_backfill'
            THEN 'pending'
        ELSE 'degraded'
    END,
    reason = CASE
        WHEN thread_projection_state.state = 'pending' AND thread_projection_state.reason = 'schema_backfill'
            THEN thread_projection_state.reason
        ELSE excluded.reason
    END,
    pending_since = COALESCE(thread_projection_state.pending_since, excluded.pending_since),
    updated_at = excluded.updated_at`, accountID, reason, timestamp, timestamp); err != nil {
		return fmt.Errorf("mark thread projection degraded: %w", err)
	}
	return nil
}

func clearCompletedThreadProjectionSeeds(ctx context.Context, tx *sql.Tx, accountID string, seeds []string, now time.Time) error {
	for _, seed := range uniqueSortedStrings(seeds) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM thread_projection_jobs WHERE account_id = ? AND seed_message_id = ?`, accountID, seed); err != nil {
			return fmt.Errorf("clear completed thread projection job: %w", err)
		}
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM thread_projection_jobs WHERE account_id = ?`, accountID).Scan(&pending); err != nil {
		return fmt.Errorf("count pending thread projection jobs: %w", err)
	}
	if pending != 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_state
SET state = CASE WHEN reason = 'schema_backfill' THEN state ELSE 'ready' END,
    reason = CASE WHEN reason = 'schema_backfill' THEN reason ELSE '' END,
    pending_since = CASE WHEN reason = 'schema_backfill' THEN pending_since ELSE NULL END,
    updated_at = ?
WHERE account_id = ?`, now.UTC().Format(time.RFC3339Nano), accountID); err != nil {
		return fmt.Errorf("mark thread projection ready: %w", err)
	}
	return nil
}

// preserveDeferredThreadProjectionSeeds gives every newly committed seed a
// truthful local thread handle even when its full graph closure is too large to
// rebuild in the foreground. Existing memberships are left untouched and are
// explicitly marked degraded by persistDeferredThreadProjection.
func preserveDeferredThreadProjectionSeeds(ctx context.Context, tx *sql.Tx, accountID string, seeds []string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS thread_rebuild_scope (
    message_id TEXT PRIMARY KEY
) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("create deferred thread scope: %w", err)
	}
	for _, seed := range uniqueSortedStrings(seeds) {
		var membership string
		err := tx.QueryRowContext(ctx, `SELECT thread_id FROM thread_messages WHERE message_id = ?`, seed).Scan(&membership)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read deferred seed thread membership: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+threadScopeTable); err != nil {
			return fmt.Errorf("clear deferred singleton scope: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO `+threadScopeTable+` (message_id)
SELECT message_id FROM messages WHERE account_id = ? AND message_id = ?`, accountID, seed); err != nil {
			return fmt.Errorf("seed deferred singleton projection: %w", err)
		}
		nodes, err := loadThreadNodesFromScope(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if err := persistReconstructedThreadGraph(ctx, tx, accountID, nodes, now); err != nil {
			return fmt.Errorf("persist deferred singleton projection: %w", err)
		}
	}
	return nil
}

type ThreadProjectionRepairResult struct {
	Attempted int
	Advanced  int
	Completed int
	Pending   int
	More      bool
}

// RepairThreadProjection resumes a bounded number of durable projection jobs.
// Normal-size jobs retry the exact graph reconstruction. Oversized jobs advance
// a persisted membership frontier in fixed pages and finalize through indexed
// SQLite aggregation rather than loading the entire conversation into a Go map.
func (store *Store) RepairThreadProjection(ctx context.Context, accountID string, limit int, now time.Time) (ThreadProjectionRepairResult, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 || limit > 1000 {
		return ThreadProjectionRepairResult{}, errors.New("repair thread projection: account and bounded limit are required")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT seed_message_id
FROM thread_projection_jobs
WHERE account_id = ?
ORDER BY updated_at, seed_message_id
LIMIT ?`, accountID, limit+1)
	if err != nil {
		return ThreadProjectionRepairResult{}, fmt.Errorf("list deferred thread projection jobs: %w", err)
	}
	seeds := make([]string, 0, limit+1)
	for rows.Next() {
		var seed string
		if err := rows.Scan(&seed); err != nil {
			rows.Close()
			return ThreadProjectionRepairResult{}, fmt.Errorf("read deferred thread projection job: %w", err)
		}
		seeds = append(seeds, seed)
	}
	if err := rows.Close(); err != nil {
		return ThreadProjectionRepairResult{}, fmt.Errorf("close deferred thread projection jobs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return ThreadProjectionRepairResult{}, fmt.Errorf("list deferred thread projection jobs: %w", err)
	}
	result := ThreadProjectionRepairResult{More: len(seeds) > limit}
	if result.More {
		seeds = seeds[:limit]
	}
	for _, seed := range seeds {
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return result, fmt.Errorf("begin deferred thread projection repair: %w", err)
		}
		result.Attempted++
		completed, advanced, repairErr := repairThreadProjectionSeed(ctx, tx, accountID, seed, now)
		if repairErr != nil {
			_ = tx.Rollback()
			return result, repairErr
		}
		if err := tx.Commit(); err != nil {
			return result, fmt.Errorf("commit deferred thread projection repair: %w", err)
		}
		if completed {
			result.Completed++
		}
		if advanced {
			result.Advanced++
		}
	}
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM thread_projection_jobs WHERE account_id = ?`, accountID).Scan(&result.Pending); err != nil {
		return result, fmt.Errorf("count deferred thread projection jobs after repair: %w", err)
	}
	result.More = result.More || result.Pending > 0
	return result, nil
}

type deferredProjectionJob struct {
	Phase              string
	Cursor             string
	TargetsJSON        string
	CanonicalThreadID  string
	MessageCount       int
	UnreadCount        int
	AttachmentCount    int
	FirstMessageAt     string
	LastMessageAt      string
	LatestSortAt       string
	LatestMessageID    string
	LatestSnippet      string
	LatestSubject      string
	LatestRFCMessageID string
}

func repairThreadProjectionSeed(ctx context.Context, tx *sql.Tx, accountID, seed string, now time.Time) (bool, bool, error) {
	job, err := loadDeferredProjectionJob(ctx, tx, accountID, seed)
	if err != nil {
		return false, false, err
	}
	switch job.Phase {
	case "discover":
		complete, err := attemptAffectedThreadGraph(ctx, tx, accountID, []string{seed}, now)
		if err != nil {
			return false, false, err
		}
		if complete {
			if err := clearCompletedThreadProjectionSeeds(ctx, tx, accountID, []string{seed}, now); err != nil {
				return false, false, err
			}
			return true, true, nil
		}
		targets, err := deferredProjectionTargetThreads(ctx, tx, accountID, seed)
		if err != nil {
			return false, false, err
		}
		if len(targets) == 0 {
			if err := clearCompletedThreadProjectionSeeds(ctx, tx, accountID, []string{seed}, now); err != nil {
				return false, false, err
			}
			return true, true, nil
		}
		if len(targets) > maxThreadProjectionRepairTargets {
			return false, false, fmt.Errorf("repair deferred thread projection: target fanout exceeds bounded maximum %d", maxThreadProjectionRepairTargets)
		}
		canonicalID, err := selectDeferredCanonicalThread(ctx, tx, accountID, targets)
		if err != nil {
			return false, false, err
		}
		if err := initializeDeferredProjectionScan(ctx, tx, accountID, seed, targets, canonicalID, now); err != nil {
			return false, false, err
		}
		return false, true, nil

	case "scan":
		targets, err := decodeDeferredProjectionTargets(job.TargetsJSON)
		if err != nil {
			return false, false, err
		}
		page, more, err := deferredProjectionMembershipPage(ctx, tx, accountID, targets, job.Cursor, threadProjectionRepairBatchSize)
		if err != nil {
			return false, false, err
		}
		if len(page) > 0 {
			if err := accumulateDeferredProjectionPage(ctx, tx, accountID, seed, page, job, now); err != nil {
				return false, false, err
			}
			if err := moveDeferredProjectionMembershipPage(ctx, tx, job.CanonicalThreadID, page); err != nil {
				return false, false, err
			}
		}
		timestamp := now.UTC().Format(time.RFC3339Nano)
		if more {
			cursor := page[len(page)-1]
			if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_jobs
SET repair_cursor = ?, attempts = attempts + 1, last_error = '', updated_at = ?
WHERE account_id = ? AND seed_message_id = ?`, cursor, timestamp, accountID, seed); err != nil {
				return false, false, fmt.Errorf("advance deferred thread scan frontier: %w", err)
			}
			return false, true, nil
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_jobs
SET repair_phase = 'edges', repair_cursor = '', attempts = attempts + 1,
    last_error = '', updated_at = ?
WHERE account_id = ? AND seed_message_id = ?`, timestamp, accountID, seed); err != nil {
			return false, false, fmt.Errorf("advance deferred thread repair to edge phase: %w", err)
		}
		return false, true, nil

	case "edges":
		page, more, err := repairDeferredProjectionEdgePage(ctx, tx, accountID, seed, job.CanonicalThreadID, job.Cursor, threadProjectionRepairBatchSize, now)
		if err != nil {
			return false, false, err
		}
		timestamp := now.UTC().Format(time.RFC3339Nano)
		if more {
			cursor := page[len(page)-1]
			if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_jobs
SET repair_cursor = ?, attempts = attempts + 1, last_error = '', updated_at = ?
WHERE account_id = ? AND seed_message_id = ?`, cursor, timestamp, accountID, seed); err != nil {
				return false, false, fmt.Errorf("advance deferred thread edge frontier: %w", err)
			}
			return false, true, nil
		}
		if err := finalizeDeferredProjectionJob(ctx, tx, accountID, seed, job, now); err != nil {
			return false, false, err
		}
		if err := clearCompletedThreadProjectionSeeds(ctx, tx, accountID, []string{seed}, now); err != nil {
			return false, false, err
		}
		return true, true, nil
	default:
		return false, false, fmt.Errorf("repair deferred thread projection: invalid persisted phase %q", job.Phase)
	}
}

func loadDeferredProjectionJob(ctx context.Context, tx *sql.Tx, accountID, seed string) (deferredProjectionJob, error) {
	var job deferredProjectionJob
	if err := tx.QueryRowContext(ctx, `
SELECT repair_phase, repair_cursor, target_threads_json, canonical_thread_id,
       aggregate_message_count, aggregate_unread_count, aggregate_attachment_count,
       aggregate_first_message_at, aggregate_last_message_at,
       aggregate_latest_sort_at, aggregate_latest_message_id,
       aggregate_latest_snippet, aggregate_latest_subject, aggregate_latest_rfc_message_id
FROM thread_projection_jobs
WHERE account_id = ? AND seed_message_id = ?`, accountID, seed).Scan(
		&job.Phase, &job.Cursor, &job.TargetsJSON, &job.CanonicalThreadID,
		&job.MessageCount, &job.UnreadCount, &job.AttachmentCount,
		&job.FirstMessageAt, &job.LastMessageAt, &job.LatestSortAt, &job.LatestMessageID,
		&job.LatestSnippet, &job.LatestSubject, &job.LatestRFCMessageID,
	); err != nil {
		return deferredProjectionJob{}, fmt.Errorf("read deferred thread repair frontier: %w", err)
	}
	return job, nil
}

func selectDeferredCanonicalThread(ctx context.Context, tx *sql.Tx, accountID string, threadIDs []string) (string, error) {
	type candidate struct {
		id       string
		created  time.Time
		messages int
	}
	values := make([]candidate, 0, len(threadIDs))
	for _, threadID := range uniqueSortedStrings(threadIDs) {
		var createdAt string
		var messageCount int
		if err := tx.QueryRowContext(ctx, `
SELECT created_at, message_count FROM threads WHERE account_id = ? AND thread_id = ?`, accountID, threadID).Scan(&createdAt, &messageCount); err != nil {
			return "", fmt.Errorf("read deferred target thread: %w", err)
		}
		parsed, _ := time.Parse(time.RFC3339Nano, createdAt)
		values = append(values, candidate{id: threadID, created: parsed, messages: messageCount})
	}
	if len(values) == 0 {
		return "", errors.New("select deferred canonical thread: no targets")
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].created.Equal(values[j].created) {
			if values[i].messages != values[j].messages {
				return values[i].messages > values[j].messages
			}
			return values[i].id < values[j].id
		}
		if values[i].created.IsZero() {
			return false
		}
		if values[j].created.IsZero() {
			return true
		}
		return values[i].created.Before(values[j].created)
	})
	return values[0].id, nil
}

func initializeDeferredProjectionScan(ctx context.Context, tx *sql.Tx, accountID, seed string, targets []string, canonicalID string, now time.Time) error {
	encoded, err := json.Marshal(uniqueSortedStrings(targets))
	if err != nil {
		return fmt.Errorf("encode deferred thread targets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM thread_projection_repair_participants WHERE account_id = ? AND seed_message_id = ?`, accountID, seed); err != nil {
		return fmt.Errorf("reset deferred thread participant accumulator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM thread_projection_repair_folders WHERE account_id = ? AND seed_message_id = ?`, accountID, seed); err != nil {
		return fmt.Errorf("reset deferred thread folder accumulator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_jobs
SET repair_phase = 'scan', repair_cursor = '', target_threads_json = ?, canonical_thread_id = ?,
    attempts = attempts + 1, last_error = '',
    aggregate_message_count = 0, aggregate_unread_count = 0, aggregate_attachment_count = 0,
    aggregate_first_message_at = '', aggregate_last_message_at = '',
    aggregate_latest_sort_at = '', aggregate_latest_message_id = '',
    aggregate_latest_snippet = '', aggregate_latest_subject = '', aggregate_latest_rfc_message_id = '',
    updated_at = ?
WHERE account_id = ? AND seed_message_id = ?`, string(encoded), canonicalID, now.UTC().Format(time.RFC3339Nano), accountID, seed); err != nil {
		return fmt.Errorf("initialize deferred thread scan: %w", err)
	}
	return nil
}

func decodeDeferredProjectionTargets(encoded string) ([]string, error) {
	var targets []string
	if err := json.Unmarshal([]byte(encoded), &targets); err != nil {
		return nil, fmt.Errorf("decode deferred thread targets: %w", err)
	}
	targets = uniqueSortedStrings(targets)
	if len(targets) == 0 || len(targets) > maxThreadProjectionRepairTargets {
		return nil, fmt.Errorf("decode deferred thread targets: persisted target count %d is invalid", len(targets))
	}
	return targets, nil
}

func accumulateDeferredProjectionPage(ctx context.Context, tx *sql.Tx, accountID, seed string, messageIDs []string, job deferredProjectionJob, now time.Time) error {
	if len(messageIDs) == 0 || len(messageIDs) > threadProjectionRepairBatchSize {
		return fmt.Errorf("accumulate deferred thread page: invalid page size %d", len(messageIDs))
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS thread_rebuild_scope (
    message_id TEXT PRIMARY KEY
) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("create deferred aggregate scope: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+threadScopeTable); err != nil {
		return fmt.Errorf("clear deferred aggregate scope: %w", err)
	}
	for _, messageID := range messageIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+threadScopeTable+` (message_id) VALUES (?)`, messageID); err != nil {
			return fmt.Errorf("seed deferred aggregate scope: %w", err)
		}
	}
	nodes, err := loadThreadNodesFromScope(ctx, tx, accountID)
	if err != nil {
		return err
	}
	if len(nodes) != len(messageIDs) {
		return fmt.Errorf("accumulate deferred thread page: loaded %d of %d messages", len(nodes), len(messageIDs))
	}
	pageCount, pageUnread, pageAttachments := 0, 0, 0
	firstAt, lastAt := parseRepairTime(job.FirstMessageAt), parseRepairTime(job.LastMessageAt)
	latestAt := parseRepairTime(job.LatestSortAt)
	latestID, latestSnippet, latestSubject, latestRFC := job.LatestMessageID, job.LatestSnippet, job.LatestSubject, job.LatestRFCMessageID
	participants := map[string]struct{}{}
	folders := map[string]struct{}{}
	for _, node := range nodes {
		pageCount++
		if node.Unread {
			pageUnread++
		}
		pageAttachments += node.AttachmentCount
		if firstAt.IsZero() || node.SortAt.Before(firstAt) {
			firstAt = node.SortAt
		}
		if lastAt.IsZero() || node.SortAt.After(lastAt) {
			lastAt = node.SortAt
		}
		if latestAt.IsZero() || node.SortAt.After(latestAt) || (node.SortAt.Equal(latestAt) && node.ID > latestID) {
			latestAt, latestID = node.SortAt, node.ID
			latestSnippet, latestSubject, latestRFC = node.Snippet, node.Subject, node.RFCMessageID
		}
		for _, value := range append([]string{node.FromEmail, node.ReplyToEmail}, node.Recipients...) {
			if value = canonicalEmail(value); value != "" {
				participants[value] = struct{}{}
			}
		}
		for _, folderID := range node.FolderIDs {
			if folderID = strings.TrimSpace(folderID); folderID != "" {
				folders[folderID] = struct{}{}
			}
		}
	}
	for value := range participants {
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO thread_projection_repair_participants (account_id, seed_message_id, value)
VALUES (?, ?, ?)`, accountID, seed, value); err != nil {
			return fmt.Errorf("accumulate deferred thread participant: %w", err)
		}
	}
	for folderID := range folders {
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO thread_projection_repair_folders (account_id, seed_message_id, folder_id)
VALUES (?, ?, ?)`, accountID, seed, folderID); err != nil {
			return fmt.Errorf("accumulate deferred thread folder: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE thread_projection_jobs
SET aggregate_message_count = aggregate_message_count + ?,
    aggregate_unread_count = aggregate_unread_count + ?,
    aggregate_attachment_count = aggregate_attachment_count + ?,
    aggregate_first_message_at = ?, aggregate_last_message_at = ?,
    aggregate_latest_sort_at = ?, aggregate_latest_message_id = ?,
    aggregate_latest_snippet = ?, aggregate_latest_subject = ?, aggregate_latest_rfc_message_id = ?,
    updated_at = ?
WHERE account_id = ? AND seed_message_id = ?`,
		pageCount, pageUnread, pageAttachments,
		formatRepairTime(firstAt), formatRepairTime(lastAt), formatRepairTime(latestAt), latestID,
		latestSnippet, latestSubject, latestRFC, now.UTC().Format(time.RFC3339Nano), accountID, seed,
	); err != nil {
		return fmt.Errorf("persist deferred thread aggregate page: %w", err)
	}
	return nil
}

func parseRepairTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return parsed
}

func formatRepairTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func moveDeferredProjectionMembershipPage(ctx context.Context, tx *sql.Tx, canonicalThreadID string, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(messageIDs)), ",")
	args := make([]any, 0, len(messageIDs)+1)
	args = append(args, strings.TrimSpace(canonicalThreadID))
	for _, messageID := range messageIDs {
		args = append(args, messageID)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE thread_messages
SET thread_id = ?
WHERE message_id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("move deferred thread membership page: %w", err)
	}
	return nil
}

type deferredProjectionEdge struct {
	ID             string
	ChildMessageID string
	Relation       string
	Token          string
}

func repairDeferredProjectionEdgePage(
	ctx context.Context,
	tx *sql.Tx,
	accountID, seed, canonicalThreadID, cursor string,
	limit int,
	now time.Time,
) ([]string, bool, error) {
	if limit < 1 || limit > threadProjectionRepairBatchSize {
		return nil, false, fmt.Errorf("repair deferred thread edges: invalid limit %d", limit)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS thread_rebuild_scope (
    message_id TEXT PRIMARY KEY
) WITHOUT ROWID`); err != nil {
		return nil, false, fmt.Errorf("create deferred edge scope: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+threadScopeTable); err != nil {
		return nil, false, fmt.Errorf("clear deferred edge scope: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+threadScopeTable+` (message_id) VALUES (?)`, seed); err != nil {
		return nil, false, fmt.Errorf("seed deferred edge scope: %w", err)
	}
	nodes, err := loadThreadNodesFromScope(ctx, tx, accountID)
	if err != nil {
		return nil, false, err
	}
	seedNode := nodes[seed]
	if seedNode == nil {
		return nil, false, fmt.Errorf("repair deferred thread edges: seed %s no longer exists", seed)
	}
	rfcTokens, relationTokens, fallbackTokens := threadNeighborTokens(nodes)
	allTokens := uniqueSortedStrings(append(append(append([]string{}, rfcTokens...), relationTokens...), fallbackTokens...))

	where := `e.account_id = ? AND e.edge_id > ?
AND EXISTS (
    SELECT 1 FROM thread_messages AS tm
    WHERE tm.message_id = e.child_message_id AND tm.thread_id = ?
)
AND e.child_message_id = ?`
	args := []any{accountID, strings.TrimSpace(cursor), canonicalThreadID, seed}
	if len(allTokens) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(allTokens)), ",")
		where = `e.account_id = ? AND e.edge_id > ?
AND EXISTS (
    SELECT 1 FROM thread_messages AS tm
    WHERE tm.message_id = e.child_message_id AND tm.thread_id = ?
)
AND (e.child_message_id = ? OR e.token IN (` + placeholders + `))`
		for _, token := range allTokens {
			args = append(args, token)
		}
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `
SELECT e.edge_id, e.child_message_id, e.relation, e.token
FROM thread_edges AS e
WHERE `+where+`
ORDER BY e.edge_id
LIMIT ?`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("page deferred thread edges: %w", err)
	}
	edges := make([]deferredProjectionEdge, 0, limit+1)
	for rows.Next() {
		var edge deferredProjectionEdge
		if err := rows.Scan(&edge.ID, &edge.ChildMessageID, &edge.Relation, &edge.Token); err != nil {
			rows.Close()
			return nil, false, fmt.Errorf("read deferred thread edge: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close deferred thread edge page: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("page deferred thread edges: %w", err)
	}
	more := len(edges) > limit
	if more {
		edges = edges[:limit]
	}
	seedRFC, _ := normalizeThreadToken(seedNode.RFCMessageID)
	fallbackSet := make(map[string]struct{}, len(fallbackTokens))
	for _, token := range fallbackTokens {
		fallbackSet[token] = struct{}{}
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	processed := make([]string, 0, len(edges))
	for _, edge := range edges {
		processed = append(processed, edge.ID)
		if edge.Relation == "fallback" {
			status := "isolated"
			if _, shared := fallbackSet[edge.Token]; shared {
				var peer int
				if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1
    FROM thread_edges AS e
    JOIN thread_messages AS tm ON tm.message_id = e.child_message_id
    WHERE tm.thread_id = ? AND e.relation = 'fallback' AND e.token = ?
      AND e.child_message_id <> ?
    LIMIT 1
)`, canonicalThreadID, edge.Token, edge.ChildMessageID).Scan(&peer); err != nil {
					return nil, false, fmt.Errorf("check deferred fallback peer: %w", err)
				}
				if peer == 1 {
					status = "grouped"
				}
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE thread_edges SET parent_message_id = NULL, status = ?, updated_at = ?
WHERE edge_id = ?`, status, timestamp, edge.ID); err != nil {
				return nil, false, fmt.Errorf("refresh deferred fallback edge: %w", err)
			}
			continue
		}
		token, valid := normalizeThreadToken(edge.Token)
		if !valid {
			if _, err := tx.ExecContext(ctx, `
UPDATE thread_edges SET parent_message_id = NULL, status = 'malformed', updated_at = ?
WHERE edge_id = ?`, timestamp, edge.ID); err != nil {
				return nil, false, fmt.Errorf("refresh malformed deferred edge: %w", err)
			}
			continue
		}
		parent := ""
		if seedRFC != "" && token == seedRFC && edge.ChildMessageID != seed {
			parent = seed
		} else {
			err := tx.QueryRowContext(ctx, `
SELECT m.message_id
FROM messages AS m
JOIN thread_messages AS tm ON tm.message_id = m.message_id
WHERE m.account_id = ? AND tm.thread_id = ? AND m.message_id <> ?
  AND lower(trim(trim(m.rfc_message_id), '<>')) = ?
ORDER BY m.message_id
LIMIT 1`, accountID, canonicalThreadID, edge.ChildMessageID, token).Scan(&parent)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, false, fmt.Errorf("resolve deferred edge parent: %w", err)
			}
		}
		status := "resolved"
		if parent == "" {
			status = "unresolved"
		} else if parent == edge.ChildMessageID {
			parent = ""
			status = "self"
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE thread_edges SET parent_message_id = ?, status = ?, updated_at = ?
WHERE edge_id = ?`, nullableThreadString(parent), status, timestamp, edge.ID); err != nil {
			return nil, false, fmt.Errorf("refresh deferred thread edge: %w", err)
		}
	}
	return processed, more, nil
}

func finalizeDeferredProjectionJob(ctx context.Context, tx *sql.Tx, accountID, seed string, initial deferredProjectionJob, now time.Time) error {
	job, err := loadDeferredProjectionJob(ctx, tx, accountID, seed)
	if err != nil {
		return err
	}
	if job.CanonicalThreadID == "" {
		job.CanonicalThreadID = initial.CanonicalThreadID
	}
	targets, err := decodeDeferredProjectionTargets(job.TargetsJSON)
	if err != nil {
		return err
	}
	var participantsJSON, foldersJSON string
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(json_group_array(value), '[]')
FROM (
    SELECT value
    FROM thread_projection_repair_participants
    WHERE account_id = ? AND seed_message_id = ?
    ORDER BY value
)`, accountID, seed).Scan(&participantsJSON); err != nil {
		return fmt.Errorf("finalize deferred thread participants: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(json_group_array(folder_id), '[]')
FROM (
    SELECT folder_id
    FROM thread_projection_repair_folders
    WHERE account_id = ? AND seed_message_id = ?
    ORDER BY folder_id
)`, accountID, seed).Scan(&foldersJSON); err != nil {
		return fmt.Errorf("finalize deferred thread folders: %w", err)
	}
	var rootRFC, subject string
	if err := tx.QueryRowContext(ctx, `
SELECT root_rfc_message_id, canonical_subject
FROM threads
WHERE account_id = ? AND thread_id = ?`, accountID, job.CanonicalThreadID).Scan(&rootRFC, &subject); err != nil {
		return fmt.Errorf("read deferred canonical thread metadata: %w", err)
	}
	if strings.TrimSpace(rootRFC) == "" {
		rootRFC, _ = normalizeThreadToken(job.LatestRFCMessageID)
	}
	if strings.TrimSpace(subject) == "" {
		subject = strings.TrimSpace(job.LatestSubject)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
UPDATE threads
SET root_rfc_message_id = ?, canonical_subject = ?, snippet = ?,
    participants_json = ?, folder_ids_json = ?, message_count = ?, unread_count = ?,
    attachment_count = ?, first_message_at = ?, last_message_at = ?, updated_at = ?
WHERE account_id = ? AND thread_id = ?`,
		rootRFC, subject, job.LatestSnippet, participantsJSON, foldersJSON,
		job.MessageCount, job.UnreadCount, job.AttachmentCount,
		nullableRepairText(job.FirstMessageAt), nullableRepairText(job.LastMessageAt), timestamp,
		accountID, job.CanonicalThreadID,
	); err != nil {
		return fmt.Errorf("persist deferred thread aggregate: %w", err)
	}
	for _, threadID := range targets {
		if threadID == job.CanonicalThreadID {
			continue
		}
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM thread_messages WHERE thread_id = ?`, threadID).Scan(&remaining); err != nil {
			return fmt.Errorf("verify deferred source thread drained: %w", err)
		}
		if remaining != 0 {
			return fmt.Errorf("finalize deferred thread projection: source thread %s still has %d members", threadID, remaining)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE thread_aliases SET canonical_thread_id = ?
WHERE account_id = ? AND canonical_thread_id = ?`, job.CanonicalThreadID, accountID, threadID); err != nil {
			return fmt.Errorf("flatten deferred thread aliases: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE account_id = ? AND thread_id = ?`, accountID, threadID); err != nil {
			return fmt.Errorf("retire deferred merged thread: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO thread_aliases (alias_thread_id, account_id, canonical_thread_id, reason, created_at)
VALUES (?, ?, ?, 'graph_merge', ?)
ON CONFLICT(alias_thread_id) DO UPDATE SET
    canonical_thread_id = excluded.canonical_thread_id,
    reason = excluded.reason`, threadID, accountID, job.CanonicalThreadID, timestamp); err != nil {
			return fmt.Errorf("persist deferred merged thread alias: %w", err)
		}
	}
	return nil
}

func deferredProjectionTargetThreads(ctx context.Context, tx *sql.Tx, accountID, seed string) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS thread_rebuild_scope (
    message_id TEXT PRIMARY KEY
) WITHOUT ROWID`); err != nil {
		return nil, fmt.Errorf("create deferred repair scope: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+threadScopeTable); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+threadScopeTable+` (message_id) VALUES (?)`, seed); err != nil {
		return nil, err
	}
	nodes, err := loadThreadNodesFromScope(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	node := nodes[seed]
	if node == nil {
		return nil, nil
	}
	targets := map[string]struct{}{}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT thread_id FROM thread_messages WHERE message_id = ?`, seed).Scan(&current); err == nil {
		targets[current] = struct{}{}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read deferred seed membership: %w", err)
	}
	rfcTokens, relationTokens, fallbackTokens := threadNeighborTokens(nodes)
	for _, token := range relationTokens {
		rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT tm.thread_id
FROM messages AS m
JOIN thread_messages AS tm ON tm.message_id = m.message_id
JOIN threads AS t ON t.thread_id = tm.thread_id AND t.account_id = ?
WHERE m.account_id = ? AND m.message_id <> ?
  AND lower(trim(trim(m.rfc_message_id), '<>')) = ?
ORDER BY tm.thread_id
LIMIT ?`, accountID, accountID, seed, token, maxThreadProjectionRepairTargets+1)
		if err != nil {
			return nil, fmt.Errorf("find deferred RFC thread targets: %w", err)
		}
		for rows.Next() {
			var threadID string
			if err := rows.Scan(&threadID); err != nil {
				rows.Close()
				return nil, err
			}
			targets[threadID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	edgeTokens := append(append([]string{}, rfcTokens...), relationTokens...)
	edgeTokens = append(edgeTokens, fallbackTokens...)
	for _, token := range uniqueSortedStrings(edgeTokens) {
		rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT tm.thread_id
FROM thread_edges AS e
JOIN thread_messages AS tm ON tm.message_id = e.child_message_id
JOIN threads AS t ON t.thread_id = tm.thread_id AND t.account_id = ?
WHERE e.account_id = ? AND e.child_message_id <> ? AND e.token = ?
  AND (e.relation = 'fallback' OR e.status <> 'malformed')
ORDER BY tm.thread_id
LIMIT ?`, accountID, accountID, seed, token, maxThreadProjectionRepairTargets+1)
		if err != nil {
			return nil, fmt.Errorf("find deferred edge thread targets: %w", err)
		}
		for rows.Next() {
			var threadID string
			if err := rows.Scan(&threadID); err != nil {
				rows.Close()
				return nil, err
			}
			targets[threadID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	values := make([]string, 0, len(targets))
	for threadID := range targets {
		values = append(values, threadID)
	}
	sort.Strings(values)
	return values, nil
}

func deferredProjectionMembershipPage(ctx context.Context, tx *sql.Tx, accountID string, threadIDs []string, cursor string, limit int) ([]string, bool, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(threadIDs)), ",")
	args := make([]any, 0, len(threadIDs)+3)
	args = append(args, accountID)
	for _, threadID := range threadIDs {
		args = append(args, threadID)
	}
	args = append(args, strings.TrimSpace(cursor), limit+1)
	rows, err := tx.QueryContext(ctx, `
SELECT tm.message_id
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.thread_id IN (`+placeholders+`) AND tm.message_id > ?
ORDER BY tm.message_id
LIMIT ?`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("page deferred thread membership: %w", err)
	}
	defer rows.Close()
	values := make([]string, 0, limit+1)
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			return nil, false, err
		}
		values = append(values, messageID)
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

func nullableRepairText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func loadThreadNodesFromScope(ctx context.Context, tx *sql.Tx, accountID string) (map[string]*threadNode, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT m.message_id, m.account_id, m.rfc_message_id, m.in_reply_to, m.subject, m.snippet,
       m.from_email, m.reply_to_email, m.sort_at, m.attachment_count, m.created_at, m.updated_at,
       EXISTS (
           SELECT 1
           FROM outbound_attempts AS oa
           WHERE oa.account_id = m.account_id
             AND oa.proposed_message_id = m.message_id
             AND oa.source_message_id <> ''
       )
FROM messages AS m
JOIN `+threadScopeTable+` AS scoped ON scoped.message_id = m.message_id
WHERE m.account_id = ?
ORDER BY COALESCE(m.sort_at, m.created_at), m.message_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("load messages for thread reconstruction: %w", err)
	}
	nodes := map[string]*threadNode{}
	for rows.Next() {
		var node threadNode
		var sortAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&node.ID, &node.AccountID, &node.RFCMessageID, &node.InReplyTo,
			&node.Subject, &node.Snippet, &node.FromEmail, &node.ReplyToEmail,
			&sortAt, &node.AttachmentCount, &createdAt, &updatedAt, &node.SuppressFallback,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read message for thread reconstruction: %w", err)
		}
		if sortAt.Valid {
			node.SortAt, _ = time.Parse(time.RFC3339Nano, sortAt.String)
		}
		node.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		node.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		if node.SortAt.IsZero() {
			node.SortAt = node.CreatedAt
		}
		nodes[node.ID] = &node
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close messages for thread reconstruction: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages for thread reconstruction: %w", err)
	}
	if len(nodes) == 0 {
		return nodes, nil
	}

	referenceRows, err := tx.QueryContext(ctx, `
SELECT r.message_id, r.reference
FROM message_references AS r
JOIN `+threadScopeTable+` AS scoped ON scoped.message_id = r.message_id
ORDER BY r.message_id, r.ordinal`)
	if err != nil {
		return nil, fmt.Errorf("load references for thread reconstruction: %w", err)
	}
	for referenceRows.Next() {
		var messageID, reference string
		if err := referenceRows.Scan(&messageID, &reference); err != nil {
			referenceRows.Close()
			return nil, fmt.Errorf("read reference for thread reconstruction: %w", err)
		}
		if node := nodes[messageID]; node != nil {
			node.References = append(node.References, reference)
		}
	}
	if err := referenceRows.Close(); err != nil {
		return nil, fmt.Errorf("close references for thread reconstruction: %w", err)
	}
	if err := referenceRows.Err(); err != nil {
		return nil, fmt.Errorf("load references for thread reconstruction: %w", err)
	}

	recipientRows, err := tx.QueryContext(ctx, `
SELECT r.message_id, r.email_address
FROM message_recipients AS r
JOIN `+threadScopeTable+` AS scoped ON scoped.message_id = r.message_id
WHERE r.kind IN ('to', 'cc')
ORDER BY r.message_id, r.kind, r.ordinal`)
	if err != nil {
		return nil, fmt.Errorf("load participants for thread reconstruction: %w", err)
	}
	for recipientRows.Next() {
		var messageID, email string
		if err := recipientRows.Scan(&messageID, &email); err != nil {
			recipientRows.Close()
			return nil, fmt.Errorf("read participant for thread reconstruction: %w", err)
		}
		if node := nodes[messageID]; node != nil {
			node.Recipients = append(node.Recipients, canonicalEmail(email))
		}
	}
	if err := recipientRows.Close(); err != nil {
		return nil, fmt.Errorf("close participants for thread reconstruction: %w", err)
	}
	if err := recipientRows.Err(); err != nil {
		return nil, fmt.Errorf("load participants for thread reconstruction: %w", err)
	}

	locationRows, err := tx.QueryContext(ctx, `
SELECT l.message_id, l.folder_id,
       MAX(CASE WHEN `+unreadLocationSQL("l", "f")+` THEN 1 ELSE 0 END)
FROM message_locations AS l
JOIN `+threadScopeTable+` AS scoped ON scoped.message_id = l.message_id
JOIN folders AS f ON f.folder_id = l.folder_id
WHERE l.account_id = ? AND l.present = 1 AND l.deleted = 0
GROUP BY l.message_id, l.folder_id
ORDER BY l.message_id, l.folder_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("load locations for thread reconstruction: %w", err)
	}
	for locationRows.Next() {
		var messageID, folderID string
		var unread int
		if err := locationRows.Scan(&messageID, &folderID, &unread); err != nil {
			locationRows.Close()
			return nil, fmt.Errorf("read location for thread reconstruction: %w", err)
		}
		if node := nodes[messageID]; node != nil {
			node.FolderIDs = append(node.FolderIDs, folderID)
			node.Unread = node.Unread || unread == 1
		}
	}
	if err := locationRows.Close(); err != nil {
		return nil, fmt.Errorf("close locations for thread reconstruction: %w", err)
	}
	if err := locationRows.Err(); err != nil {
		return nil, fmt.Errorf("load locations for thread reconstruction: %w", err)
	}
	return nodes, nil
}

func loadAffectedThreadNodes(ctx context.Context, tx *sql.Tx, accountID string, seedMessageIDs []string) (map[string]*threadNode, error) {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS thread_rebuild_scope (
    message_id TEXT PRIMARY KEY
) WITHOUT ROWID`); err != nil {
		return nil, fmt.Errorf("create affected thread scope: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+threadScopeTable); err != nil {
		return nil, fmt.Errorf("clear affected thread scope: %w", err)
	}
	for _, messageID := range uniqueSortedStrings(seedMessageIDs) {
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO `+threadScopeTable+` (message_id)
SELECT message_id FROM messages WHERE account_id = ? AND message_id = ?`, accountID, messageID); err != nil {
			return nil, fmt.Errorf("seed affected thread scope: %w", err)
		}
	}
	for {
		before, err := threadScopeCount(ctx, tx)
		if err != nil {
			return nil, err
		}
		if before == 0 {
			return map[string]*threadNode{}, nil
		}
		if before > maxAffectedThreadMessages {
			return nil, fmt.Errorf("%w: maximum %d messages", errThreadProjectionScopeLimit, maxAffectedThreadMessages)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO `+threadScopeTable+` (message_id)
SELECT related.message_id
FROM thread_messages AS seed
JOIN `+threadScopeTable+` AS scoped ON scoped.message_id = seed.message_id
JOIN threads AS t ON t.thread_id = seed.thread_id AND t.account_id = ?
JOIN thread_messages AS related ON related.thread_id = t.thread_id`, accountID); err != nil {
			return nil, fmt.Errorf("expand affected prior thread membership: %w", err)
		}

		nodes, err := loadThreadNodesFromScope(ctx, tx, accountID)
		if err != nil {
			return nil, err
		}
		rfcTokens, relationTokens, fallbackTokens := threadNeighborTokens(nodes)
		if err := addThreadIdentityNeighbors(ctx, tx, accountID, relationTokens); err != nil {
			return nil, err
		}
		edgeTokens := append(append([]string{}, rfcTokens...), relationTokens...)
		edgeTokens = append(edgeTokens, fallbackTokens...)
		if err := addThreadEdgeNeighbors(ctx, tx, accountID, uniqueSortedStrings(edgeTokens)); err != nil {
			return nil, err
		}
		after, err := threadScopeCount(ctx, tx)
		if err != nil {
			return nil, err
		}
		if after == before {
			return loadThreadNodesFromScope(ctx, tx, accountID)
		}
	}
}

func threadScopeCount(ctx context.Context, tx *sql.Tx) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+threadScopeTable).Scan(&count); err != nil {
		return 0, fmt.Errorf("count affected thread scope: %w", err)
	}
	return count, nil
}

func threadNeighborTokens(nodes map[string]*threadNode) ([]string, []string, []string) {
	rfcTokens := []string{}
	relationTokens := []string{}
	fallbackTokens := []string{}
	for _, node := range nodes {
		if token, valid := normalizeThreadToken(node.RFCMessageID); valid {
			rfcTokens = append(rfcTokens, token)
		}
		for _, value := range append(append([]string{}, node.References...), node.InReplyTo) {
			if token, valid := normalizeThreadToken(value); valid {
				relationTokens = append(relationTokens, token)
			}
		}
		if strings.TrimSpace(node.InReplyTo) == "" && len(node.References) == 0 {
			if key := fallbackThreadKey(node); key != "" {
				fallbackTokens = append(fallbackTokens, key)
			}
		}
	}
	return uniqueSortedStrings(rfcTokens), uniqueSortedStrings(relationTokens), uniqueSortedStrings(fallbackTokens)
}

func addThreadIdentityNeighbors(ctx context.Context, tx *sql.Tx, accountID string, tokens []string) error {
	for _, batch := range stringBatches(uniqueSortedStrings(tokens), threadReconstructionBatchSize) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		arguments := make([]any, 0, len(batch)+1)
		arguments = append(arguments, accountID)
		for _, token := range batch {
			arguments = append(arguments, token)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO `+threadScopeTable+` (message_id)
SELECT m.message_id
FROM messages AS m
WHERE m.account_id = ?
  AND lower(trim(trim(m.rfc_message_id), '<>')) IN (`+placeholders+`)`, arguments...); err != nil {
			return fmt.Errorf("expand affected RFC parents: %w", err)
		}
	}
	return nil
}

func addThreadEdgeNeighbors(ctx context.Context, tx *sql.Tx, accountID string, tokens []string) error {
	for _, batch := range stringBatches(uniqueSortedStrings(tokens), threadReconstructionBatchSize) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		arguments := make([]any, 0, len(batch)+1)
		arguments = append(arguments, accountID)
		for _, token := range batch {
			arguments = append(arguments, token)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO `+threadScopeTable+` (message_id)
SELECT child_message_id
FROM thread_edges
WHERE account_id = ? AND token IN (`+placeholders+`)
  AND (relation = 'fallback' OR status <> 'malformed')`, arguments...); err != nil {
			return fmt.Errorf("expand affected thread-edge neighbors: %w", err)
		}
	}
	return nil
}

func loadExistingThreadMembershipForMessages(ctx context.Context, tx *sql.Tx, accountID string, messageIDs []string) (map[string]string, map[string]time.Time, error) {
	membership := map[string]string{}
	created := map[string]time.Time{}
	for _, batch := range stringBatches(uniqueSortedStrings(messageIDs), threadReconstructionBatchSize) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		arguments := make([]any, 0, len(batch)+1)
		arguments = append(arguments, accountID)
		for _, messageID := range batch {
			arguments = append(arguments, messageID)
		}
		rows, err := tx.QueryContext(ctx, `
SELECT tm.message_id, t.thread_id, t.created_at
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.message_id IN (`+placeholders+`)`, arguments...)
		if err != nil {
			return nil, nil, fmt.Errorf("load affected thread memberships: %w", err)
		}
		for rows.Next() {
			var messageID, threadID, createdAt string
			if err := rows.Scan(&messageID, &threadID, &createdAt); err != nil {
				rows.Close()
				return nil, nil, fmt.Errorf("read affected thread membership: %w", err)
			}
			membership[messageID] = threadID
			if value, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
				created[threadID] = value
			}
		}
		if err := rows.Close(); err != nil {
			return nil, nil, fmt.Errorf("close affected thread memberships: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("load affected thread memberships: %w", err)
		}
	}
	return membership, created, nil
}

func deleteThreadRowsForMessages(ctx context.Context, tx *sql.Tx, accountID, table, column string, messageIDs []string) error {
	for _, batch := range stringBatches(uniqueSortedStrings(messageIDs), threadReconstructionBatchSize) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		arguments := make([]any, 0, len(batch)+1)
		statement := `DELETE FROM ` + table + ` WHERE ` + column + ` IN (` + placeholders + `)`
		for _, messageID := range batch {
			arguments = append(arguments, messageID)
		}
		if accountID != "" {
			statement += ` AND account_id = ?`
			arguments = append(arguments, accountID)
		}
		if _, err := tx.ExecContext(ctx, statement, arguments...); err != nil {
			return err
		}
	}
	return nil
}

func sortedNodeIDs(nodes map[string]*threadNode) []string {
	values := make([]string, 0, len(nodes))
	for messageID := range nodes {
		values = append(values, messageID)
	}
	sort.Slice(values, func(i, j int) bool { return threadNodeLess(nodes[values[i]], nodes[values[j]]) })
	return values
}

func uniqueSortedStrings(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringBatches(values []string, size int) [][]string {
	if size < 1 {
		size = 1
	}
	result := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		result = append(result, values[start:end])
	}
	return result
}

func buildThreadComponents(nodes map[string]*threadNode) (map[string][]string, []graphEdge) {
	union := newThreadUnion(nodes)
	byRFC := map[string]string{}
	for _, node := range nodes {
		if token, valid := normalizeThreadToken(node.RFCMessageID); valid {
			byRFC[token] = node.ID
		}
	}
	unresolved := map[string][]string{}
	edges := []graphEdge{}
	adjacency := map[string][]string{}
	for _, node := range sortedThreadNodes(nodes) {
		relations := make([]struct {
			kind    string
			ordinal int
			value   string
		}, 0, len(node.References)+1)
		for index, reference := range node.References {
			relations = append(relations, struct {
				kind    string
				ordinal int
				value   string
			}{"reference", index, reference})
		}
		if strings.TrimSpace(node.InReplyTo) != "" {
			relations = append(relations, struct {
				kind    string
				ordinal int
				value   string
			}{"in_reply_to", 0, node.InReplyTo})
		}
		seenRelation := map[string]struct{}{}
		for _, relation := range relations {
			token, valid := normalizeThreadToken(relation.value)
			storedToken := token
			if !valid {
				storedToken = strings.TrimSpace(relation.value)
			}
			key := relation.kind + "\x00" + storedToken
			if _, exists := seenRelation[key]; exists {
				continue
			}
			seenRelation[key] = struct{}{}
			edge := graphEdge{ChildMessageID: node.ID, Relation: relation.kind, Ordinal: relation.ordinal, Token: storedToken}
			if !valid {
				edge.Status = "malformed"
			} else if parentID := byRFC[token]; parentID != "" {
				edge.ParentMessageID = parentID
				if parentID == node.ID {
					edge.Status = "self"
				} else {
					edge.Status = "resolved"
					union.join(node.ID, parentID)
					adjacency[node.ID] = append(adjacency[node.ID], parentID)
				}
			} else {
				edge.Status = "unresolved"
				unresolved[token] = append(unresolved[token], node.ID)
			}
			edge.ID = stableThreadEvidenceID(node.AccountID, node.ID, edge.Relation, fmt.Sprintf("%d", edge.Ordinal), edge.Token)
			edges = append(edges, edge)
		}
	}
	for _, messageIDs := range unresolved {
		for index := 1; index < len(messageIDs); index++ {
			union.join(messageIDs[0], messageIDs[index])
		}
	}

	fallbackBuckets := map[string][]*threadNode{}
	for _, node := range nodes {
		if node.SuppressFallback {
			continue
		}
		if strings.TrimSpace(node.InReplyTo) != "" || len(node.References) > 0 {
			continue
		}
		key := fallbackThreadKey(node)
		if key != "" {
			fallbackBuckets[key] = append(fallbackBuckets[key], node)
		}
	}
	for key, bucket := range fallbackBuckets {
		sort.Slice(bucket, func(i, j int) bool { return threadNodeLess(bucket[i], bucket[j]) })
		groups := [][]*threadNode{}
		for _, node := range bucket {
			if !replyLikeSubject(node.Subject) || len(groups) == 0 ||
				node.SortAt.Sub(groups[len(groups)-1][len(groups[len(groups)-1])-1].SortAt) > 14*24*time.Hour {
				groups = append(groups, []*threadNode{node})
			} else {
				groups[len(groups)-1] = append(groups[len(groups)-1], node)
			}
		}
		for _, group := range groups {
			grouped := len(group) > 1
			if grouped {
				for index := 1; index < len(group); index++ {
					union.join(group[0].ID, group[index].ID)
				}
			}
			for _, node := range group {
				status := "isolated"
				if grouped {
					status = "grouped"
				}
				edges = append(edges, graphEdge{
					ID:             stableThreadEvidenceID(node.AccountID, node.ID, "fallback", "0", key),
					ChildMessageID: node.ID, Relation: "fallback", Token: key, Status: status,
				})
			}
		}
	}

	for index := range edges {
		if edges[index].Status == "resolved" && pathReaches(adjacency, edges[index].ParentMessageID, edges[index].ChildMessageID, map[string]bool{}) {
			edges[index].Status = "cycle"
		}
	}
	components := map[string][]string{}
	for messageID := range nodes {
		root := union.find(messageID)
		components[root] = append(components[root], messageID)
	}
	for root := range components {
		sort.Slice(components[root], func(i, j int) bool {
			return threadNodeLess(nodes[components[root][i]], nodes[components[root][j]])
		})
	}
	return components, edges
}

func aggregateThreadComponents(
	ctx context.Context,
	tx *sql.Tx,
	accountID string,
	nodes map[string]*threadNode,
	components map[string][]string,
	existing map[string]string,
	created map[string]time.Time,
) ([]threadAggregate, map[string]string, error) {
	type componentPlan struct {
		key        string
		messageIDs []string
	}
	plans := make([]componentPlan, 0, len(components))
	for key, messageIDs := range components {
		plans = append(plans, componentPlan{key: key, messageIDs: append([]string(nil), messageIDs...)})
	}
	sort.Slice(plans, func(i, j int) bool {
		left, right := nodes[plans[i].messageIDs[0]], nodes[plans[j].messageIDs[0]]
		if threadNodeLess(left, right) {
			return true
		}
		if threadNodeLess(right, left) {
			return false
		}
		return plans[i].key < plans[j].key
	})

	// One old thread ID may appear in multiple new components after stronger RFC
	// evidence replaces an earlier fallback. Preserve it for exactly one owner:
	// the component containing its original provisional anchor when possible,
	// otherwise the earliest component. Other split components receive new IDs.
	threadComponents := map[string]map[string]struct{}{}
	for _, plan := range plans {
		for _, messageID := range plan.messageIDs {
			if threadID := existing[messageID]; threadID != "" {
				if threadComponents[threadID] == nil {
					threadComponents[threadID] = map[string]struct{}{}
				}
				threadComponents[threadID][plan.key] = struct{}{}
			}
		}
	}
	owners := map[string]string{}
	for threadID, candidates := range threadComponents {
		owner := ""
		for _, plan := range plans {
			if _, ok := candidates[plan.key]; !ok {
				continue
			}
			for _, messageID := range plan.messageIDs {
				if provisionalThreadID(messageID) == threadID {
					owner = plan.key
					break
				}
			}
			if owner != "" {
				break
			}
		}
		if owner == "" {
			for _, plan := range plans {
				if _, ok := candidates[plan.key]; ok {
					owner = plan.key
					break
				}
			}
		}
		owners[threadID] = owner
	}

	aggregates := make([]threadAggregate, 0, len(plans))
	retired := map[string]string{}
	allocated := map[string]struct{}{}
	for _, plan := range plans {
		ownedExisting := []string{}
		for threadID, owner := range owners {
			if owner == plan.key {
				ownedExisting = append(ownedExisting, threadID)
			}
		}
		sort.Slice(ownedExisting, func(i, j int) bool {
			return threadIdentityLess(ownedExisting[i], ownedExisting[j], created)
		})
		threadID := ""
		if len(ownedExisting) > 0 {
			threadID = ownedExisting[0]
		}
		if threadID == "" {
			var err error
			threadID, err = allocateThreadID(ctx, tx, accountID, plan.messageIDs, allocated)
			if err != nil {
				return nil, nil, err
			}
		}
		allocated[threadID] = struct{}{}
		for _, candidate := range ownedExisting {
			if candidate != threadID {
				retired[candidate] = threadID
			}
		}

		messageIDs := plan.messageIDs
		aggregate := threadAggregate{ID: threadID, MessageIDs: append([]string(nil), messageIDs...)}
		participants := map[string]struct{}{}
		folders := map[string]struct{}{}
		for index, messageID := range messageIDs {
			node := nodes[messageID]
			if index == 0 {
				aggregate.FirstMessageAt = node.SortAt
				aggregate.CreatedAt = node.CreatedAt
			}
			aggregate.LastMessageAt = node.SortAt
			if node.UpdatedAt.After(aggregate.UpdatedAt) {
				aggregate.UpdatedAt = node.UpdatedAt
			}
			if node.CreatedAt.Before(aggregate.CreatedAt) || aggregate.CreatedAt.IsZero() {
				aggregate.CreatedAt = node.CreatedAt
			}
			if node.Unread {
				aggregate.UnreadCount++
			}
			aggregate.AttachmentCount += node.AttachmentCount
			if node.Snippet != "" {
				aggregate.Snippet = node.Snippet
			}
			for _, value := range append([]string{node.FromEmail, node.ReplyToEmail}, node.Recipients...) {
				if value = canonicalEmail(value); value != "" {
					participants[value] = struct{}{}
				}
			}
			for _, folderID := range node.FolderIDs {
				if folderID = strings.TrimSpace(folderID); folderID != "" {
					folders[folderID] = struct{}{}
				}
			}
		}
		aggregate.MessageCount = len(messageIDs)
		aggregate.Participants = sortedSet(participants)
		aggregate.FolderIDs = sortedSet(folders)
		aggregate.RootRFCMessageID = componentRootToken(nodes, messageIDs)
		aggregate.Subject = componentSubject(nodes, messageIDs, aggregate.RootRFCMessageID)
		if aggregate.UpdatedAt.IsZero() {
			aggregate.UpdatedAt = aggregate.LastMessageAt
		}
		if existingCreated := created[threadID]; !existingCreated.IsZero() {
			aggregate.CreatedAt = existingCreated
		}
		aggregates = append(aggregates, aggregate)
	}
	sort.Slice(aggregates, func(i, j int) bool {
		if aggregates[i].LastMessageAt.Equal(aggregates[j].LastMessageAt) {
			return aggregates[i].ID < aggregates[j].ID
		}
		return aggregates[i].LastMessageAt.Before(aggregates[j].LastMessageAt)
	})
	return aggregates, retired, nil
}

func allocateThreadID(ctx context.Context, tx *sql.Tx, accountID string, messageIDs []string, allocated map[string]struct{}) (string, error) {
	for _, messageID := range messageIDs {
		candidate := provisionalThreadID(messageID)
		if _, used := allocated[candidate]; used {
			continue
		}
		available, err := threadIDAvailable(ctx, tx, accountID, candidate)
		if err != nil {
			return "", err
		}
		if available {
			return candidate, nil
		}
	}
	for attempts := 0; attempts < 8; attempts++ {
		candidate, err := id.New("thr")
		if err != nil {
			return "", fmt.Errorf("allocate reconstructed thread ID: %w", err)
		}
		if _, used := allocated[candidate]; used {
			continue
		}
		available, err := threadIDAvailable(ctx, tx, accountID, candidate)
		if err != nil {
			return "", err
		}
		if available {
			return candidate, nil
		}
	}
	return "", errors.New("allocate reconstructed thread ID: exhausted collision retries")
}

func threadIDAvailable(ctx context.Context, tx *sql.Tx, accountID, threadID string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT (
    SELECT count(*) FROM threads WHERE thread_id = ?
) + (
    SELECT count(*) FROM thread_aliases WHERE alias_thread_id = ?
)`, threadID, threadID).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect reconstructed thread ID availability for %s: %w", accountID, err)
	}
	return count == 0, nil
}

func (repository *ThreadRepository) Latest(ctx context.Context, accountID string, limit int) ([]maildomain.Thread, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list latest threads: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, threadColumns+`
WHERE account_id = ?
ORDER BY last_message_at DESC, thread_id DESC
LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list latest threads: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Thread, 0, limit+1)
	for rows.Next() {
		value, err := scanThread(rows)
		if err != nil {
			return nil, false, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list latest threads: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	if err := applyThreadProjectionState(ctx, repository.db, accountID, values); err != nil {
		return nil, false, err
	}
	return values, more, nil
}

func (repository *ThreadRepository) Show(ctx context.Context, accountID, selector, cursor string, limit int) (maildomain.ThreadPage, error) {
	if limit < 1 {
		return maildomain.ThreadPage{}, errors.New("show thread: limit must be positive")
	}
	threadID, redirectedFrom, err := repository.resolve(ctx, accountID, selector)
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	thread, err := scanThread(repository.db.QueryRowContext(ctx, threadColumns+` WHERE account_id = ? AND thread_id = ?`, strings.TrimSpace(accountID), threadID))
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	projection := []maildomain.Thread{thread}
	if err := applyThreadProjectionState(ctx, repository.db, accountID, projection); err != nil {
		return maildomain.ThreadPage{}, err
	}
	thread = projection[0]
	thread.RedirectedFrom = redirectedFrom
	cursorValue, err := decodeThreadCursor(cursor, threadID)
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	arguments := []any{strings.TrimSpace(accountID), threadID}
	cursorClause := ""
	if cursorValue.MessageID != "" {
		cursorClause = `
AND (
    COALESCE(m.sort_at, m.created_at) > ?
    OR (COALESCE(m.sort_at, m.created_at) = ? AND m.message_id > ?)
)`
		arguments = append(arguments, cursorValue.SortAt, cursorValue.SortAt, cursorValue.MessageID)
	}
	arguments = append(arguments, limit+1)
	rows, err := repository.db.QueryContext(ctx, messageColumns+`
JOIN thread_messages AS tm ON tm.message_id = m.message_id
WHERE m.account_id = ? AND tm.thread_id = ?
`+cursorClause+`
ORDER BY COALESCE(m.sort_at, m.created_at), m.message_id
LIMIT ?`, arguments...)
	if err != nil {
		return maildomain.ThreadPage{}, fmt.Errorf("read thread messages: %w", err)
	}
	messages := make([]maildomain.Message, 0, limit+1)
	for rows.Next() {
		value, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return maildomain.ThreadPage{}, err
		}
		messages = append(messages, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return maildomain.ThreadPage{}, fmt.Errorf("read thread messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return maildomain.ThreadPage{}, fmt.Errorf("close thread messages: %w", err)
	}
	more := len(messages) > limit
	if more {
		messages = messages[:limit]
	}
	mailRepository := &MailRepository{db: repository.db}
	for index := range messages {
		if err := mailRepository.loadMessageDetails(ctx, &messages[index]); err != nil {
			return maildomain.ThreadPage{}, err
		}
		if err := mailRepository.loadMessageContent(ctx, &messages[index]); err != nil {
			return maildomain.ThreadPage{}, err
		}
		if err := mailRepository.loadLocalRawSource(ctx, &messages[index]); err != nil {
			return maildomain.ThreadPage{}, err
		}
	}
	thread.Messages = messages
	edges, edgeEvidenceTruncated, err := repository.edges(ctx, accountID, messageIDs(messages))
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	thread.Edges = edges
	thread.EdgeEvidenceTruncated = edgeEvidenceTruncated
	nextCursor := ""
	if more && len(messages) > 0 {
		last := messages[len(messages)-1]
		sortAt := last.CreatedAt
		if last.SortAt != nil {
			sortAt = *last.SortAt
		}
		nextCursor, err = encodeThreadCursor(threadID, sortAt, last.ID)
		if err != nil {
			return maildomain.ThreadPage{}, err
		}
	}
	return maildomain.ThreadPage{Thread: thread, More: more, NextCursor: nextCursor, Edges: edges}, nil
}

func applyThreadProjectionState(ctx context.Context, db *sql.DB, accountID string, threads []maildomain.Thread) error {
	if len(threads) == 0 {
		return nil
	}
	accountID = strings.TrimSpace(accountID)
	state, reason, updatedAt := "ready", "", ""
	var pendingSince sql.NullString
	err := db.QueryRowContext(ctx, `
SELECT state, reason, pending_since, updated_at
FROM thread_projection_state
WHERE account_id = ?`, accountID).Scan(&state, &reason, &pendingSince, &updatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read thread projection state: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		state, reason, updatedAt = "ready", "", ""
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM thread_projection_jobs WHERE account_id = ?`, accountID).Scan(&pending); err != nil {
		return fmt.Errorf("count pending thread projection jobs: %w", err)
	}
	var updated *time.Time
	if parsed, parseErr := time.Parse(time.RFC3339Nano, updatedAt); parseErr == nil && !parsed.IsZero() {
		value := parsed.UTC()
		updated = &value
	}
	for index := range threads {
		threads[index].ProjectionState = state
		threads[index].ProjectionReason = reason
		threads[index].ProjectionPending = pending
		threads[index].ProjectionUpdatedAt = updated
	}
	return nil
}

func (repository *ThreadRepository) Resolve(ctx context.Context, accountID, selector string) (string, error) {
	threadID, _, err := repository.resolve(ctx, accountID, selector)
	return threadID, err
}

type storedThreadCursor struct {
	Version   int    `json:"v"`
	ThreadID  string `json:"thread"`
	SortAt    string `json:"sort_at"`
	MessageID string `json:"message"`
}

func encodeThreadCursor(threadID string, sortAt time.Time, messageID string) (string, error) {
	if strings.TrimSpace(threadID) == "" || sortAt.IsZero() || strings.TrimSpace(messageID) == "" {
		return "", maildomain.ErrInvalidThreadCursor
	}
	payload, err := json.Marshal(storedThreadCursor{
		Version: 1, ThreadID: threadID, SortAt: sortAt.UTC().Format(time.RFC3339Nano), MessageID: messageID,
	})
	if err != nil {
		return "", fmt.Errorf("encode mail thread cursor: %w", err)
	}
	return "cur_" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeThreadCursor(value, threadID string) (storedThreadCursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return storedThreadCursor{}, nil
	}
	if !strings.HasPrefix(value, "cur_") {
		return storedThreadCursor{}, maildomain.ErrInvalidThreadCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "cur_"))
	if err != nil {
		return storedThreadCursor{}, maildomain.ErrInvalidThreadCursor
	}
	var cursor storedThreadCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return storedThreadCursor{}, maildomain.ErrInvalidThreadCursor
	}
	if cursor.Version != 1 || cursor.ThreadID != threadID || !strings.HasPrefix(cursor.MessageID, "msg_") {
		return storedThreadCursor{}, maildomain.ErrInvalidThreadCursor
	}
	parsed, err := time.Parse(time.RFC3339Nano, cursor.SortAt)
	if err != nil || parsed.IsZero() {
		return storedThreadCursor{}, maildomain.ErrInvalidThreadCursor
	}
	cursor.SortAt = parsed.UTC().Format(time.RFC3339Nano)
	return cursor, nil
}

func (repository *ThreadRepository) resolve(ctx context.Context, accountID, selector string) (string, string, error) {
	accountID = strings.TrimSpace(accountID)
	selector = strings.TrimSpace(selector)
	if strings.HasPrefix(selector, "msg_") {
		var threadID string
		err := repository.db.QueryRowContext(ctx, `
SELECT tm.thread_id
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.message_id = ?`, accountID, selector).Scan(&threadID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", maildomain.ErrThreadNotFound
		}
		if err != nil {
			return "", "", fmt.Errorf("resolve thread by message: %w", err)
		}
		return threadID, "", nil
	}
	var threadID string
	err := repository.db.QueryRowContext(ctx, `SELECT thread_id FROM threads WHERE account_id = ? AND thread_id = ?`, accountID, selector).Scan(&threadID)
	if err == nil {
		return threadID, "", nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("resolve thread: %w", err)
	}
	err = repository.db.QueryRowContext(ctx, `
SELECT canonical_thread_id FROM thread_aliases WHERE account_id = ? AND alias_thread_id = ?`, accountID, selector).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", maildomain.ErrThreadNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve thread alias: %w", err)
	}
	return threadID, selector, nil
}

const maxVerboseThreadEdges = 500

func (repository *ThreadRepository) edges(ctx context.Context, accountID string, messageIDs []string) ([]maildomain.ThreadEdge, bool, error) {
	if len(messageIDs) == 0 {
		return nil, false, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(messageIDs)), ",")
	arguments := make([]any, 0, len(messageIDs)+1)
	arguments = append(arguments, strings.TrimSpace(accountID))
	for _, messageID := range messageIDs {
		arguments = append(arguments, messageID)
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT edge_id, account_id, child_message_id, relation, ordinal, token,
       COALESCE(parent_message_id, ''), status
FROM thread_edges
WHERE account_id = ? AND child_message_id IN (`+placeholders+`)
ORDER BY child_message_id, relation, ordinal, token
LIMIT ?`, append(arguments, maxVerboseThreadEdges+1)...)
	if err != nil {
		return nil, false, fmt.Errorf("read thread edge evidence: %w", err)
	}
	defer rows.Close()
	values := []maildomain.ThreadEdge{}
	for rows.Next() {
		var value maildomain.ThreadEdge
		if err := rows.Scan(&value.ID, &value.AccountID, &value.ChildMessageID, &value.Relation, &value.Ordinal, &value.Token, &value.ParentMessageID, &value.Status); err != nil {
			return nil, false, fmt.Errorf("read thread edge evidence: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read thread edge evidence: %w", err)
	}
	truncated := len(values) > maxVerboseThreadEdges
	if truncated {
		values = values[:maxVerboseThreadEdges]
	}
	return values, truncated, nil
}

const threadColumnList = `thread_id, account_id, root_rfc_message_id, canonical_subject, snippet,
       participants_json, folder_ids_json, message_count, unread_count,
       attachment_count, first_message_at, last_message_at, status,
       reconstruction, created_at, updated_at`

const threadColumns = `
SELECT ` + threadColumnList + `
FROM threads`

func scanThread(scanner interface{ Scan(...any) error }) (maildomain.Thread, error) {
	return scanThreadWithExtra(scanner)
}

func scanThreadWithExtra(scanner interface{ Scan(...any) error }, extra ...any) (maildomain.Thread, error) {
	var value maildomain.Thread
	var participantsJSON, foldersJSON string
	var firstMessageAt, lastMessageAt sql.NullString
	var createdAt, updatedAt string
	targets := []any{
		&value.ID, &value.AccountID, &value.RootRFCMessageID, &value.CanonicalSubject,
		&value.Snippet, &participantsJSON, &foldersJSON, &value.MessageCount,
		&value.UnreadCount, &value.AttachmentCount, &firstMessageAt, &lastMessageAt,
		&value.Status, &value.Reconstruction, &createdAt, &updatedAt,
	}
	targets = append(targets, extra...)
	if err := scanner.Scan(targets...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Thread{}, maildomain.ErrThreadNotFound
		}
		return maildomain.Thread{}, fmt.Errorf("read mail thread: %w", err)
	}
	if err := json.Unmarshal([]byte(participantsJSON), &value.Participants); err != nil {
		return maildomain.Thread{}, fmt.Errorf("decode thread participants: %w", err)
	}
	if err := json.Unmarshal([]byte(foldersJSON), &value.FolderIDs); err != nil {
		return maildomain.Thread{}, fmt.Errorf("decode thread folders: %w", err)
	}
	value.FirstMessageAt = parseThreadTime(firstMessageAt)
	value.LastMessageAt = parseThreadTime(lastMessageAt)
	value.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	value.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return value, nil
}

type threadUnion struct {
	parent map[string]string
	rank   map[string]int
}

func newThreadUnion(nodes map[string]*threadNode) *threadUnion {
	value := &threadUnion{parent: map[string]string{}, rank: map[string]int{}}
	for messageID := range nodes {
		value.parent[messageID] = messageID
	}
	return value
}

func (value *threadUnion) find(messageID string) string {
	parent := value.parent[messageID]
	if parent != messageID {
		value.parent[messageID] = value.find(parent)
	}
	return value.parent[messageID]
}

func (value *threadUnion) join(left, right string) {
	leftRoot, rightRoot := value.find(left), value.find(right)
	if leftRoot == rightRoot {
		return
	}
	if value.rank[leftRoot] < value.rank[rightRoot] {
		leftRoot, rightRoot = rightRoot, leftRoot
	}
	value.parent[rightRoot] = leftRoot
	if value.rank[leftRoot] == value.rank[rightRoot] {
		value.rank[leftRoot]++
	}
}

func sortedThreadNodes(nodes map[string]*threadNode) []*threadNode {
	values := make([]*threadNode, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, node)
	}
	sort.Slice(values, func(i, j int) bool { return threadNodeLess(values[i], values[j]) })
	return values
}

func threadNodeLess(left, right *threadNode) bool {
	if left.SortAt.Equal(right.SortAt) {
		return left.ID < right.ID
	}
	return left.SortAt.Before(right.SortAt)
}

func normalizedThreadReferences(values []string) []string {
	result := []string{}
	for _, value := range values {
		if normalized, valid := normalizeThreadToken(value); valid {
			result = append(result, normalized)
		}
	}
	return result
}

func fallbackThreadKey(node *threadNode) string {
	if node == nil || node.SuppressFallback {
		return ""
	}
	subject := normalizeThreadSubject(node.Subject)
	if subject == "" {
		return ""
	}
	participants := map[string]struct{}{}
	for _, value := range append([]string{node.FromEmail, node.ReplyToEmail}, node.Recipients...) {
		if value = canonicalEmail(value); value != "" {
			participants[value] = struct{}{}
		}
	}
	if len(participants) < 2 {
		return ""
	}
	return subject + "|" + strings.Join(sortedSet(participants), ",")
}

func normalizeThreadSubject(value string) string {
	value = strings.TrimSpace(value)
	for {
		lower := strings.ToLower(value)
		trimmed := value
		for _, prefix := range []string{"re:", "fw:", "fwd:"} {
			if strings.HasPrefix(lower, prefix) {
				trimmed = strings.TrimSpace(value[len(prefix):])
				break
			}
		}
		if trimmed == value {
			break
		}
		value = trimmed
	}
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func replyLikeSubject(subject string) bool {
	lower := strings.ToLower(strings.TrimSpace(subject))
	return strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "fw:") || strings.HasPrefix(lower, "fwd:")
}

func pathReaches(adjacency map[string][]string, current, target string, seen map[string]bool) bool {
	if current == target {
		return true
	}
	if current == "" || seen[current] {
		return false
	}
	seen[current] = true
	for _, next := range adjacency[current] {
		if pathReaches(adjacency, next, target, seen) {
			return true
		}
	}
	return false
}

func componentRootToken(nodes map[string]*threadNode, messageIDs []string) string {
	known := map[string]*threadNode{}
	for _, messageID := range messageIDs {
		if token, valid := normalizeThreadToken(nodes[messageID].RFCMessageID); valid {
			known[token] = nodes[messageID]
		}
	}
	for _, messageID := range messageIDs {
		node := nodes[messageID]
		if strings.TrimSpace(node.InReplyTo) == "" && len(node.References) == 0 {
			if token, valid := normalizeThreadToken(node.RFCMessageID); valid {
				return token
			}
		}
	}
	for _, messageID := range messageIDs {
		node := nodes[messageID]
		for _, token := range normalizedThreadReferences(node.References) {
			if _, exists := known[token]; !exists {
				return token
			}
		}
		if token, valid := normalizeThreadToken(node.InReplyTo); valid {
			if _, exists := known[token]; !exists {
				return token
			}
		}
	}
	for _, messageID := range messageIDs {
		if token, valid := normalizeThreadToken(nodes[messageID].RFCMessageID); valid {
			return token
		}
	}
	return ""
}

func componentSubject(nodes map[string]*threadNode, messageIDs []string, rootToken string) string {
	if rootToken != "" {
		for _, messageID := range messageIDs {
			token, valid := normalizeThreadToken(nodes[messageID].RFCMessageID)
			if valid && token == rootToken && strings.TrimSpace(nodes[messageID].Subject) != "" {
				return strings.TrimSpace(nodes[messageID].Subject)
			}
		}
	}
	for _, messageID := range messageIDs {
		if subject := strings.TrimSpace(nodes[messageID].Subject); subject != "" {
			return subject
		}
	}
	return ""
}

func threadIdentityLess(left, right string, created map[string]time.Time) bool {
	leftTime, rightTime := created[left], created[right]
	if leftTime.Equal(rightTime) {
		return left < right
	}
	if leftTime.IsZero() {
		return false
	}
	if rightTime.IsZero() {
		return true
	}
	return leftTime.Before(rightTime)
}

func provisionalThreadID(messageID string) string {
	if strings.HasPrefix(messageID, "msg_") {
		return "thr_" + strings.TrimPrefix(messageID, "msg_")
	}
	sum := sha256.Sum256([]byte(messageID))
	return "thr_" + hex.EncodeToString(sum[:16])
}

func stableThreadEvidenceID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "ted_" + hex.EncodeToString(sum[:16])
}

func canonicalEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeThreadToken(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") && len(value) > 2 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" || strings.ContainsAny(value, "<>") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", false
	}
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 || strings.Contains(value[:at], "@") {
		return "", false
	}
	return strings.ToLower(value), true
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func nullableThreadTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseThreadTime(value sql.NullString) time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value.String)
	return parsed
}

func nullableThreadString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func messageIDs(messages []maildomain.Message) []string {
	values := make([]string, 0, len(messages))
	for _, message := range messages {
		values = append(values, message.ID)
	}
	return values
}
