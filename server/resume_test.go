package main

import (
	"strings"
	"testing"
	"time"
)

// rejoin joins as a client resuming from `version` of the room instance
// `epoch`, with a send buffer of the given size.
func rejoin(t *testing.T, h *Hub, id, epoch string, version, buffer int) (*testClient, serverMsg) {
	t.Helper()
	tc := newTestClient(buffer)
	tc.resume = &resumeRequest{epoch, version}
	if _, err := h.Join(t.Context(), id, tc.Client); err != nil {
		t.Fatal(err)
	}
	return tc, tc.recv(t)
}

func insertedText(m serverMsg) string {
	var sb strings.Builder
	for _, s := range m.Updates {
		for _, c := range s.Changes {
			sb.WriteString(c.Insert)
		}
	}
	return sb.String()
}

func TestResumeReplaysMissedBatchesInOrderWithTheirAuthors(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, room, snap := join(t, h, id)
	b, _, _ := join(t, h, id)
	room.update(a.Client, "ca", 0, insert(0, "x"))
	room.leave(b.Client) // b has seen nothing: it is at version 0

	room.update(a.Client, "ca", 1, insert(1, "y"))
	two := []Step{
		{Changes: []Change{{From: 2, To: 2, Insert: "p"}}},
		{Changes: []Change{{From: 3, To: 3, Insert: "q"}}},
	}
	room.update(a.Client, "cc", 2, two)

	got, first := rejoin(t, h, id, snap.Epoch, 0, 16)
	type want struct {
		version  int
		clientID string
		text     string
	}
	wants := []want{{0, "ca", "x"}, {1, "ca", "y"}, {2, "cc", "pq"}}
	for i, w := range wants {
		m := first
		if i > 0 {
			m = got.recv(t)
		}
		if m.Type != "updates" || m.Version != w.version || m.ClientID != w.clientID || insertedText(m) != w.text {
			t.Fatalf("batch %d: got %+v, want %+v", i, m, w)
		}
	}
	if m := got.recv(t); m.Type != "resumed" || m.Version != 4 {
		t.Fatalf("got %+v, want resumed at version 4", m)
	}
}

func TestResumeAtTheCurrentVersionReplaysNothing(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	a, room, snap := join(t, h, id)
	room.update(a.Client, "ca", 0, insert(0, "x"))

	_, m := rejoin(t, h, id, snap.Epoch, 1, 16)
	if m.Type != "resumed" || m.Version != 1 {
		t.Fatalf("got %+v", m)
	}
}

// The server applied a batch but the client never saw the broadcast. The
// client recognises its own clientID in the replay and does not apply it
// twice; here, that the batch is in the replay exactly once and the server
// did not apply anything again.
func TestResumeAfterALostAckReplaysTheClientsOwnBatch(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	_, room, snap := join(t, h, id)
	lost, _, _ := join(t, h, id)

	room.update(lost.Client, "c1", 0, insert(0, "mine"))
	room.leave(lost.Client) // dropped before reading the broadcast

	got, m := rejoin(t, h, id, snap.Epoch, 0, 16)
	if m.Type != "updates" || m.ClientID != "c1" || m.Version != 0 || insertedText(m) != "mine" {
		t.Fatalf("got %+v", m)
	}
	if m := got.recv(t); m.Type != "resumed" || m.Version != 1 {
		t.Fatalf("got %+v, want resumed at version 1", m)
	}
	got.expectNothing(t)
}

func TestResumeFallsBackToASnapshot(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		epoch func(real string) string
		from  int
	}{
		{"epoch of another room instance", 1 << 20, func(string) string { return "other" }, 0},
		{"version ahead of the room", 1 << 20, func(e string) string { return e }, 99},
		{"negative version", 1 << 20, func(e string) string { return e }, -1},
		{"history no longer reaches the version", 1, func(e string) string { return e }, 0},
		// Versions 1 and 2 are the two steps of one batch; a client never
		// stands between them.
		{"version in the middle of a batch", 1 << 20, func(e string) string { return e }, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store := newTestHub(t)
			h.historyLimit = tt.limit
			id := newEntry(t, store, "")
			a, room, snap := join(t, h, id)
			room.update(a.Client, "ca", 0, []Step{
				{Changes: []Change{{From: 0, To: 0, Insert: "a"}}},
				{Changes: []Change{{From: 1, To: 1, Insert: "b"}}},
			})
			room.update(a.Client, "ca", 2, insert(2, "c"))

			_, m := rejoin(t, h, id, tt.epoch(snap.Epoch), tt.from, 16)
			if m.Type != "snapshot" || m.Doc != "abc" || m.Version != 3 || m.Epoch != snap.Epoch {
				t.Fatalf("got %+v", m)
			}
		})
	}
}

func TestResumeFallsBackWhenTheGapDoesNotFitTheClientsBuffer(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	a, room, snap := join(t, h, id)
	for i := range 5 {
		room.update(a.Client, "ca", i, insert(i, "x"))
	}

	// Five batches and the resumed marker need six slots.
	_, m := rejoin(t, h, id, snap.Epoch, 0, 5)
	if m.Type != "snapshot" || m.Doc != "xxxxx" {
		t.Fatalf("got %+v", m)
	}
	_, m = rejoin(t, h, id, snap.Epoch, 0, 6)
	if m.Type != "updates" {
		t.Fatalf("got %+v", m)
	}
}

