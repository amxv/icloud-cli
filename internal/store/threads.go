package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type ThreadRepository struct {
	db *sql.DB
}

type threadNode struct {
	ID              string
	AccountID       string
	RFCMessageID    string
	InReplyTo       string
	References      []string
	Subject         string
	Snippet         string
	FromEmail       string
	ReplyToEmail    string
	Recipients      []string
	FolderIDs       []string
	SortAt          time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Unread          bool
	AttachmentCount int
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
	if len(nodes) == 0 {
		return nil
	}
	existingMembership, threadCreated, err := loadExistingThreadMembership(ctx, tx, accountID)
	if err != nil {
		return err
	}
	components, edges := buildThreadComponents(nodes)
	aggregates, retired := aggregateThreadComponents(nodes, components, existingMembership, threadCreated)
	timestamp := now.UTC().Format(time.RFC3339Nano)

	if _, err := tx.ExecContext(ctx, `
DELETE FROM thread_edges WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("replace thread edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM thread_messages
WHERE thread_id IN (SELECT thread_id FROM threads WHERE account_id = ?)`, accountID); err != nil {
		return fmt.Errorf("replace thread memberships: %w", err)
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
	rows, err := tx.QueryContext(ctx, `SELECT thread_id FROM threads WHERE account_id = ?`, accountID)
	if err != nil {
		return fmt.Errorf("list obsolete reconstructed threads: %w", err)
	}
	obsolete := []string{}
	for rows.Next() {
		var threadID string
		if err := rows.Scan(&threadID); err != nil {
			rows.Close()
			return fmt.Errorf("read obsolete reconstructed thread: %w", err)
		}
		if _, ok := active[threadID]; !ok {
			obsolete = append(obsolete, threadID)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close obsolete reconstructed threads: %w", err)
	}
	for _, threadID := range obsolete {
		if canonicalID, ok := retired[threadID]; ok && canonicalID != threadID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE thread_id = ?`, threadID); err != nil {
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

func loadThreadNodes(ctx context.Context, tx *sql.Tx, accountID string) (map[string]*threadNode, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT message_id, account_id, rfc_message_id, in_reply_to, subject, snippet,
       from_email, reply_to_email, sort_at, attachment_count, created_at, updated_at
FROM messages
WHERE account_id = ?
ORDER BY COALESCE(sort_at, created_at), message_id`, accountID)
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
			&sortAt, &node.AttachmentCount, &createdAt, &updatedAt,
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
JOIN messages AS m ON m.message_id = r.message_id
WHERE m.account_id = ?
ORDER BY r.message_id, r.ordinal`, accountID)
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

	recipientRows, err := tx.QueryContext(ctx, `
SELECT r.message_id, r.email_address
FROM message_recipients AS r
JOIN messages AS m ON m.message_id = r.message_id
WHERE m.account_id = ? AND r.kind IN ('to', 'cc')
ORDER BY r.message_id, r.kind, r.ordinal`, accountID)
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

	locationRows, err := tx.QueryContext(ctx, `
SELECT l.message_id, l.folder_id,
       MAX(CASE WHEN f.kind <> 'sent' AND l.seen = 0 THEN 1 ELSE 0 END)
FROM message_locations AS l
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
	return nodes, nil
}

func loadExistingThreadMembership(ctx context.Context, tx *sql.Tx, accountID string) (map[string]string, map[string]time.Time, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT tm.message_id, t.thread_id, t.created_at
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ?`, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("load existing thread memberships: %w", err)
	}
	membership := map[string]string{}
	created := map[string]time.Time{}
	for rows.Next() {
		var messageID, threadID, createdAt string
		if err := rows.Scan(&messageID, &threadID, &createdAt); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("read existing thread membership: %w", err)
		}
		membership[messageID] = threadID
		if value, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			created[threadID] = value
		}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, fmt.Errorf("close existing thread memberships: %w", err)
	}
	return membership, created, nil
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

func aggregateThreadComponents(nodes map[string]*threadNode, components map[string][]string, existing map[string]string, created map[string]time.Time) ([]threadAggregate, map[string]string) {
	aggregates := make([]threadAggregate, 0, len(components))
	retired := map[string]string{}
	for _, messageIDs := range components {
		existingIDs := map[string]struct{}{}
		for _, messageID := range messageIDs {
			if threadID := existing[messageID]; threadID != "" {
				existingIDs[threadID] = struct{}{}
			}
		}
		threadID := ""
		for candidate := range existingIDs {
			if threadID == "" || threadIdentityLess(candidate, threadID, created) {
				threadID = candidate
			}
		}
		if threadID == "" {
			threadID = provisionalThreadID(messageIDs[0])
		}
		for candidate := range existingIDs {
			if candidate != threadID {
				retired[candidate] = threadID
			}
		}
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
	return aggregates, retired
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

func (repository *ThreadRepository) Show(ctx context.Context, accountID, selector string, limit int) (maildomain.ThreadPage, error) {
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
	rows, err := repository.db.QueryContext(ctx, messageColumns+`
JOIN thread_messages AS tm ON tm.message_id = m.message_id
WHERE m.account_id = ? AND tm.thread_id = ?
ORDER BY COALESCE(m.sort_at, m.created_at), m.message_id
LIMIT ?`, strings.TrimSpace(accountID), threadID, limit+1)
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
	}
	thread.Messages = messages
	edges, err := repository.edges(ctx, accountID, messageIDs(messages))
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	thread.Edges = edges
	return maildomain.ThreadPage{Thread: thread, More: more, Edges: edges}, nil
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

const threadColumns = `
SELECT thread_id, account_id, root_rfc_message_id, canonical_subject, snippet,
       participants_json, folder_ids_json, message_count, unread_count,
       attachment_count, first_message_at, last_message_at, status,
       reconstruction, created_at, updated_at
FROM threads`

func scanThread(scanner interface{ Scan(...any) error }) (maildomain.Thread, error) {
	var value maildomain.Thread
	var participantsJSON, foldersJSON string
	var firstMessageAt, lastMessageAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&value.ID, &value.AccountID, &value.RootRFCMessageID, &value.CanonicalSubject,
		&value.Snippet, &participantsJSON, &foldersJSON, &value.MessageCount,
		&value.UnreadCount, &value.AttachmentCount, &firstMessageAt, &lastMessageAt,
		&value.Status, &value.Reconstruction, &createdAt, &updatedAt,
	); err != nil {
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
