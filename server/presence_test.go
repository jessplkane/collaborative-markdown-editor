package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func viewerNames(m serverMsg) []string {
	names := make([]string, len(m.Viewers))
	for i, v := range m.Viewers {
		names[i] = v.Name
	}
	return names
}

func expectViewers(t *testing.T, tc *testClient, want ...string) {
	t.Helper()
	m := tc.recv(t)
	if got := viewerNames(m); m.Type != "viewers" || !slices.Equal(got, want) {
		t.Fatalf("got %s %v, want viewers %v", m.Type, got, want)
	}
}

func TestViewersListedInJoinOrderOnceTheySayHello(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, roomA, _ := join(t, h, id)
	b, roomB, _ := join(t, h, id)

	// Connected but anonymous: not listed, nothing sent.
	a.expectNothing(t)

	// b says hello first, but a joined first and so is listed first.
	roomB.hello(b.Client, "cb", "ub", "Grace")
	expectViewers(t, a, "Grace")
	expectViewers(t, b, "Grace")
	roomA.hello(a.Client, "ca", "ua", "Ada")
	expectViewers(t, a, "Ada", "Grace")
	expectViewers(t, b, "Ada", "Grace")

	// A new name replaces the old one.
	roomA.hello(a.Client, "ca", "ua", "Ada L.")
	expectViewers(t, a, "Ada L.", "Grace")
	expectViewers(t, b, "Ada L.", "Grace")
}

func TestViewerLeavesTheListWhenTheyLeave(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, roomA, _ := join(t, h, id)
	b, roomB, _ := join(t, h, id)
	roomA.hello(a.Client, "ca", "ua", "Ada")
	roomB.hello(b.Client, "cb", "ub", "Grace")
	expectViewers(t, a, "Ada")
	expectViewers(t, a, "Ada", "Grace")

	roomB.leave(b.Client)
	expectViewers(t, a, "Ada")
}

func TestAnonymousClientLeavingSendsNoViewerList(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, _, _ := join(t, h, id)
	b, roomB, _ := join(t, h, id)
	roomB.leave(b.Client)
	a.expectNothing(t)
}

func TestDroppedSlowViewerLeavesTheList(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, roomA, _ := join(t, h, id)
	slow := newTestClient(2)
	roomSlow, err := h.Join(t.Context(), id, slow.Client)
	if err != nil {
		t.Fatal(err)
	}
	slow.recv(t) // snapshot

	roomA.hello(a.Client, "ca", "ua", "Ada")
	roomSlow.hello(slow.Client, "cs", "us", "Slow")
	expectViewers(t, a, "Ada")
	expectViewers(t, a, "Ada", "Slow")

	// slow never reads, so its two-message buffer is full by now. The next
	// broadcast drops it, and the others are told it is gone.
	roomA.update(a.Client, "ca", 0, insert(0, "x"))
	if m := a.recv(t); m.Type != "updates" {
		t.Fatalf("got %s", m.Type)
	}
	expectViewers(t, a, "Ada")
	slow.expectKick(t)
}

func TestCursorIsRelayedToOthersOnly(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "hello")

	a, roomA, _ := join(t, h, id)
	b, roomB, _ := join(t, h, id)
	roomA.hello(a.Client, "ca", "ua", "Ada")
	roomB.hello(b.Client, "cb", "ub", "Grace")
	expectViewers(t, a, "Ada")
	expectViewers(t, a, "Ada", "Grace")
	expectViewers(t, b, "Ada")
	expectViewers(t, b, "Ada", "Grace")

	roomA.cursor(a.Client, 0, 3)
	m := b.recv(t)
	if m.Type != "cursor" || m.ClientID != "ca" || m.Version != 0 || m.Pos != 3 {
		t.Fatalf("got %+v", m)
	}
	a.expectNothing(t)
}

// A cursor message is queued behind every update the room had broadcast
// before it, which is what lets a receiver rely on having seen the version
// the cursor refers to.
func TestCursorArrivesAfterTheUpdatesItFollows(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, roomA, _ := join(t, h, id)
	b, roomB, _ := join(t, h, id)
	roomA.hello(a.Client, "ca", "ua", "Ada")
	roomB.hello(b.Client, "cb", "ub", "Grace")
	expectViewers(t, b, "Ada")
	expectViewers(t, b, "Ada", "Grace")

	roomA.update(a.Client, "ca", 0, insert(0, "hi"))
	roomA.cursor(a.Client, 1, 2)
	if m := b.recv(t); m.Type != "updates" {
		t.Fatalf("got %s first", m.Type)
	}
	if m := b.recv(t); m.Type != "cursor" || m.Version != 1 {
		t.Fatalf("got %+v", m)
	}
}

func TestMisbehavingCursorsAreKicked(t *testing.T) {
	t.Run("before hello", func(t *testing.T) {
		h, store := newTestHub(t)
		a, roomA, _ := join(t, h, newEntry(t, store, ""))
		roomA.cursor(a.Client, 0, 0)
		a.expectKick(t)
	})
	t.Run("version the room has not reached", func(t *testing.T) {
		h, store := newTestHub(t)
		a, roomA, _ := join(t, h, newEntry(t, store, ""))
		roomA.hello(a.Client, "ca", "ua", "Ada")
		expectViewers(t, a, "Ada")
		roomA.cursor(a.Client, 5, 0)
		a.expectKick(t)
	})
}

func TestWebSocketRejectsMalformedPresenceMessages(t *testing.T) {
	long := string(make([]byte, maxIDBytes+1))
	for name, msg := range map[string]clientMessage{
		"hello without a user":          {Type: "hello", ClientID: "c"},
		"hello without a client":        {Type: "hello", UserID: "u"},
		"hello with a huge id":          {Type: "hello", ClientID: long, UserID: "u"},
		"hello with a long name":        {Type: "hello", ClientID: "c", UserID: "u", Name: string(make([]rune, maxNameRunes+1))},
		"cursor without a position":     {Type: "cursor"},
		"cursor at a negative position": {Type: "cursor", Pos: new(-1)},
	} {
		t.Run(name, func(t *testing.T) {
			srv, store := newWSServer(t)
			conn := mustDial(t, srv, newEntry(t, store, ""))
			readServerMsg(t, conn)

			sendJSON(t, conn, msg)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusInvalidFramePayloadData {
				t.Fatalf("got %v, want close with invalid payload", err)
			}
		})
	}
}

// A viewer whose connection stops answering pings is removed, so a crashed
// browser or a dead network does not linger in the list.
func TestUnresponsiveViewerLeavesTheList(t *testing.T) {
	srv, store := newWSServerWith(t, func(h *Hub) {
		h.pingInterval = 20 * time.Millisecond
		h.pingTimeout = 100 * time.Millisecond
	})
	id := newEntry(t, store, "")

	live := mustDial(t, srv, id)
	stuck := mustDial(t, srv, id)
	readServerMsg(t, live)
	sendJSON(t, live, clientMessage{Type: "hello", ClientID: "live", UserID: "u1", Name: "Live"})
	sendJSON(t, stuck, clientMessage{Type: "hello", ClientID: "stuck", UserID: "u2", Name: "Stuck"})

	// stuck never reads, and a websocket only answers pings while it is being
	// read, so the server's pings go unanswered. live keeps reading and
	// answers its own.
	// Both are listed at first; then stuck goes.
	var sawBoth bool
	for {
		names := viewerNames(readServerMsg(t, live))
		switch {
		case slices.Equal(names, []string{"Live", "Stuck"}):
			sawBoth = true
		case sawBoth && slices.Equal(names, []string{"Live"}):
			return
		}
	}
}