func TestEachRoomInstanceHasItsOwnEpoch(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	a, room, first := join(t, h, id)
	room.leave(a.Client)
	waitForRoomsClosed(t, h)

	// Same entry, same content, versions starting from 0 again: only the
	// epoch tells a returning client its version number means something else.
	_, _, second := join(t, h, id)
	if first.Epoch == "" || first.Epoch == second.Epoch {
		t.Fatalf("epochs %q and %q", first.Epoch, second.Epoch)
	}
}

func TestReconnectingClientIsListedOnceAndItsOldConnectionClosed(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	other, otherRoom, snap := join(t, h, id)
	stale, staleRoom, _ := join(t, h, id)
	otherRoom.hello(other.Client, "co", "uo", "Grace")
	staleRoom.hello(stale.Client, "c1", "u1", "Ada")
	expectViewers(t, other, "Grace")
	expectViewers(t, other, "Grace", "Ada")
	expectViewers(t, stale, "Grace")
	expectViewers(t, stale, "Grace", "Ada")

	// The server has not noticed that the first connection is gone when the
	// same client turns up on a new one.
	fresh, m := rejoin(t, h, id, snap.Epoch, 0, 16)
	if m.Type != "resumed" {
		t.Fatalf("got %+v", m)
	}
	otherRoom.hello(fresh.Client, "c1", "u1", "Ada")

	if k := stale.expectKick(t); k.code != closeReplaced {
		t.Fatalf("kicked with %+v", k)
	}
	expectViewers(t, other, "Grace", "Ada")
	expectViewers(t, fresh, "Grace", "Ada")
}

func TestEmptyRoomKeepsItsHistoryForAWhile(t *testing.T) {
	h, store := newTestHub(t)
	h.linger = time.Hour
	h.debounce = time.Hour // only the save on leaving can have written this
	h.maxWait = time.Hour
	id := newEntry(t, store, "")

	a, room, snap := join(t, h, id)
	room.update(a.Client, "ca", 0, insert(0, "typed"))
	room.leave(a.Client)

	// Saved when the last client left, not only when the room finally exits.
	deadline := time.Now().Add(2 * time.Second)
	for {
		content, err := store.GetContent(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if content == "typed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("content %q was not saved", content)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A lone client that dropped can resume into the room it left.
	_, m := rejoin(t, h, id, snap.Epoch, 0, 16)
	if m.Type != "updates" || m.ClientID != "ca" {
		t.Fatalf("got %+v", m)
	}
}

func TestEmptyRoomExitsAfterTheLinger(t *testing.T) {
	h, store := newTestHub(t)
	h.linger = 30 * time.Millisecond
	id := newEntry(t, store, "")

	a, room, _ := join(t, h, id)
	room.leave(a.Client)
	waitForRoomsClosed(t, h)

	// A returning client finds a new room, hence a new epoch.
	_, _, snap := join(t, h, id)
	if snap.Type != "snapshot" {
		t.Fatalf("got %+v", snap)
	}
}

func TestEmptyRoomIsRevivedByAJoinDuringTheLinger(t *testing.T) {
	h, store := newTestHub(t)
	h.linger = 200 * time.Millisecond
	id := newEntry(t, store, "")

	a, room, snap := join(t, h, id)
	room.leave(a.Client)
	time.Sleep(50 * time.Millisecond)
	b, _ := rejoin(t, h, id, snap.Epoch, 0, 16)

	// The linger that was running when b joined must not close the room under it.
	time.Sleep(250 * time.Millisecond)
	room.update(b.Client, "cb", 0, insert(0, "still here"))
	if m := b.recv(t); m.Type != "updates" || insertedText(m) != "still here" {
		t.Fatalf("got %+v", m)
	}
}

func TestWebSocketReconnectResumes(t *testing.T) {
	srv, store := newWSServer(t)
	id := newEntry(t, store, "")

	holder := mustDial(t, srv, id) // keeps the room alive meanwhile
	snap := readServerMsg(t, holder)
	conn := mustDial(t, srv, id)
	readServerMsg(t, conn)

	writeUpdate(t, holder, "ch", 0, insert(0, "hello"))
	readServerMsg(t, holder)
	conn.CloseNow()

	writeUpdate(t, holder, "ch", 1, insert(1, "!"))
	readServerMsg(t, holder)

	back := mustDial(t, srv, id+"?epoch="+snap.Epoch+"&version=0")
	for i, want := range []string{"hello", "!"} {
		m := readServerMsg(t, back)
		if m.Type != "updates" || m.Version != i || insertedText(m) != want {
			t.Fatalf("batch %d: got %+v", i, m)
		}
	}
	if m := readServerMsg(t, back); m.Type != "resumed" || m.Version != 2 {
		t.Fatalf("got %+v", m)
	}

	// A resumed connection is a full participant.
	writeUpdate(t, back, "cb", 2, insert(2, "?"))
	if m := readServerMsg(t, holder); m.Type != "updates" || m.ClientID != "cb" {
		t.Fatalf("got %+v", m)
	}
}

func TestWebSocketResumeWithAnUnusableQueryGetsASnapshot(t *testing.T) {
	srv, store := newWSServer(t)
	id := newEntry(t, store, "text")

	holder := mustDial(t, srv, id)
	snap := readServerMsg(t, holder)
	for _, query := range []string{
		"?epoch=" + snap.Epoch + "&version=abc",
		"?epoch=" + snap.Epoch,
		"?version=0",
	} {
		conn := mustDial(t, srv, id+query)
		if m := readServerMsg(t, conn); m.Type != "snapshot" || m.Doc != "text" {
			t.Fatalf("%s: got %+v", query, m)
		}
	}
}
