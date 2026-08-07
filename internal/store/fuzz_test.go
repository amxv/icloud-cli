package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func FuzzThreadComponentsKeepEveryMessageExactlyOnce(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Add([]byte("references-and-cycles"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4<<10 {
			t.Skip()
		}
		count := len(data)%16 + 1
		nodes := make(map[string]*threadNode, count)
		ids := make([]string, count)
		for index := 0; index < count; index++ {
			ids[index] = fmt.Sprintf("msg_%02d", index)
		}
		for index, messageID := range ids {
			selector := byte(0)
			if len(data) > 0 {
				selector = data[index%len(data)]
			}
			node := &threadNode{
				ID:           messageID,
				AccountID:    "acct_fuzz",
				RFCMessageID: fmt.Sprintf("node-%02d@example.test", index),
				Subject:      "Re: fuzz thread",
				FromEmail:    fmt.Sprintf("sender-%d@example.test", index%3),
				SortAt:       time.Unix(int64(index), 0).UTC(),
			}
			switch selector % 6 {
			case 0:
				// Isolated RFC node.
			case 1:
				node.InReplyTo = fmt.Sprintf("node-%02d@example.test", (index+count-1)%count)
			case 2:
				node.References = []string{fmt.Sprintf("node-%02d@example.test", (index+1)%count)}
			case 3:
				node.References = []string{"<malformed value>", fmt.Sprintf("node-%02d@example.test", (index+1)%count)}
			case 4:
				node.InReplyTo = node.RFCMessageID // self-cycle evidence must remain non-destructive.
			case 5:
				node.References = []string{"missing-parent@example.test"}
			}
			nodes[messageID] = node
		}

		firstComponents, firstEdges := buildThreadComponents(nodes)
		secondComponents, secondEdges := buildThreadComponents(nodes)
		if !reflect.DeepEqual(firstComponents, secondComponents) || !reflect.DeepEqual(firstEdges, secondEdges) {
			t.Fatal("thread reconstruction is not deterministic for identical evidence")
		}

		seen := make(map[string]int, len(nodes))
		for _, component := range firstComponents {
			for _, messageID := range component {
				if _, ok := nodes[messageID]; !ok {
					t.Fatalf("thread component contains unknown message %q", messageID)
				}
				seen[messageID]++
			}
		}
		for messageID := range nodes {
			if seen[messageID] != 1 {
				t.Fatalf("message %q appears %d times across thread components", messageID, seen[messageID])
			}
		}
		for _, edge := range firstEdges {
			if _, ok := nodes[edge.ChildMessageID]; !ok {
				t.Fatalf("thread edge has unknown child %q", edge.ChildMessageID)
			}
			if edge.ParentMessageID != "" {
				if _, ok := nodes[edge.ParentMessageID]; !ok {
					t.Fatalf("resolved thread edge has unknown parent %q", edge.ParentMessageID)
				}
			}
		}
	})
}
