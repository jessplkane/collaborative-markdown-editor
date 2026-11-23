package main

import "sort"

// changeOverhead is what a change costs in the history's size estimate on top
// of its inserted text, so that a long run of tiny edits still adds up.
const changeOverhead = 24

// batch is one accepted client update, kept whole so it can be replayed to a
// reconnecting client exactly as it was broadcast: the client recognises its
// own batches by clientID.
type batch struct {
	base     int // document version the batch was applied on top of
	clientID string
	steps    []Step
}

func (b batch) size() int {
	n := 0
	for _, s := range b.steps {
		for _, c := range s.Changes {
			n += len(c.Insert) + changeOverhead
		}
	}
	return n
}

// history is the most recent accepted batches, bounded by estimated size. A
// client that was away longer than the history reaches cannot be replayed to
// and gets a fresh snapshot instead.
type history struct {
	batches []batch
	bytes   int
	limit   int
}

// add records b, which must start at the version after the previous batch. A
// batch larger than the limit empties the history, itself included: nothing
// older than it can be replayed without it.
func (h *history) add(b batch) {
	h.batches = append(h.batches, b)
	h.bytes += b.size()
	drop := 0
	for h.bytes > h.limit && drop < len(h.batches) {
		h.bytes -= h.batches[drop].size()
		h.batches[drop] = batch{} // let the steps be collected
		drop++
	}
	h.batches = h.batches[drop:]
}

// since returns the batches that take a client at document version `version`
// to `current`, the room's version. It reports false if they are not all
// available: the version is ahead of the room, older than the history, or in
// the middle of a batch. A client's version always sits between batches, so
// the last case means it is not talking about this room's history.
func (h *history) since(version, current int) ([]batch, bool) {
	if version == current {
		return nil, true
	}
	if version > current {
		return nil, false
	}
	i := sort.Search(len(h.batches), func(i int) bool { return h.batches[i].base >= version })
	if i == len(h.batches) || h.batches[i].base != version {
		return nil, false
	}
	return h.batches[i:], true
}
