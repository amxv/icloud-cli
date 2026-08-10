package store

import (
	"context"
	"strings"
	"testing"
)

func TestAgentParityPhase8OutboundStateReadsUseDedicatedIndexes(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	for _, test := range []struct {
		name  string
		query string
		args  []any
		index string
	}{
		{
			name: "forward",
			query: `
EXPLAIN QUERY PLAN
SELECT EXISTS (
    SELECT 1 FROM outbound_attempts
    WHERE account_id = ? AND source_message_id = ? AND status = 'sent'
)`,
			args:  []any{accountID, "msg_phase8_source"},
			index: "outbound_attempts_sent_forward_source_idx",
		},
		{
			name: "reply",
			query: `
EXPLAIN QUERY PLAN
SELECT EXISTS (
    SELECT 1 FROM outbound_attempts
    WHERE account_id = ? AND parent_message_id = ? AND status = 'sent'
      AND outbound_kind IN ('reply', 'reply_all')
      AND answer_reconciliation = 'pending'
)`,
			args:  []any{accountID, "msg_phase8_parent"},
			index: "outbound_attempts_sent_reply_parent_idx",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := state.db.QueryContext(context.Background(), test.query, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var id, parent, notUsed int
				var detail string
				if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				plan.WriteString(detail)
				plan.WriteByte('\n')
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.String(), test.index) {
				t.Fatalf("outbound-state query did not use %s:\n%s", test.index, plan.String())
			}
		})
	}
}
