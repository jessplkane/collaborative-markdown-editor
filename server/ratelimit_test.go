package main

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestLimiterAllowsABurstThenRefillsAtItsRate(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(10, 3, now) // 10 a second, up to 3 saved up

	for i := range 3 {
		if !l.allow(now) {
			t.Fatalf("message %d of the burst was refused", i+1)
		}
	}
	if l.allow(now) {
		t.Fatal("a message past the burst was allowed")
	}

	// 150 ms at 10 a second is one and a half tokens: one message, not two.
	now = now.Add(150 * time.Millisecond)
	if !l.allow(now) {
		t.Fatal("a refilled token was refused")
	}
	if l.allow(now) {
		t.Fatal("half a token was enough")
	}

	// Idle time saves up no more than the burst.
	now = now.Add(time.Hour)
	for i := range 3 {
		if !l.allow(now) {
			t.Fatalf("message %d after idling was refused", i+1)
		}
	}
	if l.allow(now) {
		t.Fatal("idling saved up more than the burst")
	}
}

func TestWebSocketClosesAConnectionThatSendsTooFast(t *testing.T) {
	srv, store := newWSServerWith(t, func(h *Hub) {
		// Practically no refill, so the outcome does not depend on timing.
		h.messageRate, h.messageBurst = 0.001, 3
	})
	conn := mustDial(t, srv, newEntry(t, store, ""))
	readServerMsg(t, conn)

	for range 3 {
		sendJSON(t, conn, map[string]string{"type": "ping"})
		if m := readServerMsg(t, conn); m.Type != "pong" {
			t.Fatalf("got %+v", m)
		}
	}

	sendJSON(t, conn, map[string]string{"type": "ping"})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("close status %v (err %v)", got, err)
	}
}
