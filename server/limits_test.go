package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestUpdateThatLandsExactlyAtTheCapIsAccepted(t *testing.T) {
	h, store := newTestHub(t)
	h.maxDocUnits = 6
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)

	// Two emoji are four UTF-16 units, so this is 6: units, not characters or bytes.
	room.update(a.Client, "ca", 0, insert(0, "😀😀ab"))
	if m := a.recv(t); m.Type != "updates" {
		t.Fatalf("got %+v", m)
	}
	a.expectNothing(t)
}

func TestUpdateThatWouldExceedTheCapIsRefusedAndTheDocumentIsUntouched(t *testing.T) {
	h, store := newTestHub(t)
	h.maxDocUnits = 6
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)
	b, _, _ := join(t, h, id)

	room.update(a.Client, "ca", 0, insert(0, "😀😀ab"))
	a.recv(t)
	b.recv(t)

	// One unit too many: refused, its author is told, and nobody else hears of it.
	room.update(a.Client, "ca", 1, insert(6, "c"))
	if k := a.expectKick(t); k.code != closeTooLarge {
		t.Fatalf("kicked with %+v", k)
	}
	b.expectNothing(t)

	// The room carries on with the document as it was, for others and for newcomers.
	room.update(b.Client, "cb", 1, []Step{{Changes: []Change{{From: 4, To: 5, Insert: "z"}}}})
	if m := b.recv(t); m.Type != "updates" || m.ClientID != "cb" {
		t.Fatalf("got %+v", m)
	}
	_, _, snap := join(t, h, id)
	if snap.Doc != "😀😀zb" || snap.Version != 2 {
		t.Fatalf("got doc %q at version %d", snap.Doc, snap.Version)
	}
}

// Applying an edit can overwrite the document's storage, so a refused update
// must be refused before anything is applied. Replacing text while growing is
// what overwrites it; the entry's non-ASCII text leaves spare capacity for
// that to happen in place.
func TestRefusedGrowingReplaceLeavesTheDocumentIntact(t *testing.T) {
	h, store := newTestHub(t)
	h.maxDocUnits = 12
	const original = "éééééééééé"
	id := newEntry(t, store, original)
	a, room, _ := join(t, h, id)
	join(t, h, id) // keeps the room alive after a is kicked

	room.update(a.Client, "ca", 0, []Step{{Changes: []Change{{From: 0, To: 1, Insert: "ABCDEFGH"}}}})
	if k := a.expectKick(t); k.code != closeTooLarge {
		t.Fatalf("kicked with %+v", k)
	}

	_, _, snap := join(t, h, id)
	if snap.Doc != original || snap.Version != 0 {
		t.Fatalf("got doc %q at version %d", snap.Doc, snap.Version)
	}
}

// An entry saved before the cap existed (or under a larger one) still opens.
// It cannot grow, but it can shrink, and once it is under the cap it is an
// ordinary entry again.
func TestEntryAlreadyOverTheCapOpensAndCanShrink(t *testing.T) {
	h, store := newTestHub(t)
	h.maxDocUnits = 4
	id := newEntry(t, store, "0123456789")
	a, room, snap := join(t, h, id)
	if snap.Doc != "0123456789" {
		t.Fatalf("got %+v", snap)
	}

	// Same size is not growth.
	room.update(a.Client, "ca", 0, []Step{{Changes: []Change{{From: 0, To: 1, Insert: "x"}}}})
	if m := a.recv(t); m.Type != "updates" {
		t.Fatalf("got %+v", m)
	}

	room.update(a.Client, "ca", 1, []Step{{Changes: []Change{{From: 0, To: 8, Insert: ""}}}})
	if m := a.recv(t); m.Type != "updates" {
		t.Fatalf("got %+v", m)
	}
	// Two units left; growing by two lands exactly on the cap.
	room.update(a.Client, "ca", 2, insert(2, "ab"))
	if m := a.recv(t); m.Type != "updates" {
		t.Fatalf("got %+v", m)
	}

	room.update(a.Client, "ca", 3, insert(4, "!"))
	if k := a.expectKick(t); k.code != closeTooLarge {
		t.Fatalf("kicked with %+v", k)
	}
}

// The read limit has to admit every update the cap does, however much JSON
// escaping inflates it: a control character costs six bytes (\u0001) per unit.
func TestWebSocketAcceptsAnUpdateAtTheCapInTheWorstCaseEncoding(t *testing.T) {
	srv, store := newWSServer(t)
	conn := mustDial(t, srv, newEntry(t, store, ""))
	readServerMsg(t, conn)

	text := strings.Repeat("\x01", defaultMaxDocUnits)
	writeUpdate(t, conn, "c", 0, insert(0, text))
	m := readServerMsg(t, conn)
	if m.Type != "updates" || len(m.Updates[0].Changes[0].Insert) != defaultMaxDocUnits {
		t.Fatalf("got type %q", m.Type)
	}
}

func TestWebSocketUpdateOverTheCapClosesWithTheTooLargeCode(t *testing.T) {
	srv, store := newWSServerWith(t, func(h *Hub) { h.maxDocUnits = 10 })
	conn := mustDial(t, srv, newEntry(t, store, ""))
	readServerMsg(t, conn)

	writeUpdate(t, conn, "c", 0, insert(0, strings.Repeat("x", 11)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != closeTooLarge {
		t.Fatalf("close status %d (%v), want %d", got, err, closeTooLarge)
	}
}
