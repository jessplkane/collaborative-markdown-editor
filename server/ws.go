package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/coder/websocket"
)

const (
	// maxMessageBytes caps one incoming frame. The library default of 32 KiB
	// would drop the connection on a modest paste.
	maxMessageBytes = 2 << 20

	// clientSendBuffer is how many outgoing messages may queue for one
	// connection before it is dropped as too slow.
	clientSendBuffer = 256

	// closeNotFound is the application close code for an unknown entry.
	closeNotFound = websocket.StatusCode(4004)
)

// ServeWS handles /ws/entries/{id}: it joins the entry's room and then relays
// between the socket and the room until either side goes away.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.originPatterns,
	})
	if err != nil {
		return // Accept has already written the HTTP error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxMessageBytes)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c := &Client{send: make(chan []byte, clientSendBuffer)}
	var kicked atomic.Bool
	c.kick = func(reason string) {
		if kicked.CompareAndSwap(false, true) {
			// Close blocks for the closing handshake; the room must not wait.
			go conn.Close(websocket.StatusPolicyViolation, reason)
		}
	}

	room, err := h.Join(ctx, r.PathValue("id"), c)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			conn.Close(closeNotFound, "entry not found")
		} else {
			log.Printf("ws join: %v", err)
			conn.Close(websocket.StatusInternalError, "internal error")
		}
		return
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case msg := <-c.send:
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		var m clientMessage
		if err := json.Unmarshal(data, &m); err != nil ||
			m.Type != "update" || m.ClientID == "" || len(m.Updates) == 0 {
			conn.Close(websocket.StatusInvalidFramePayloadData, "bad message")
			break
		}
		room.update(c, m.ClientID, m.Version, m.Updates)
	}

	cancel()
	<-writerDone
	room.leave(c)
}
