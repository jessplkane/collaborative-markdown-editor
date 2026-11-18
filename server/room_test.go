package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type serverMsg struct {
	Type     string `json:"type"`
	Doc      string `json:"doc"`
	Version  int    `json:"version"`
	ClientID string `json:"clientID"`
	Updates  []Step `json:"updates"`
}

type testClient struct {
	*Client
	kicks chan string
}

func newTestClient(buffer int) *testClient {
	tc := &testClient{kicks: make(chan string, 8)}
	tc.Client = &Client{
		send: make(chan []byte, buffer),
		kick: func(reason string) { tc.kicks <- reason },
	}
	return tc
}

func (tc *testClient) recv(t *testing.T) serverMsg {
	t.Helper()
	select {
	case data := <-tc.send:
		var m serverMsg
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return serverMsg{}
	}
}

func (tc *testClient) expectNothing(t *testing.T) {
	t.Helper()
	select {
	case data := <-tc.send:
		t.Fatalf("unexpected message: %s", data)
	case <-time.After(50 * time.Millisecond):
	}
}

func (tc *testClient) expectKick(t *testing.T) string {
	t.Helper()
	select {
	case reason := <-tc.kicks:
		return reason
	case <-time.After(2 * time.Second):
		t.Fatal("client was not kicked")
		return ""
	}
}

func newTestHub(t *testing.T) (*Hub, *Store) {
	t.Helper()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewHub(store), store
}

func newEntry(t *testing.T, store *Store, content string) string {
	t.Helper()
	e, err := store.CreateEntry(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := store.SaveContent(t.Context(), e.ID, content, deriveTitle(content)); err != nil {
			t.Fatal(err)
		}
	}
	return e.ID
}

// join adds a new client to the entry's room and consumes its snapshot.
func join(t *testing.T, h *Hub, id string) (*testClient, *Room, serverMsg) {
	t.Helper()
	tc := newTestClient(16)
	room, err := h.Join(t.Context(), id, tc.Client)
	if err != nil {
		t.Fatal(err)
	}
	return tc, room, tc.recv(t)
}

func insert(pos int, text string) []Step {
	return []Step{{Changes: []Change{{From: pos, To: pos, Insert: text}}}}
}

func TestJoinSendsSnapshot(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "hello 😀")

	_, _, snap := join(t, h, id)
	if snap.Type != "snapshot" || snap.Doc != "hello 😀" || snap.Version != 0 {
		t.Fatalf("got %+v", snap)
	}
}

func TestJoinUnknownEntry(t *testing.T) {
	h, _ := newTestHub(t)
	_, err := h.Join(t.Context(), "nope", newTestClient(1).Client)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestAcceptedUpdateIsBroadcastToEveryoneIncludingAuthor(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)
	b, _, _ := join(t, h, id)

	room.update(a.Client, "a", 0, insert(0, "hi"))
	for name, tc := range map[string]*testClient{"author": a, "other": b} {
		m := tc.recv(t)
		if m.Type != "updates" || m.Version != 0 || m.ClientID != "a" ||
			len(m.Updates) != 1 || m.Updates[0].Changes[0].Insert != "hi" {
			t.Fatalf("%s got %+v", name, m)
		}
	}

	// The version advanced: an update based on 1 is accepted.
	room.update(b.Client, "b", 1, insert(2, "!"))
	if m := a.recv(t); m.Type != "updates" || m.Version != 1 || m.ClientID != "b" {
		t.Fatalf("got %+v", m)
	}
}

func TestStaleUpdateIsRejectedAfterTheUpdatesThatMadeItStale(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)
	b, _, _ := join(t, h, id)

	// Both write against version 0; a is sequenced first.
	room.update(a.Client, "a", 0, insert(0, "A"))
	room.update(b.Client, "b", 0, insert(0, "B"))

	// b must see a's update before the rejection: the client relies on this
	// order to rebase before resending.
	if m := b.recv(t); m.Type != "updates" || m.ClientID != "a" {
		t.Fatalf("first message to b: %+v", m)
	}
	if m := b.recv(t); m.Type != "reject" || m.Version != 1 {
		t.Fatalf("second message to b: %+v", m)
	}
	a.recv(t) // a's own confirmation
	a.expectNothing(t)

	// The resend, rebased onto version 1, is accepted.
	room.update(b.Client, "b", 1, insert(1, "B"))
	if m := a.recv(t); m.Type != "updates" || m.Version != 1 || m.ClientID != "b" {
		t.Fatalf("got %+v", m)
	}
}

func TestBatchAdvancesVersionByStepCount(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)

	room.update(a.Client, "a", 0, append(insert(0, "x"), insert(1, "y")...))
	if m := a.recv(t); m.Type != "updates" || m.Version != 0 || len(m.Updates) != 2 {
		t.Fatalf("got %+v", m)
	}

	room.update(a.Client, "a", 1, insert(2, "z")) // 1 is now stale: version is 2
	if m := a.recv(t); m.Type != "reject" || m.Version != 2 {
		t.Fatalf("got %+v", m)
	}
	room.update(a.Client, "a", 2, insert(2, "z"))
	if m := a.recv(t); m.Type != "updates" || m.Version != 2 {
		t.Fatalf("got %+v", m)
	}
}

