package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func newWSServer(t *testing.T, patterns ...string) (*httptest.Server, *Store) {
	t.Helper()
	return newWSServerWith(t, func(h *Hub) { h.originPatterns = patterns })
}

// newWSServerWith is newWSServer with a hook to adjust the hub before it
// serves anything.
func newWSServerWith(t *testing.T, configure func(*Hub)) (*httptest.Server, *Store) {
	t.Helper()
	h, store := newTestHub(t)
	configure(h)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/entries/{id}", h.ServeWS)
	srv := httptest.NewServer(mux)
	// Runs after the test's connections are closed and before the store is,
	// so rooms finish their final save against an open database.
	t.Cleanup(func() { waitForRoomsClosed(t, h) })
	t.Cleanup(srv.Close)
	return srv, store
}

func sendJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func dial(t *testing.T, srv *httptest.Server, id string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/entries/"+id, opts)
	if err == nil {
		conn.SetReadLimit(maxMessageBytes)
		t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, res, err
}

func mustDial(t *testing.T, srv *httptest.Server, id string) *websocket.Conn {
	t.Helper()
	conn, _, err := dial(t, srv, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func readServerMsg(t *testing.T, conn *websocket.Conn) serverMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m serverMsg
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeUpdate(t *testing.T, conn *websocket.Conn, clientID string, version int, steps []Step) {
	t.Helper()
	data, err := json.Marshal(clientMessage{Type: "update", ClientID: clientID, Version: version, Updates: steps})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func TestWebSocketEditsSurviveReconnect(t *testing.T) {
	srv, store := newWSServer(t)
	id := newEntry(t, store, "")

	conn := mustDial(t, srv, id)
	if m := readServerMsg(t, conn); m.Type != "snapshot" || m.Doc != "" || m.Version != 0 {
		t.Fatalf("got %+v", m)
	}
	writeUpdate(t, conn, "c1", 0, insert(0, "hello 😀"))
	if m := readServerMsg(t, conn); m.Type != "updates" || m.ClientID != "c1" || m.Version != 0 {
		t.Fatalf("got %+v", m)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// Whether the room outlives the disconnect or has already saved and been
	// replaced, the next connection sees the edit.
	conn2 := mustDial(t, srv, id)
	if m := readServerMsg(t, conn2); m.Type != "snapshot" || m.Doc != "hello 😀" {
		t.Fatalf("got %+v", m)
	}
}

func TestWebSocketClientsSeeEachOthersEdits(t *testing.T) {
	srv, store := newWSServer(t)
	id := newEntry(t, store, "")

	a, b := mustDial(t, srv, id), mustDial(t, srv, id)
	readServerMsg(t, a)
	readServerMsg(t, b)

	writeUpdate(t, a, "a", 0, insert(0, "from a"))
	for _, conn := range []*websocket.Conn{a, b} {
		m := readServerMsg(t, conn)
		if m.Type != "updates" || m.ClientID != "a" || m.Updates[0].Changes[0].Insert != "from a" {
			t.Fatalf("got %+v", m)
		}
	}
}

func TestWebSocketAcceptsLargePaste(t *testing.T) {
	srv, store := newWSServer(t)
	conn := mustDial(t, srv, newEntry(t, store, ""))
	readServerMsg(t, conn)

	// Well over the websocket library's default 32 KiB read limit.
	big := strings.Repeat("x", 300_000)
	writeUpdate(t, conn, "c", 0, insert(0, big))
	if m := readServerMsg(t, conn); m.Type != "updates" || len(m.Updates[0].Changes[0].Insert) != len(big) {
		t.Fatalf("got type %q", m.Type)
	}
}

func TestWebSocketMalformedMessageClosesConnection(t *testing.T) {
	srv, store := newWSServer(t)
	for name, payload := range map[string]string{
		"not json":      `nope`,
		"wrong type":    `{"type":"hello"}`,
		"no client id":  `{"type":"update","version":0,"updates":[{"changes":[]}]}`,
		"empty updates": `{"type":"update","clientID":"c","version":0,"updates":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			conn := mustDial(t, srv, newEntry(t, store, ""))
			readServerMsg(t, conn)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
				t.Fatal(err)
			}
			_, _, err := conn.Read(ctx)
			if got := websocket.CloseStatus(err); got != websocket.StatusInvalidFramePayloadData {
				t.Fatalf("close status %v (err %v)", got, err)
			}
		})
	}
}

func TestWebSocketUnknownEntryClosesWithNotFound(t *testing.T) {
	srv, _ := newWSServer(t)
	conn := mustDial(t, srv, "doesnotexist")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != closeNotFound {
		t.Fatalf("close status %v (err %v)", got, err)
	}
}

func TestWebSocketOriginCheck(t *testing.T) {
	srv, store := newWSServer(t, "app.example.com", "*.preview.example.com")
	id := newEntry(t, store, "")

	for origin, wantOK := range map[string]bool{
		"https://app.example.com":          true,
		"https://pr-7.preview.example.com": true,
		"https://evil.example.org":         false,
		"https://app.example.com.evil.io":  false,
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
