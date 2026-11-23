package main

import (
	"strings"
	"testing"
)

// A batch costs memory even when its steps carry no text, and so does the
// clientID it is stored with; neither may let the history grow without bound.
func TestHistoryIsBoundedForBatchesThatCarryNoText(t *testing.T) {
	tests := []struct {
		name  string
		batch func(base int) batch
	}{
		{"empty steps", func(base int) batch {
			return batch{base, "c", []Step{{}}}
		}},
		{"long client id", func(base int) batch {
			return batch{base, strings.Repeat("x", maxIDBytes), []Step{{}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := history{limit: 4096}
			for i := range 10_000 {
				h.add(tt.batch(i))
			}
			if h.bytes > h.limit {
				t.Fatalf("history holds %d bytes, over its limit of %d", h.bytes, h.limit)
			}
			if len(h.batches) == 0 || len(h.batches) > 1000 {
				t.Fatalf("kept %d batches", len(h.batches))
			}
			// The newest batch is still replayable.
			if _, ok := h.since(9_999, 10_000); !ok {
				t.Fatal("newest batch was dropped")
			}
		})
	}
}