func TestInvalidUpdateKicksOnlyTheSender(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "abc")
	a, room, _ := join(t, h, id)
	b, _, _ := join(t, h, id)

	room.update(a.Client, "a", 0, insert(99, "x")) // past the end
	a.expectKick(t)
	b.expectNothing(t)

	// The document was not touched, and the room still works for b.
	room.update(b.Client, "b", 0, insert(3, "d"))
	if m := b.recv(t); m.Type != "updates" || m.Version != 0 {
		t.Fatalf("got %+v", m)
	}
	// A kicked client's later messages are ignored.
	room.update(a.Client, "a", 1, insert(0, "zzz"))
	b.expectNothing(t)
}

func TestUpdateFromTheFutureKicksSender(t *testing.T) {
	h, store := newTestHub(t)
	a, room, _ := join(t, h, newEntry(t, store, ""))

	room.update(a.Client, "a", 5, insert(0, "x"))
	a.expectKick(t)
}

func TestSlowClientIsDroppedWithoutStallingTheRoom(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	fast, room, _ := join(t, h, id)

	// A client whose buffer holds only its snapshot and who never reads.
	slow := newTestClient(1)
	if _, err := h.Join(t.Context(), id, slow.Client); err != nil {
		t.Fatal(err)
	}

	room.update(fast.Client, "f", 0, insert(0, "x"))
	if reason := slow.expectKick(t); reason != "too slow" {
		t.Fatalf("reason %q", reason)
	}
	if m := fast.recv(t); m.Type != "updates" {
		t.Fatalf("got %+v", m)
	}
	room.update(fast.Client, "f", 1, insert(1, "y"))
	if m := fast.recv(t); m.Type != "updates" || m.Version != 1 {
		t.Fatalf("got %+v", m)
	}
}

func TestContentAndTitleAreSavedWhenLastClientLeaves(t *testing.T) {
	h, store := newTestHub(t)
	h.debounce, h.maxWait = time.Hour, time.Hour // only the leave can trigger a save
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)

	room.update(a.Client, "a", 0, insert(0, "# My notes 😀\nbody"))
	a.recv(t)
	room.leave(a.Client)
	<-room.done

	got, err := store.GetContent(t.Context(), id)
	if err != nil || got != "# My notes 😀\nbody" {
		t.Fatalf("content %q, %v", got, err)
	}
	e, err := store.GetEntry(t.Context(), id)
	if err != nil || e.Title != "My notes 😀" {
		t.Fatalf("title %q, %v", e.Title, err)
	}

	// A new joiner gets a fresh room loaded from the saved content.
	_, room2, snap := join(t, h, id)
	if room2 == room || snap.Doc != "# My notes 😀\nbody" || snap.Version != 0 {
		t.Fatalf("got room reuse=%v snapshot %+v", room2 == room, snap)
	}
}

func TestEditsAreSavedAfterDebounceWhileClientsStayConnected(t *testing.T) {
	h, store := newTestHub(t)
	h.debounce, h.maxWait = 20*time.Millisecond, time.Hour
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)

	room.update(a.Client, "a", 0, insert(0, "saved"))
	waitForContent(t, store, id, "saved", time.Second)
}

func TestContinuousTypingCannotPostponeSavesForever(t *testing.T) {
	h, store := newTestHub(t)
	h.debounce, h.maxWait = 200*time.Millisecond, 300*time.Millisecond
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)

	// An edit every 20ms never lets the 200ms debounce expire; only maxWait
	// can save. Stop typing once content shows up in the store.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for v := 0; time.Now().Before(deadline); v++ {
		room.update(a.Client, "a", v, insert(v, "x"))
		a.recv(t)
		if got, _ := store.GetContent(t.Context(), id); got != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("nothing saved while typing continuously")
}

func waitForContent(t *testing.T, store *Store, id, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		got, err := store.GetContent(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("content %q, want %q", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Many clients joining and leaving one entry at once exercises the room
// shutdown / re-creation race. Run with -race.
func TestJoinLeaveChurnDoesNotDeadlockOrLeakRooms(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")

	const workers, rounds = 16, 25
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for range rounds {
				tc := newTestClient(64)
				room, err := h.Join(ctx, id, tc.Client)
				if err != nil {
					t.Error(err)
					return
				}
				// The snapshot tells us the version to build on; a rejection is
				// fine, we only care that nothing hangs.
				var m serverMsg
				select {
				case data := <-tc.send:
					_ = json.Unmarshal(data, &m)
				case <-ctx.Done():
					t.Error("timed out")
					return
				}
				room.update(tc.Client, "w"+string(rune('a'+w)), m.Version, insert(0, "x"))
				room.leave(tc.Client)
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("deadline exceeded")
	}

	waitForRoomsClosed(t, h)
}

// waitForRoomsClosed waits for every room to finish its final save and exit.
// Rooms shut down asynchronously after their last client leaves.
func waitForRoomsClosed(t *testing.T, h *Hub) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.rooms)
		h.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%d rooms still open", n)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
