package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDeleteClosesEveryClientAndDiscardsUnsavedEdits(t *testing.T) {
	h, store := newTestHub(t)
	h.debounce, h.maxWait = time.Hour, time.Hour
	id := newEntry(t, store, "saved")

	a, room, _ := join(t, h, id)
	b, _, _ := join(t, h, id)
	room.update(a.Client, "ca", 0, insert(5, " and unsaved"))

	// The entry's row is deliberately left in place: if the room saved on its
	// way out, the edit would show up in it. (The API removes the row first;
	// see TestDeleteEndpointSendsViewersAway.)
	h.Delete(id)

	for name, tc := range map[string]*testClient{"a": a, "b": b} {
		if k := tc.expectKick(t); k.code != closeDeleted {
			t.Errorf("%s: kicked with %+v", name, k)
		}
	}
	waitForRoomsClosed(t, h)
	content, err := store.GetContent(t.Context(), id)
	if err != nil || content != "saved" {
		t.Fatalf("content %q, err %v: the room saved after the delete", content, err)
	}
}

func TestJoinAfterDeleteFindsNoEntry(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	a, _, _ := join(t, h, id)

	if err := store.DeleteEntry(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	h.Delete(id)
	a.expectKick(t)
	waitForRoomsClosed(t, h)

	if _, err := h.Join(t.Context(), id, newTestClient(4).Client); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestDeleteEndsAnEmptyLingeringRoom(t *testing.T) {
	h, store := newTestHub(t)
	h.linger = time.Hour
	id := newEntry(t, store, "")
	a, room, _ := join(t, h, id)
	room.leave(a.Client)

	h.Delete(id)
	waitForRoomsClosed(t, h)
	h.Delete(id) // and deleting an entry nobody has open is fine
}

// The whole path: a viewer connected over a real websocket, the entry deleted
// through the REST API.
func TestDeleteEndpointSendsViewersAway(t *testing.T) {
	h, store := newTestHub(t)
	mux := http.NewServeMux()
	(&API{store: store, hub: h}).Register(mux)
	mux.HandleFunc("GET /ws/entries/{id}", h.ServeWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { waitForRoomsClosed(t, h) })

	id := newEntry(t, store, "")
	conn := mustDial(t, srv, id)
	readServerMsg(t, conn)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/entries/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != closeDeleted {
		t.Fatalf("close status %d (%v), want %d", got, err, closeDeleted)
	}

	// A late arrival is told the entry does not exist, not that it was deleted.
	late, _, err := dial(t, srv, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = late.Read(ctx)
	if got := websocket.CloseStatus(err); got != closeNotFound {
		t.Fatalf("close status %d (%v), want %d", got, err, closeNotFound)
	}
}

// Edits made since the last save must survive a restart: the debounce is far
// too long for the timer to have saved anything.
func TestShutdownSavesEditsThatSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub(store)
	h.debounce, h.maxWait = time.Hour, time.Hour
	id := newEntry(t, store, "before")

	a, room, _ := join(t, h, id)
	room.update(a.Client, "ca", 0, insert(6, " and after"))
	a.recv(t) // the broadcast: the room has applied it

	// A second room that nobody is in any more, which must be saved and closed too.
	idle := newEntry(t, store, "idle")
	h.linger = time.Hour
	b, idleRoom, _ := join(t, h, idle)
	idleRoom.update(b.Client, "cb", 0, insert(4, "!"))
	idleRoom.leave(b.Client)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if k := a.expectKick(t); k.code != websocket.StatusGoingAway {
		t.Fatalf("kicked with %+v", k)
	}
	// Once Shutdown returns the store can be closed without losing a save.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store2.Close() })
	h2 := NewHub(store2)
	h2.linger = 0
	for id, want := range map[string]string{id: "before and after", idle: "idle!"} {
		_, _, snap := join(t, h2, id)
		if snap.Doc != want {
			t.Errorf("after restart %q, want %q", snap.Doc, want)
		}
	}
}

func TestNoJoinsAfterShutdown(t *testing.T) {
	h, store := newTestHub(t)
	id := newEntry(t, store, "")
	if err := h.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Join(t.Context(), id, newTestClient(4).Client); !errors.Is(err, errShuttingDown) {
		t.Fatalf("got %v, want errShuttingDown", err)
	}
}

func TestWebSocketJoinDuringShutdownIsToldToComeBack(t *testing.T) {
	var hub *Hub
	srv, store := newWSServerWith(t, func(h *Hub) { hub = h })
	id := newEntry(t, store, "")
	if err := hub.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	conn := mustDial(t, srv, id)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Fatalf("close status %d (%v), want %d", got, err, websocket.StatusGoingAway)
	}
}

// The origin the deployment is configured with (server/fly.toml) is accepted,
// parsed the way main does it; others are not.
func TestWebSocketOriginCheckWithTheDeployedHost(t *testing.T) {
	srv, store := newWSServerWith(t, func(h *Hub) {
		h.originPatterns = parseOrigins("mrkdwn-editor.netlify.app")
	})
	id := newEntry(t, store, "")

	for origin, wantOK := range map[string]bool{
		"https://mrkdwn-editor.netlify.app":                   true,
		"https://deploy-preview-1--mrkdwn-editor.netlify.app": false,
		"https://netlify.app":                                 false,
	} {
		_, res, err := dial(t, srv, id, &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": {origin}},
		})
		if wantOK && err != nil {
			t.Errorf("%s: rejected: %v", origin, err)
		}
		if !wantOK && (err == nil || res == nil || res.StatusCode != http.StatusForbidden) {
			t.Errorf("%s: expected 403, got res=%v err=%v", origin, res, err)
		}
	}
}
