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

func ensureThreadGraphBackfill(ctx context.Context, db *sql.DB) error {
	var complete string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_metadata WHERE key = 'thread_graph_backfill_v1'`).Scan(&complete)
	if err == nil && complete == "complete" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read thread graph backfill state: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin thread graph backfill: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT account_id FROM accounts ORDER BY account_id`)
	if err != nil {
		return fmt.Errorf("list accounts for thread graph backfill: %w", err)
	}
	accountIDs := []string{}
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			rows.Close()
			return fmt.Errorf("read thread graph backfill account: %w", err)
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close thread graph backfill accounts: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list accounts for thread graph backfill: %w", err)
	}
	now := time.Now().UTC()
	for _, accountID := range accountIDs {
		if err := rebuildAccountThreadGraph(ctx, tx, accountID, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO app_metadata (key, value, updated_at)
VALUES ('thread_graph_backfill_v1', 'complete', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("persist thread graph backfill state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit thread graph backfill: %w", err)
	}
	return nil
}

func rebuildAccountThreadGraph(ctx context.Context, tx *sql.Tx, accountID string, now time.Time) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("rebuild thread graph: account ID is required")
	}
	nodes, err := loadThreadNodes(ctx, tx, accountID)
	if err != nil {
		return err
	}
	return persistReconstructedThreadGraph(ctx, tx, accountID, nodes, now)
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
	threadScopeTable              = "temp.thread_rebuild_scope"
	maxAffectedThreadMessages     = 10000
	threadReconstructionBatchSize = 300
)

var errThreadProjectionScopeLimit = errors.New("affected thread reconstruction scope limit exceeded")

// rebuildCanonicalThreadProjection keeps derived thread work off the canonical
// ingestion frontier. An oversized but otherwise valid conversation may defer
// its thread projection, but it must never roll back durable message/location
// ingestion or a sync/backfill checkpoint. Other reconstruction failures still
// surface because they can indicate corruption rather than bounded-work pressure.
func rebuildCanonicalThreadProjection(ctx context.Context, tx *sql.Tx, accountID string, seedMessageIDs []string, now time.Time) error {
	err := rebuildAffectedThreadGraph(ctx, tx, accountID, seedMessageIDs, now)
	if errors.Is(err, errThreadProjectionScopeLimit) {
		return nil
	}
	return err
}

func loadThreadNodes(ctx context.Context, tx *sql.Tx, accountID string) (map[string]*threadNode, error) {
	return loadThreadNodesQuery(ctx, tx, accountID, false)
}

func loadThreadNodesFromScope(ctx context.Context, tx *sql.Tx, accountID string) (map[string]*threadNode, error) {
	return loadThreadNodesQuery(ctx, tx, accountID, true)
}

func loadThreadNodesQuery(ctx context.Context, tx *sql.Tx, accountID string, scoped bool) (map[string]*threadNode, error) {
	messageJoin := ""
	if scoped {
		messageJoin = " JOIN " + threadScopeTable + " AS scoped ON scoped.message_id = m.message_id"
	}
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
FROM messages AS m`+messageJoin+`
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

	referenceJoin := "JOIN messages AS m ON m.message_id = r.message_id"
	referenceWhere := "WHERE m.account_id = ?"
	referenceArgs := []any{accountID}
	if scoped {
		referenceJoin = "JOIN " + threadScopeTable + " AS scoped ON scoped.message_id = r.message_id"
		referenceWhere = ""
		referenceArgs = nil
	}
	referenceRows, err := tx.QueryContext(ctx, `
SELECT r.message_id, r.reference
FROM message_references AS r
`+referenceJoin+`
`+referenceWhere+`
ORDER BY r.message_id, r.ordinal`, referenceArgs...)
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

	recipientJoin := "JOIN messages AS m ON m.message_id = r.message_id"
	recipientWhere := "WHERE m.account_id = ? AND r.kind IN ('to', 'cc')"
	recipientArgs := []any{accountID}
	if scoped {
		recipientJoin = "JOIN " + threadScopeTable + " AS scoped ON scoped.message_id = r.message_id"
		recipientWhere = "WHERE r.kind IN ('to', 'cc')"
		recipientArgs = nil
	}
	recipientRows, err := tx.QueryContext(ctx, `
SELECT r.message_id, r.email_address
FROM message_recipients AS r
`+recipientJoin+`
`+recipientWhere+`
ORDER BY r.message_id, r.kind, r.ordinal`, recipientArgs...)
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

	locationScopeJoin := ""
	if scoped {
		locationScopeJoin = "JOIN " + threadScopeTable + " AS scoped ON scoped.message_id = l.message_id"
	}
	locationRows, err := tx.QueryContext(ctx, `
SELECT l.message_id, l.folder_id,
       MAX(CASE WHEN `+unreadLocationSQL("l", "f")+` THEN 1 ELSE 0 END)
FROM message_locations AS l
`+locationScopeJoin+`
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
	edges, err := repository.edges(ctx, accountID, messageIDs(messages))
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	thread.Edges = edges
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

func (repository *ThreadRepository) edges(ctx context.Context, accountID string, messageIDs []string) ([]maildomain.ThreadEdge, error) {
	if len(messageIDs) == 0 {
		return nil, nil
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
LIMIT 500`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("read thread edge evidence: %w", err)
	}
	defer rows.Close()
	values := []maildomain.ThreadEdge{}
	for rows.Next() {
		var value maildomain.ThreadEdge
		if err := rows.Scan(&value.ID, &value.AccountID, &value.ChildMessageID, &value.Relation, &value.Ordinal, &value.Token, &value.ParentMessageID, &value.Status); err != nil {
			return nil, fmt.Errorf("read thread edge evidence: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read thread edge evidence: %w", err)
	}
	return values, nil
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
